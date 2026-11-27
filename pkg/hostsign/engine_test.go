package hostsign

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testHost = "iaas.eu-frankfurt-1.oraclecloud.com"
	resA     = "ocid1.vcn.oc1.eu-frankfurt-1.aaaa"
	digD     = "sha256:abababababababababababababababababababababababababababababababab"
	digOther = "sha256:1212121212121212121212121212121212121212121212121212121212121212"
	cannedBd = `{"id":"vcn"}`
)

// testProfile is a minimal signing profile so that the core tests need no provider package.
type testProfile struct{ lim Limits }

func (testProfile) ID() string       { return "http-signature:test:plain@1" }
func (testProfile) Digest() string   { return "sha256:" + strings.Repeat("5", 64) }
func (p testProfile) Limits() Limits { return p.lim }
func (testProfile) Prepare(r PrepRequest, date string) (*Prepared, error) {
	return &Prepared{
		StringToSign: []byte("(request-target): " + strings.ToLower(r.Method) + " " + r.Target() + "\ndate: " + date),
		Headers:      map[string]string{"date": date},
		Authorize:    func(keyID, sig string) string { return "Sig keyId=" + keyID + " sig=" + sig },
	}, nil
}

type countingSigner struct {
	mu    sync.Mutex
	calls int
	got   []byte
	err   error
}

func (s *countingSigner) Sign(_ context.Context, data []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.got = data
	return "SIGNATURE-VALUE-b64", s.err
}

type countingDoer struct {
	mu    sync.Mutex
	calls int
	got   *http.Request
	body  string
	hdr   http.Header
	code  int
	err   error
	block bool
}

func (d *countingDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.calls++
	d.got = req
	d.mu.Unlock()
	if d.block {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	if d.err != nil {
		return nil, d.err
	}
	h := d.hdr
	if h == nil {
		h = http.Header{}
	}
	code := d.code
	if code == 0 {
		code = 200
	}
	body := d.body
	if body == "" {
		body = cannedBd
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
}

func refPolicy() Policy {
	return Policy{
		Hosts: []string{testHost},
		Operations: map[string]OperationPolicy{
			"observe": {Methods: []string{"GET", "HEAD"}, PathTemplate: "/20160918/vcns/{resource_id}"},
			"destroy": {Methods: []string{"DELETE"}, PathTemplate: "/20160918/vcns/{resource_id}"},
			"execute": {Methods: []string{"PUT"}, PathTemplate: "/20160918/vcns/{resource_id}"},
		},
		Bindings:                map[string]string{resA: digD},
		ResponseHeaderAllowlist: []string{"etag", "opc-request-id", "set-cookie", "authorization"},
		Limits:                  Limits{TimeoutMS: 2000, MaxRequestBytes: 1024, MaxResponseBytes: 1024},
	}
}

type rig struct {
	e   *Engine
	sg  *countingSigner
	dc  *countingDoer
	now time.Time
}

func newRig(t *testing.T, mut func(*Config)) *rig {
	t.Helper()
	r := &rig{sg: &countingSigner{}, dc: &countingDoer{}, now: time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)}
	cfg := Config{
		Policy:        refPolicy(),
		Profiles:      []Profile{testProfile{}},
		Keys:          func(ref string) (Key, bool) { return Key{Signer: r.sg, KeyID: "ten/usr/fp"}, ref == "oci/default" },
		Egress:        func(m, h string, p int, path string) bool { return h == testHost && p == 443 },
		Doer:          r.dc,
		Now:           func() time.Time { return r.now },
		ProfileDigest: digOther,
	}
	if mut != nil {
		mut(&cfg)
	}
	e, err := NewEngine(cfg)
	require.NoError(t, err)
	r.e = e
	return r
}

func baseReq() map[string]any {
	return map[string]any{
		"v": 1, "signing_profile": "http-signature:test:plain@1", "key_ref": "oci/default",
		"request":  map[string]any{"method": "GET", "host": testHost, "path": "/20160918/vcns/" + resA, "headers": map[string]any{"accept": "application/json"}},
		"intent":   map[string]any{"operation": "observe", "resource_id": resA, "binding_digest": digD},
		"trace_id": "guest-chosen",
	}
}

func (r *rig) do(t *testing.T, req map[string]any) (Response, []byte) {
	t.Helper()
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	out := r.e.Handle(context.Background(), raw)
	var resp Response
	require.NoError(t, json.Unmarshal(out, &resp), string(out))
	return resp, out
}

func sub(m map[string]any, k string) map[string]any { return m[k].(map[string]any) }

func (r *rig) assertNothingLeft(t *testing.T) {
	t.Helper()
	assert.Zero(t, r.sg.calls, "the signer (Vault) must not be called")
	assert.Zero(t, r.dc.calls, "nothing may be sent")
}

// AC-SS-03: the ten reference cases (contract SS-03 / reference validate_target), end to end through
// the production Handle path: a refused request has the named code, no signature call, no send,
// and a denied interaction record carrying that code.
func TestAC_SS03_ReferenceCases(t *testing.T) {
	cases := []struct {
		name string
		mut  func(m map[string]any)
		code string
	}{
		{"target-valid", func(m map[string]any) {}, ""},
		{"host-outside-policy", func(m map[string]any) { sub(m, "request")["host"] = "evil.example.com" }, CodePolicyHost},
		{"write-method-in-observation", func(m map[string]any) { sub(m, "request")["method"] = "DELETE" }, CodePolicyMethod},
		{"unknown-operation", func(m map[string]any) { sub(m, "intent")["operation"] = "nuke" }, CodePolicyTarget},
		{"resource-not-bound", func(m map[string]any) { sub(m, "intent")["resource_id"] = "ocid1.vcn.oc1.eu-frankfurt-1.zzzz" }, CodePolicyTarget},
		{"binding-digest-differs", func(m map[string]any) { sub(m, "intent")["binding_digest"] = digOther }, CodePolicyBinding},
		{"path-targets-other-resource", func(m map[string]any) { sub(m, "request")["path"] = "/20160918/vcns/ocid1.vcn.oc1.eu-frankfurt-1.bbbb" }, CodePolicyPath},
		{"path-traversal", func(m map[string]any) { sub(m, "request")["path"] = "/20160918/vcns/../vcns/" + resA }, CodePolicyPath},
		{"encoded-traversal", func(m map[string]any) { sub(m, "request")["path"] = "/20160918/vcns/%2e%2e/x" }, CodePolicyPath},
		{"extra-query", func(m map[string]any) { sub(m, "request")["query"] = map[string]any{"x": "1"} }, CodePolicyPath},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, nil)
			req := baseReq()
			c.mut(req)
			resp, _ := r.do(t, req)
			recs := r.e.Log().Records()
			require.Len(t, recs, 1, "every well-formed call gets exactly one record")
			if c.code == "" {
				assert.Equal(t, "completed", resp.Outcome)
				assert.Equal(t, 1, r.sg.calls)
				assert.Equal(t, "allowed", recs[0].Decision)
				return
			}
			assert.Equal(t, "denied", resp.Outcome)
			require.NotNil(t, resp.Denial)
			assert.Equal(t, c.code, resp.Denial.Code)
			r.assertNothingLeft(t)
			assert.Equal(t, "denied", recs[0].Decision)
			require.NotNil(t, recs[0].Code)
			assert.Equal(t, c.code, *recs[0].Code)
			assert.Nil(t, recs[0].Status)
		})
	}
}

// The six SS-03 steps are checked in a fixed order: when several are violated the first one wins.
func TestAC_SS03_StepOrder(t *testing.T) {
	r := newRig(t, nil)
	req := baseReq()
	sub(req, "intent")["operation"] = "nuke"
	sub(req, "request")["host"] = "evil.example.com"
	resp, _ := r.do(t, req)
	assert.Equal(t, CodePolicyTarget, resp.Denial.Code, "(1) operation before (2) host")

	req = baseReq()
	sub(req, "request")["host"] = "evil.example.com"
	sub(req, "request")["method"] = "DELETE"
	resp, _ = r.do(t, req)
	assert.Equal(t, CodePolicyHost, resp.Denial.Code, "(2) host before (3) method")

	req = baseReq()
	sub(req, "request")["method"] = "DELETE"
	sub(req, "intent")["resource_id"] = "ocid1.vcn.oc1.eu-frankfurt-1.zzzz"
	resp, _ = r.do(t, req)
	assert.Equal(t, CodePolicyMethod, resp.Denial.Code, "(3) method before (4) resource")

	req = baseReq()
	sub(req, "intent")["resource_id"] = "ocid1.vcn.oc1.eu-frankfurt-1.zzzz"
	sub(req, "intent")["binding_digest"] = digOther
	resp, _ = r.do(t, req)
	assert.Equal(t, CodePolicyTarget, resp.Denial.Code, "(4) resource before (5) binding")

	req = baseReq()
	sub(req, "intent")["binding_digest"] = digOther
	sub(req, "request")["path"] = "/x"
	resp, _ = r.do(t, req)
	assert.Equal(t, CodePolicyBinding, resp.Denial.Code, "(5) binding before (6) path")
	r.assertNothingLeft(t)
}

// AC-SS-02: every forbidden request header is refused with HEADER_FORBIDDEN, nothing is signed.
func TestAC_SS02_ForbiddenHeaders(t *testing.T) {
	for _, h := range []string{"authorization", "date", "host", "content-length", "x-content-sha256", "signature", "Authorization", "DATE"} {
		t.Run(h, func(t *testing.T) {
			r := newRig(t, nil)
			req := baseReq()
			sub(sub(req, "request"), "headers")[h] = "x"
			resp, _ := r.do(t, req)
			assert.Equal(t, "denied", resp.Outcome)
			require.NotNil(t, resp.Denial)
			assert.Equal(t, CodeHeaderForbidden, resp.Denial.Code)
			r.assertNothingLeft(t)
			recs := r.e.Log().Records()
			require.Len(t, recs, 1)
			assert.Equal(t, CodeHeaderForbidden, *recs[0].Code)
		})
	}
}

// AC-SS-04: the date comes from the host clock; a guest-supplied date is refused.
func TestAC_SS04_DateFromHostClock(t *testing.T) {
	r := newRig(t, nil)
	resp, _ := r.do(t, baseReq())
	require.Equal(t, "completed", resp.Outcome)
	want := "Fri, 27 Nov 2026 12:00:00 GMT"
	assert.Equal(t, want, r.dc.got.Header.Get("date"))
	assert.Contains(t, string(r.sg.got), "date: "+want, "the signed string carries the host date")

	r.now = r.now.Add(time.Hour)
	_, _ = r.do(t, baseReq())
	assert.Equal(t, "Fri, 27 Nov 2026 13:00:00 GMT", r.dc.got.Header.Get("date"), "each call reads the clock")

	r2 := newRig(t, nil)
	req := baseReq()
	sub(sub(req, "request"), "headers")["date"] = "Mon, 01 Jan 2001 00:00:00 GMT"
	resp, _ = r2.do(t, req)
	assert.Equal(t, CodeHeaderForbidden, resp.Denial.Code)
	r2.assertNothingLeft(t)
}

func walkKeys(v any, f func(k string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			f(k)
			walkKeys(vv, f)
		}
	case []any:
		for _, vv := range x {
			walkKeys(vv, f)
		}
	}
}

// AC-SS-01: the response never carries a signature or an Authorization header, even when the
// upstream returns such headers and the operator allowlist (wrongly) names them; the upstream
// request did carry the host-built Authorization (so signing really happened).
func TestAC_SS01_ResponseHasNoSignatureOrAuthorization(t *testing.T) {
	r := newRig(t, nil)
	r.dc.hdr = http.Header{
		"Etag": {"e1"}, "Opc-Request-Id": {"r1"}, "Set-Cookie": {"a=b"}, "Authorization": {"Bearer x"},
		"Www-Authenticate": {"Basic"}, "Signature": {"s"}, "X-Content-Sha256": {"d"}, "X-Other": {"o"},
	}
	resp, raw := r.do(t, baseReq())
	require.Equal(t, "completed", resp.Outcome)
	assert.Contains(t, r.dc.got.Header.Get("authorization"), "SIGNATURE-VALUE-b64", "the host signed and sent")
	assert.NotContains(t, string(raw), "SIGNATURE-VALUE-b64")
	assert.NotContains(t, strings.ToLower(string(raw)), "sig keyid")
	var generic map[string]any
	require.NoError(t, json.Unmarshal(raw, &generic))
	walkKeys(generic, func(k string) {
		lk := strings.ToLower(k)
		assert.NotContains(t, []string{"signature", "authorization", "set-cookie", "www-authenticate", "x-content-sha256", "x-other"}, lk)
	})
	assert.Equal(t, map[string]string{"etag": "e1", "opc-request-id": "r1"}, resp.HTTP.Headers, "only operator-allowlisted, non-forbidden headers")

	// the response type itself has no field that could carry them
	assert.NotContains(t, string(mustJSON(t, Response{})), "signature")
	assert.NotContains(t, strings.ToLower(string(mustJSON(t, Response{HTTP: &HTTPResult{}}))), "authorization")
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// AC-SS-05 + AC-SS-08 + AC-SS-09 (live): denied and failed calls get records; the record carries
// the signing profile digest and the policy digest; the response references the record digest; the
// list digest over the log verifies and has no gaps.
func TestAC_SS05_DeniedAndFailedAreRecorded(t *testing.T) {
	r := newRig(t, nil)
	// 0: allowed
	resp0, _ := r.do(t, baseReq())
	require.Equal(t, "completed", resp0.Outcome)
	// 1: denied
	req := baseReq()
	sub(req, "request")["method"] = "DELETE"
	resp1, _ := r.do(t, req)
	require.Equal(t, "denied", resp1.Outcome)
	// 2: failed (transport)
	r.dc.err = errors.New("connection reset")
	resp2, _ := r.do(t, baseReq())
	require.Equal(t, "failed", resp2.Outcome)
	assert.Equal(t, CodeTransportError, resp2.Failure.Code)
	r.dc.err = nil
	// 3: failed (signer)
	r.sg.err = errors.New("vault down")
	resp3, _ := r.do(t, baseReq())
	assert.Equal(t, "failed", resp3.Outcome)
	assert.Equal(t, CodeSignerError, resp3.Failure.Code)
	r.sg.err = nil

	recs := r.e.Log().Records()
	require.Len(t, recs, 4)
	wantDecision := []string{"allowed", "denied", "failed", "failed"}
	for i, rec := range recs {
		assert.Equal(t, i, rec.Seq)
		assert.Equal(t, wantDecision[i], rec.Decision)
		assert.Equal(t, testProfile{}.Digest(), rec.SigningProfileDigest, "AC-SS-08: signing profile digest in every record")
		assert.Equal(t, r.e.policyD, rec.PolicyDigest)
		assert.Equal(t, digOther, rec.ProfileDigest)
		assert.Equal(t, "observe", rec.Intent.Operation)
	}
	assert.Nil(t, recs[0].Code)
	require.NotNil(t, recs[0].Status)
	assert.Equal(t, 200, *recs[0].Status)
	assert.Equal(t, CodePolicyMethod, *recs[1].Code)
	assert.Equal(t, CodeTransportError, *recs[2].Code)
	assert.Equal(t, CodeSignerError, *recs[3].Code)
	for i, resp := range []Response{resp0, resp1, resp2, resp3} {
		d, err := RecordDigest(recs[i])
		require.NoError(t, err)
		assert.Equal(t, d, resp.InteractionDigest, "the response references the record digest")
		assert.Equal(t, recs[i].InteractionID, resp.InteractionID)
	}
	d, err := r.e.Log().Digest()
	require.NoError(t, err)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, d)

	drained := r.e.Log().Drain()
	assert.Len(t, drained, 4)
	assert.Empty(t, r.e.Log().Records())
	_, _ = r.do(t, baseReq())
	assert.Equal(t, 0, r.e.Log().Records()[0].Seq, "a drained log restarts at seq 0")
}

// SS-05 failures: timeout and oversized response are failed + recorded.
func TestFailures_TimeoutAndResponseTooLarge(t *testing.T) {
	r := newRig(t, func(c *Config) { c.Policy.Limits.TimeoutMS = 50 })
	r.dc.block = true
	resp, _ := r.do(t, baseReq())
	require.Equal(t, "failed", resp.Outcome)
	assert.Equal(t, CodeTimeout, resp.Failure.Code)

	r2 := newRig(t, nil)
	r2.dc.body = strings.Repeat("x", 2000)
	resp, _ = r2.do(t, baseReq())
	require.Equal(t, "failed", resp.Outcome)
	assert.Equal(t, CodeResponseTooLarge, resp.Failure.Code)
	recs := r2.e.Log().Records()
	require.Len(t, recs, 1)
	assert.Equal(t, "failed", recs[0].Decision)
	assert.Nil(t, recs[0].ResponseBodySHA256)
}

// AC-SS-06: an observation profile lets no write method leave, even for an operation the policy
// otherwise lists with that method; checked on the outgoing request, before any signature.
func TestAC_SS06_ObservationProfileBlocksWrites(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		t.Run(m, func(t *testing.T) {
			r := newRig(t, func(c *Config) { c.Policy.ObservationOnly = true })
			op := map[string]string{"DELETE": "destroy", "PUT": "execute"}[m]
			if op == "" {
				op = "observe"
			}
			req := baseReq()
			sub(req, "request")["method"] = m
			sub(req, "intent")["operation"] = op
			resp, _ := r.do(t, req)
			assert.Equal(t, "denied", resp.Outcome)
			require.NotNil(t, resp.Denial)
			assert.Equal(t, CodePolicyMethod, resp.Denial.Code)
			r.assertNothingLeft(t)
		})
	}
	// the same write passes in a non-observation policy (so the block is the profile's doing)
	r := newRig(t, nil)
	req := baseReq()
	sub(req, "request")["method"] = "DELETE"
	sub(req, "intent")["operation"] = "destroy"
	resp, _ := r.do(t, req)
	assert.Equal(t, "completed", resp.Outcome)
	// and a read passes in the observation profile
	r = newRig(t, func(c *Config) { c.Policy.ObservationOnly = true })
	resp, _ = r.do(t, baseReq())
	assert.Equal(t, "completed", resp.Outcome)
}

// Profile / key / egress / size refusals: all before the signature.
func TestRefusalsBeforeSignature(t *testing.T) {
	t.Run("unknown profile", func(t *testing.T) {
		r := newRig(t, nil)
		req := baseReq()
		req["signing_profile"] = "http-signature:other:plain@1"
		resp, _ := r.do(t, req)
		assert.Equal(t, CodeProfileUnknown, resp.Denial.Code)
		r.assertNothingLeft(t)
		assert.Equal(t, zeroDigest, r.e.Log().Records()[0].SigningProfileDigest)
	})
	t.Run("unauthorised key_ref", func(t *testing.T) {
		r := newRig(t, nil)
		req := baseReq()
		req["key_ref"] = "oci/other"
		resp, _ := r.do(t, req)
		assert.Equal(t, CodeKeyRefUnauthorized, resp.Denial.Code)
		r.assertNothingLeft(t)
	})
	t.Run("egress not permitted by the manifest", func(t *testing.T) {
		r := newRig(t, func(c *Config) { c.Egress = func(string, string, int, string) bool { return false } })
		resp, _ := r.do(t, baseReq())
		assert.Equal(t, CodeEgressBlocked, resp.Denial.Code)
		r.assertNothingLeft(t)
	})
	t.Run("nil egress and nil keys deny everything", func(t *testing.T) {
		r := newRig(t, func(c *Config) { c.Egress = nil })
		resp, _ := r.do(t, baseReq())
		assert.Equal(t, CodeEgressBlocked, resp.Denial.Code)
		r = newRig(t, func(c *Config) { c.Keys = nil })
		resp, _ = r.do(t, baseReq())
		assert.Equal(t, CodeKeyRefUnauthorized, resp.Denial.Code)
	})
	t.Run("body too large", func(t *testing.T) {
		r := newRig(t, nil)
		req := baseReq()
		sub(req, "request")["method"] = "PUT"
		sub(req, "intent")["operation"] = "execute"
		sub(req, "request")["body"] = map[string]any{"base64": "QUFB" + strings.Repeat("QUFB", 400)}
		resp, _ := r.do(t, req)
		assert.Equal(t, CodeBodyTooLarge, resp.Denial.Code)
		r.assertNothingLeft(t)
	})
}

// SS-01 / SS-05 schema strictness: a request that is not a well-formed v1 request (no binding
// digest, unknown fields such as a guest-supplied signature or limit, query in the path) is
// refused with REQUEST_INVALID before anything happens.
func TestInvalidRequests(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"no binding_digest":    func(m map[string]any) { delete(sub(m, "intent"), "binding_digest") },
		"no intent":            func(m map[string]any) { delete(m, "intent") },
		"guest signature":      func(m map[string]any) { m["signature"] = "abc" },
		"guest limit":          func(m map[string]any) { m["timeout_ms"] = 1 },
		"guest limit in req":   func(m map[string]any) { sub(m, "request")["max_response_bytes"] = 9 },
		"query in path":        func(m map[string]any) { sub(m, "request")["path"] = "/x?a=b" },
		"v2":                   func(m map[string]any) { m["v"] = 2 },
		"bad base64":           func(m map[string]any) { sub(m, "request")["body"] = map[string]any{"base64": "!!"} },
		"newline in operation": func(m map[string]any) { sub(m, "intent")["operation"] = "a\nb" },
		"header injection":     func(m map[string]any) { sub(sub(m, "request"), "headers")["x-a"] = "a\r\nb: c" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, nil)
			req := baseReq()
			mut(req)
			raw, _ := json.Marshal(req)
			out := r.e.Handle(context.Background(), raw)
			var inv InvalidResponse
			require.NoError(t, json.Unmarshal(out, &inv))
			assert.Equal(t, CodeRequestInvalid, inv.Code, string(out))
			r.assertNothingLeft(t)
			assert.Empty(t, r.e.Log().Records())
		})
	}
	r := newRig(t, nil)
	out := r.e.Handle(context.Background(), []byte("not json"))
	assert.Contains(t, string(out), CodeRequestInvalid)
}

// SS-05: limits. The profile can only narrow the operator cap; wider is a configuration error.
func TestLimits(t *testing.T) {
	_, err := NewEngine(Config{
		Policy: refPolicy(), Profiles: []Profile{testProfile{lim: Limits{TimeoutMS: 99999}}},
		Doer: &countingDoer{}, ProfileDigest: digOther,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLimitExceedsOperatorCap)

	r := newRig(t, func(c *Config) { c.Profiles = []Profile{testProfile{lim: Limits{MaxResponseBytes: 5}}} })
	resp, _ := r.do(t, baseReq())
	assert.Equal(t, CodeResponseTooLarge, resp.Failure.Code, "effective limit = min(operator, profile)")
}

// "Redirect to another host is not followed": measured against a real *http.Client handed to the
// engine (which disables redirect following itself) and a server that redirects to another host.
func TestRedirectToOtherHostIsNotFollowed(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		hits[req.Host]++
		mu.Unlock()
		if req.Host == testHost {
			http.Redirect(w, req, "https://evil.example.com/steal", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("followed"))
	}))
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}} // note: default CheckRedirect would follow
	r := newRig(t, func(c *Config) { c.Doer = client })
	resp, _ := r.do(t, baseReq())
	require.Equal(t, "completed", resp.Outcome)
	assert.Equal(t, 302, resp.HTTP.Status, "the 3xx is returned, not followed")
	assert.NotContains(t, resp.HTTP.Headers, "location")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, hits[testHost])
	assert.Zero(t, hits["evil.example.com"], "the redirect target was never contacted")
}

func TestPolicyValidateAndDigest(t *testing.T) {
	assert.NoError(t, refPolicy().Validate())
	p := refPolicy()
	p.Limits.TimeoutMS = 0
	assert.Error(t, p.Validate())
	p = refPolicy()
	p.Hosts = []string{"*.oraclecloud.com"}
	assert.Error(t, p.Validate())
	p = refPolicy()
	p.Operations["observe"] = OperationPolicy{Methods: []string{"GET"}, PathTemplate: "/a/b"}
	assert.Error(t, p.Validate())
	p = refPolicy()
	p.Bindings["a/b"] = digD
	assert.Error(t, p.Validate())
	a, b := refPolicy(), refPolicy()
	assert.Equal(t, a.Digest(), b.Digest())
	b.Hosts = append(b.Hosts, "x.example.com")
	assert.NotEqual(t, a.Digest(), b.Digest())
	_ = bytes.MinRead
}
