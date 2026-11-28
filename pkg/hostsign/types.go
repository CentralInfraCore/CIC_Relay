// Package hostsign is the generic seam of the `cic-flow.send` host capability (O-1 WP-04, D-11).
//
// A guest module hands the host a structured request description; the host validates the target
// against the operator policy, lets a provider signing profile build the canonical string, signs
// it through a key the guest cannot name, performs the controlled HTTP call and records the
// interaction. The guest never receives a signature or an Authorization header.
//
// This package and everything under core/... must not import a provider profile (see
// arch_test.go); concrete profiles live in sub-packages such as ociprofile and depend only on
// this seam plus the standard library.
package hostsign

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Denial codes: the request was refused before anything was signed or sent.
const (
	// CodePolicyHost is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodePolicyHost = "POLICY_HOST"
	// CodePolicyPath is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodePolicyPath = "POLICY_PATH"
	// CodePolicyMethod is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodePolicyMethod = "POLICY_METHOD"
	// CodePolicyTarget is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodePolicyTarget = "POLICY_TARGET"
	// CodePolicyBinding is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodePolicyBinding = "POLICY_BINDING"
	// CodeHeaderForbidden is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodeHeaderForbidden = "HEADER_FORBIDDEN"
	// CodeBodyTooLarge is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodeBodyTooLarge = "BODY_TOO_LARGE"
	// CodeProfileUnknown is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodeProfileUnknown = "PROFILE_UNKNOWN"
	// CodeKeyRefUnauthorized is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodeKeyRefUnauthorized = "KEY_REF_UNAUTHORIZED"
	// CodeEgressBlocked is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodeEgressBlocked = "EGRESS_BLOCKED"
)

// Failure codes: the call was admitted but did not complete.
const (
	// CodeSignerError is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodeSignerError = "SIGNER_ERROR"
	// CodeTransportError is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodeTransportError = "TRANSPORT_ERROR"
	// CodeResponseTooLarge is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodeResponseTooLarge = "RESPONSE_TOO_LARGE"
	// CodeTimeout is a named denial or failure code of the cic-flow.send host capability (03 section 6, SS-01..05).
	CodeTimeout = "TIMEOUT"
)

// CodeRequestInvalid answers a request that is not a well-formed v1 sign+send request at all. It is
// not part of the response schema (no interaction can be described for it) and carries no
// interaction record.
const CodeRequestInvalid = "REQUEST_INVALID"

// ForbiddenRequestHeaders are the headers a guest request may never carry: the host sets or
// derives them (SS-01..SS-04 of the contract).
var ForbiddenRequestHeaders = []string{"authorization", "date", "host", "content-length", "x-content-sha256", "signature"}

// ForbiddenResponseHeaders never go back to the guest, whatever the operator allowlist says.
var ForbiddenResponseHeaders = []string{"set-cookie", "authorization", "proxy-authenticate", "www-authenticate", "signature", "x-content-sha256"}

// Signer signs caller-built bytes with a key that never leaves its backend and returns the raw
// base64 signature. cmd/relay's Vault signer satisfies it.
type Signer interface {
	Sign(ctx context.Context, data []byte) (string, error)
}

// Key is a resolved, authorised signing key: the signer plus the provider key identifier the
// signing profile places in the Authorization header. The guest names neither.
type Key struct {
	Signer Signer
	KeyID  string
}

// Doer is the egress transport. *http.Client satisfies it; NewEngine disables redirect following
// on an *http.Client (a redirect to another host is never followed).
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// PrepRequest is the structured request as the signing profile sees it.
type PrepRequest struct {
	Method  string
	Host    string
	Path    string
	Query   map[string]string
	Headers map[string]string // guest headers, lowercase names; never the forbidden ones
	Body    []byte
}

// Target returns the request target: the path plus the canonically encoded query (keys sorted).
func (r PrepRequest) Target() string {
	if len(r.Query) == 0 {
		return r.Path
	}
	keys := make([]string, 0, len(r.Query))
	for k := range r.Query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(r.Query[k]))
	}
	return r.Path + "?" + strings.Join(parts, "&")
}

// Prepared is what a profile produces for one request.
type Prepared struct {
	// StringToSign is the canonical byte string handed to the signer.
	StringToSign []byte
	// Headers are the wire headers the host sets (date, content digest, content length, ...).
	Headers map[string]string
	// Authorize builds the Authorization header value from the key identifier and the signature.
	Authorize func(keyID, signatureB64 string) string
}

// Profile is a provider signing profile. Implementations must be pure and deterministic given
// their inputs; they do not see the key, the network or the policy.
type Profile interface {
	// ID is the signing profile identifier a guest request names (e.g. http-signature:oci:draft-cavage@1).
	ID() string
	// Digest is the digest of the profile definition; it is recorded in every host interaction.
	Digest() string
	// Limits may only narrow the operator limits (zero fields mean "no narrowing").
	Limits() Limits
	// Prepare builds the canonical string and the wire headers; date is the host-clock HTTP date.
	Prepare(r PrepRequest, date string) (*Prepared, error)
}
