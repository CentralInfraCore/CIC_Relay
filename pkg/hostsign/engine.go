package hostsign

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// zeroDigest stands in for the signing profile digest of a request that named an unknown profile
// (there is no profile to take a digest from; the record schema requires one).
const zeroDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// Config is everything one Engine needs. The guest supplies none of it.
type Config struct {
	Policy   Policy
	Profiles []Profile
	// Keys resolves a guest-named key_ref to an authorised key of THIS module; false means
	// KEY_REF_UNAUTHORIZED. A nil Keys authorises nothing (fail-closed).
	Keys func(keyRef string) (Key, bool)
	// Egress is the module-manifest egress check (manifest ∩ policy). Nil denies everything.
	Egress func(method, host string, port int, path string) bool
	Doer   Doer
	// Now is the host clock; the request date always comes from here. Nil means time.Now.
	Now func() time.Time
	// ProfileDigest is the digest of the module's verified ABI profile / project document that is
	// recorded as profile_digest.
	ProfileDigest string
	Log           *Log
}

// Engine runs the fixed sign+send step order for one module. Safe for concurrent use.
type Engine struct {
	cfg      Config
	policyD  string
	profiles map[string]Profile
}

// NewEngine validates the configuration (fail-closed) and returns an Engine.
func NewEngine(cfg Config) (*Engine, error) {
	if err := cfg.Policy.Validate(); err != nil {
		return nil, err
	}
	if cfg.Doer == nil {
		return nil, errors.New("hostsign: no HTTP doer")
	}
	if !sha256Re.MatchString(cfg.ProfileDigest) {
		return nil, errors.New("hostsign: profile digest must be sha256:<hex>")
	}
	if cfg.Log == nil {
		cfg.Log = NewLog()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	e := &Engine{cfg: cfg, policyD: cfg.Policy.Digest(), profiles: map[string]Profile{}}
	for _, p := range cfg.Profiles {
		if _, err := effectiveLimits(cfg.Policy.Limits, p.Limits()); err != nil {
			return nil, fmt.Errorf("profile %s: %w", p.ID(), err)
		}
		e.profiles[p.ID()] = p
	}
	// A redirect must never be followed: the egress check only saw the first URL.
	if c, ok := cfg.Doer.(*http.Client); ok {
		cc := *c
		cc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		e.cfg.Doer = &cc
	}
	return e, nil
}

// Log returns the interaction log of this engine.
func (e *Engine) Log() *Log { return e.cfg.Log }

// wireBody is a type of the cic-flow.send host capability.
type wireBody struct {
	Base64 string `json:"base64"`
}

// wireRequest is a type of the cic-flow.send host capability.
type wireRequest struct {
	V              int    `json:"v"`
	SigningProfile string `json:"signing_profile"`
	KeyRef         string `json:"key_ref"`
	Request        struct {
		Method  string            `json:"method"`
		Host    string            `json:"host"`
		Path    string            `json:"path"`
		Query   map[string]string `json:"query"`
		Headers map[string]string `json:"headers"`
		Body    *wireBody         `json:"body"`
	} `json:"request"`
	Intent  *RecordIntent `json:"intent"`
	TraceID string        `json:"trace_id"`
}

// Fault is a denial or failure entry of a response.
type Fault struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// HTTPResult is the allowed part of the upstream response a guest sees.
type HTTPResult struct {
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
	BodySHA256 string            `json:"body_sha256"`
}

// Response is the guest-visible answer (wire: sign-send-response.schema.json). It has no field
// that could carry a signature or an Authorization header.
type Response struct {
	V                 int         `json:"v"`
	Outcome           string      `json:"outcome"` // completed | denied | failed
	InteractionID     string      `json:"interaction_id"`
	InteractionDigest string      `json:"interaction_digest"`
	Denial            *Fault      `json:"denial,omitempty"`
	Failure           *Fault      `json:"failure,omitempty"`
	HTTP              *HTTPResult `json:"http,omitempty"`
}

// InvalidResponse is the answer to a request that is not a well-formed v1 request.
type InvalidResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

var (
	// profileRe is a package-level table or pattern of the cic-flow.send host capability.
	profileRe = regexp.MustCompile(`^[a-z][a-z0-9-]*:[a-z0-9-]+:[a-z0-9-]+@[0-9]+$`)
	// keyRefRe is a package-level table or pattern of the cic-flow.send host capability.
	keyRefRe = regexp.MustCompile(`^[a-z]+/[a-z0-9-]+$`)
	// hostRe is a package-level table or pattern of the cic-flow.send host capability.
	hostRe = regexp.MustCompile(`^[a-z0-9.-]+$`)
	// pathRe is a package-level table or pattern of the cic-flow.send host capability.
	pathRe = regexp.MustCompile(`^/[^?#\s]*$`)
	// headerRe is a package-level table or pattern of the cic-flow.send host capability.
	headerRe = regexp.MustCompile(`^[a-z0-9-]+$`)
	// tokenRe is a package-level table or pattern of the cic-flow.send host capability.
	tokenRe = regexp.MustCompile(`^[\x21-\x7e]+$`)
	// hdrValueRe is a package-level table or pattern of the cic-flow.send host capability.
	hdrValueRe = regexp.MustCompile(`^[\x20-\x7e]*$`)
)

// sha is a helper of the cic-flow.send host capability.
func sha(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// invalid is a helper of the cic-flow.send host capability.
func invalid(msg string) []byte {
	b, _ := json.Marshal(InvalidResponse{Error: msg, Code: CodeRequestInvalid})
	return b
}

// parse decodes and structurally validates the request (schema step); the forbidden-header check
// is separate so that it can be recorded as a denial.
func parse(raw []byte) (*wireRequest, []byte, error) {
	var w wireRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return nil, nil, fmt.Errorf("bad request JSON: %v", err)
	}
	if dec.More() {
		return nil, nil, errors.New("bad request JSON: trailing data")
	}
	r := w.Request
	// The rules are checked in this order; the first violated one is reported.
	if w.V != 1 {
		return nil, nil, errors.New("v must be 1")
	}
	if !profileRe.MatchString(w.SigningProfile) {
		return nil, nil, errors.New("signing_profile malformed")
	}
	if !keyRefRe.MatchString(w.KeyRef) {
		return nil, nil, errors.New("key_ref malformed")
	}
	if !validMethods[r.Method] {
		return nil, nil, errors.New("request.method malformed")
	}
	if !hostRe.MatchString(r.Host) {
		return nil, nil, errors.New("request.host malformed")
	}
	if !pathRe.MatchString(r.Path) {
		return nil, nil, errors.New("request.path malformed (no query in path)")
	}
	if w.Intent == nil {
		return nil, nil, errors.New("intent is required")
	}
	if !tokenRe.MatchString(w.Intent.Operation) || !tokenRe.MatchString(w.Intent.ResourceID) {
		return nil, nil, errors.New("intent.operation/resource_id malformed")
	}
	if !sha256Re.MatchString(w.Intent.BindingDigest) {
		return nil, nil, errors.New("intent.binding_digest is required and must be sha256:<hex>")
	}
	if len(w.TraceID) > 128 {
		return nil, nil, errors.New("trace_id too long")
	}
	for k, v := range r.Headers {
		if !headerRe.MatchString(strings.ToLower(k)) || !hdrValueRe.MatchString(v) {
			return nil, nil, fmt.Errorf("header %q malformed", k)
		}
	}
	var body []byte
	if r.Body != nil {
		b, err := base64.StdEncoding.DecodeString(r.Body.Base64)
		if err != nil {
			return nil, nil, errors.New("body.base64 not valid base64")
		}
		body = b
	}
	return &w, body, nil
}

// Handle runs one sign+send call in the enforced order and returns the JSON the guest receives:
//
//	schema → forbidden headers → body size → profile → key_ref → target policy (SS-03) →
//	egress → profile canonicalisation → signature → controlled HTTP → interaction record → response
//
// Nothing is signed or sent before the policy steps have passed. Every well-formed request,
// refused or not, gets exactly one interaction record.
func (e *Engine) Handle(ctx context.Context, raw []byte) []byte {
	if len(raw) > 2*e.cfg.Policy.Limits.MaxRequestBytes+(64<<10) {
		return invalid("request too large")
	}
	w, body, err := parse(raw)
	if err != nil {
		return invalid(err.Error())
	}
	rq := w.Request
	method := strings.ToUpper(rq.Method)

	prof := e.profiles[w.SigningProfile]
	sigDigest := zeroDigest
	if prof != nil {
		sigDigest = prof.Digest()
	}
	rec := Record{
		Method: method, Host: rq.Host, PathSHA256: sha([]byte(rq.Path)),
		PolicyDigest: e.policyD, ProfileDigest: e.cfg.ProfileDigest, SigningProfileDigest: sigDigest,
		Intent: *w.Intent,
	}
	if rq.Body != nil {
		d := sha(body)
		rec.RequestBodySHA256 = &d
	}
	deny := func(code, detail string) []byte {
		rec.Decision = "denied"
		rec.Code = &code
		return e.finish(rec, func(r Response) Response {
			r.Outcome, r.Denial = "denied", &Fault{Code: code, Detail: detail}
			return r
		})
	}
	fail := func(code, detail string, status *int) []byte {
		rec.Decision = "failed"
		rec.Code = &code
		rec.Status = status
		return e.finish(rec, func(r Response) Response {
			r.Outcome, r.Failure = "failed", &Fault{Code: code, Detail: detail}
			return r
		})
	}

	// 1b. forbidden request headers: refused, nothing signed.
	for k := range rq.Headers {
		if contains(ForbiddenRequestHeaders, strings.ToLower(k)) {
			return deny(CodeHeaderForbidden, "header "+strings.ToLower(k)+" is set by the host")
		}
	}
	// 1c. body size against the operator cap (a profile may only narrow it).
	var lim Limits
	if prof != nil {
		lim, err = effectiveLimits(e.cfg.Policy.Limits, prof.Limits())
		if err != nil {
			return fail(CodeSignerError, err.Error(), nil)
		}
	} else {
		lim = e.cfg.Policy.Limits
	}
	if len(body) > lim.MaxRequestBytes {
		return deny(CodeBodyTooLarge, "request body exceeds the limit")
	}
	if prof == nil {
		return deny(CodeProfileUnknown, "")
	}
	key, ok := Key{}, false
	if e.cfg.Keys != nil {
		key, ok = e.cfg.Keys(w.KeyRef)
	}
	if !ok || key.Signer == nil {
		return deny(CodeKeyRefUnauthorized, "")
	}
	// 2. policy: operation, host, method, resource, binding digest, path (SS-03). Before any signing.
	ti := TargetIntent{Operation: w.Intent.Operation, ResourceID: w.Intent.ResourceID, BindingDigest: w.Intent.BindingDigest}
	if code := e.cfg.Policy.ValidateTarget(ti, method, rq.Host, rq.Path, rq.Query); code != "" {
		return deny(code, "")
	}
	// 2b. the module manifest egress (manifest ∩ policy).
	if e.cfg.Egress == nil || !e.cfg.Egress(method, rq.Host, 443, rq.Path) {
		return deny(CodeEgressBlocked, "not permitted by the module manifest")
	}
	// 3. the signing profile builds the canonical string; the date is the host's.
	date := e.cfg.Now().UTC().Format(http.TimeFormat)
	guestHeaders := map[string]string{}
	for k, v := range rq.Headers {
		guestHeaders[strings.ToLower(k)] = v
	}
	pr := PrepRequest{Method: method, Host: rq.Host, Path: rq.Path, Query: rq.Query, Headers: guestHeaders, Body: body}
	prep, err := prof.Prepare(pr, date)
	if err != nil {
		return fail(CodeSignerError, "profile: "+err.Error(), nil)
	}
	// 4. Vault signature (the key never leaves its backend).
	sig, err := key.Signer.Sign(ctx, prep.StringToSign)
	if err != nil {
		return fail(CodeSignerError, "signer failed", nil)
	}
	// 5. controlled HTTP.
	cctx, cancel := context.WithTimeout(ctx, time.Duration(lim.TimeoutMS)*time.Millisecond)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(cctx, method, "https://"+rq.Host+pr.Target(), bytes.NewReader(body))
	if err != nil {
		return fail(CodeTransportError, "request could not be built", nil)
	}
	if e.cfg.Policy.ObservationOnly && httpReq.Method != http.MethodGet && httpReq.Method != http.MethodHead {
		return deny(CodePolicyMethod, "observation profile")
	}
	httpReq.Host = rq.Host
	for k, v := range guestHeaders {
		httpReq.Header.Set(k, v)
	}
	for k, v := range prep.Headers {
		httpReq.Header.Set(k, v)
	}
	httpReq.Header.Set("authorization", prep.Authorize(key.KeyID, sig))
	resp, err := e.cfg.Doer.Do(httpReq)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fail(CodeTimeout, "request timed out", nil)
		case errors.Is(err, ErrEgressBlockedAddr), strings.Contains(err.Error(), ErrEgressBlockedAddr.Error()):
			return deny(CodeEgressBlocked, "destination address is blocked")
		}
		return fail(CodeTransportError, "transport error", nil)
	}
	defer func() { _ = resp.Body.Close() }()
	status := resp.StatusCode
	if resp.ContentLength > int64(lim.MaxResponseBytes) {
		return fail(CodeResponseTooLarge, "response exceeds the limit", &status)
	}
	rb, err := io.ReadAll(io.LimitReader(resp.Body, int64(lim.MaxResponseBytes)+1))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fail(CodeTimeout, "request timed out", &status)
		}
		return fail(CodeTransportError, "response could not be read", &status)
	}
	if len(rb) > lim.MaxResponseBytes {
		return fail(CodeResponseTooLarge, "response exceeds the limit", &status)
	}
	// 6+7. record, then the response: only allowlisted headers, never a signature or Authorization.
	rd := sha(rb)
	rec.Decision, rec.Status, rec.ResponseBodySHA256 = "allowed", &status, &rd
	hdrs := map[string]string{}
	for k := range resp.Header {
		lk := strings.ToLower(k)
		if contains(e.cfg.Policy.ResponseHeaderAllowlist, lk) && !contains(ForbiddenResponseHeaders, lk) {
			hdrs[lk] = resp.Header.Get(k)
		}
	}
	return e.finish(rec, func(r Response) Response {
		r.Outcome = "completed"
		r.HTTP = &HTTPResult{Status: status, Headers: hdrs, BodyBase64: base64.StdEncoding.EncodeToString(rb), BodySHA256: rd}
		return r
	})
}

// finish appends the record and marshals the response that references it.
func (e *Engine) finish(rec Record, fill func(Response) Response) []byte {
	r, digest, err := e.cfg.Log.Append(rec)
	if err != nil {
		return invalid("interaction could not be recorded")
	}
	out := fill(Response{V: 1, InteractionID: r.InteractionID, InteractionDigest: digest})
	b, _ := json.Marshal(out)
	return b
}

// ErrEgressBlockedAddr is the error a Doer returns when a connection would reach a blocked address
// range; cmd/relay wraps its egress client's error to match (errors.Is or the message).
var ErrEgressBlockedAddr = errors.New("egress: destination address is blocked (private, loopback, link-local, unspecified or broadcast)")
