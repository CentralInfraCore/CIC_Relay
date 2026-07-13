//go:build ffi_cgo

package cicffi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"centralrelay/core/cabinet"
)

// TestPingRoundTrip proves the live Go → Rust → Go path: bytes handed to the
// Rust airlock come back byte-identical through the C-ABI seam.
func TestPingRoundTrip(t *testing.T) {
	in := []byte("hello-cic-ffi")
	got := Ping(in)
	if !bytes.Equal(got, in) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, in)
	}
}

// TestPingEmpty checks the documented empty-input contract (nil result, no
// allocation to free).
func TestPingEmpty(t *testing.T) {
	if got := Ping(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
	if got := Ping([]byte{}); got != nil {
		t.Fatalf("expected nil for empty slice, got %v", got)
	}
}

// TestConnectionRequestCanonicalizes drives the first real trust-flow function
// through the seam: a JSON request with a duplicated, unsorted scope comes back
// deduplicated and sorted (AuthScope's BTreeSet), proving real connections-crate
// types crossed the boundary deterministically.
func TestConnectionRequestCanonicalizes(t *testing.T) {
	got, err := ConnectionRequest([]byte(`{"subject":"mod-x","scope":["cn-b","cn-a","cn-b"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var v struct {
		Subject string   `json:"subject"`
		Scope   []string `json:"scope"`
	}
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatalf("output not JSON: %v (%q)", err, got)
	}
	if v.Subject != "mod-x" {
		t.Fatalf("subject: got %q want %q", v.Subject, "mod-x")
	}
	if want := []string{"cn-a", "cn-b"}; !reflect.DeepEqual(v.Scope, want) {
		t.Fatalf("scope not deduped+sorted: got %v want %v", v.Scope, want)
	}
}

// TestConnectionRequestRejects checks that the seam rejects bad input with an
// error rather than crashing (the airlock's no-panic contract).
func TestConnectionRequestRejects(t *testing.T) {
	if _, err := ConnectionRequest([]byte("not json")); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if _, err := ConnectionRequest([]byte(`{"subject":""}`)); err == nil {
		t.Fatal("expected error for empty subject")
	}
}

// TestABIVersion checks that the Go package and the linked staticlib agree on
// the C-ABI version — the guard against a stale libcic_ffi.a link.
func TestABIVersion(t *testing.T) {
	if err := CheckABI(); err != nil {
		t.Fatalf("ABI check failed: %v", err)
	}
	if got := ABIVersion(); got != ExpectedABIVersion {
		t.Fatalf("ABIVersion: got %d want %d", got, ExpectedABIVersion)
	}
}

// TestPingRejectsOversizedInput drives the seam's ingress size cap (Rust-side
// MAX_INPUT_LEN = 1 MiB): oversized input is rejected, yielding nil.
func TestPingRejectsOversizedInput(t *testing.T) {
	big := make([]byte, 1024*1024+1)
	if got := Ping(big); got != nil {
		t.Fatalf("expected nil for oversized input, got %d bytes", len(got))
	}
}

// TestRunFlowActuatesWithOpenedCredential drives the full end-to-end PoC across
// the seam: Go hands in a request, Rust runs module-CSR → mock-countersign →
// seal → open → HTTP actuation against a real (loopback) server, and only the
// outcome crosses back. It proves the opened credential reached the endpoint as
// a Bearer token and that the secret never leaks back through the boundary.
func TestRunFlowActuatesWithOpenedCredential(t *testing.T) {
	const secret = "hvs.TOPSECRET"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	const traceID = "trace-xyz-789"
	reqJSON, err := json.Marshal(map[string]any{
		"subject":     "relay.worker",
		"scope":       []string{"db.read"},
		"secret":      secret,
		"target":      srv.URL + "/x",
		"allow_hosts": []string{"127.0.0.1"},
		"insecure":    true,
		"trace_id":    traceID,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	out, err := RunFlow(reqJSON)
	if err != nil {
		t.Fatalf("RunFlow rejected valid input: %v", err)
	}

	res, err := ParseRunFlowResult(out)
	if err != nil {
		t.Fatalf("result not JSON: %v (%q)", err, out)
	}
	if !res.OK {
		t.Fatalf("flow failed: %s", res.Error)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status: got %d want 200", res.Status)
	}
	if body, _ := base64.StdEncoding.DecodeString(res.BodyBase64); string(body) != "pong" {
		t.Fatalf("body: got %q want pong", body)
	}
	if want := []string{"db.read"}; !reflect.DeepEqual(res.Scope, want) {
		t.Fatalf("scope: got %v want %v", res.Scope, want)
	}
	// The opened credential reached the endpoint as a Bearer token.
	if gotAuth != "Bearer "+secret {
		t.Fatalf("authorization: got %q want %q", gotAuth, "Bearer "+secret)
	}
	// The secret never crosses back through the seam.
	if bytes.Contains(out, []byte(secret)) {
		t.Fatal("secret leaked into the FFI result")
	}

	// feature-009 bridge: the actuation's non-secret audit crosses the FFI seam
	// and folds into the Go relay's ProofTrace vocabulary.
	if res.Audit == nil {
		t.Fatal("audit: got nil, want the actuation's audit record")
	}
	if res.Audit.TraceID != traceID {
		t.Errorf("audit trace_id: got %q want %q", res.Audit.TraceID, traceID)
	}
	if res.Audit.Method != "GET" || res.Audit.Host != "127.0.0.1" || res.Audit.Decision != "allow" {
		t.Errorf("audit facts: %+v", res.Audit)
	}
	if len(res.Audit.RecordHash) != 64 {
		t.Errorf("audit record_hash: got %q, want 64-hex", res.Audit.RecordHash)
	}
	step := cabinet.ProofTraceStepFromActuation(
		res.Audit.TraceID, res.Audit.Method, res.Audit.Host,
		res.Audit.RequestBodySHA256, res.Audit.ResponseBodySHA256,
	)
	if step.Name != traceID || step.Module != "http-executor" {
		t.Errorf("ProofTraceStep: got %+v", step)
	}
	if h := cabinet.ComputeChainHashV1("cap-flow", "", []cabinet.ProofTraceStep{step}); len(h) != 64 {
		t.Errorf("chain hash over the actuation step: got %q", h)
	}
}

// TestRunFlowRejectsMalformed checks the seam's no-panic rejection contract for
// run-flow: bad input yields an error, not a crash.
func TestRunFlowRejectsMalformed(t *testing.T) {
	if _, err := RunFlow([]byte("not json")); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if _, err := RunFlow([]byte(`{"secret":"x","target":"http://127.0.0.1/x"}`)); err == nil {
		t.Fatal("expected error for missing subject")
	}
}

// TestRunFlowLive drives the FULL live-Vault trust-flow across the FFI: Go → Rust
// → real Vault (countersign → mTLS cert-auth login → read_sealed → open) → HTTP
// actuation → back to Go. It requires the `live-vault` staticlib and a running
// Vault (env pointed at it by `make test-ffi-live`); it skips otherwise.
func TestRunFlowLive(t *testing.T) {
	addr := os.Getenv("VAULT_LIVE_ADDR")
	if addr == "" {
		t.Skip("VAULT_LIVE_ADDR unset — run via `make test-ffi-live`")
	}
	caPem, err := os.ReadFile(os.Getenv("VAULT_LIVE_CACERT"))
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}

	const secret = "hvs.LIVE-FFI-SECRET"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	envOr := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	reqJSON, err := json.Marshal(map[string]any{
		"profile":     "live",
		"subject":     "relay.worker",
		"scope":       []string{"db.read"},
		"secret":      secret,
		"target":      srv.URL + "/x",
		"allow_hosts": []string{"127.0.0.1"},
		"insecure":    true,
		"vault": map[string]any{
			"addr":              addr,
			"token":             envOr("VAULT_LIVE_TOKEN", "root"),
			"cacert_pem_base64": base64.StdEncoding.EncodeToString(caPem),
			"pki_mount":         envOr("VAULT_LIVE_PKI_MOUNT", "cic-pki"),
			"pki_role":          envOr("VAULT_LIVE_PKI_ROLE", "cic-module"),
			"cert_mount":        envOr("VAULT_LIVE_CERT_MOUNT", "cic-cert"),
			"cert_role":         envOr("VAULT_LIVE_CERT_ROLE", "cic-module"),
			"kv_mount":          envOr("VAULT_LIVE_KV_MOUNT", "cic-kv"),
			"kv_path":           envOr("VAULT_LIVE_KV_PATH", "module-secret"),
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	out, err := RunFlow(reqJSON)
	if err != nil {
		t.Fatalf("RunFlow rejected valid live request: %v", err)
	}

	var res struct {
		OK         bool     `json:"ok"`
		Status     int      `json:"status"`
		BodyBase64 string   `json:"body_base64"`
		Scope      []string `json:"scope"`
		Error      string   `json:"error"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("result not JSON: %v (%q)", err, out)
	}
	if !res.OK {
		t.Fatalf("live flow failed: %s", res.Error)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status: got %d want 200", res.Status)
	}
	if body, _ := base64.StdEncoding.DecodeString(res.BodyBase64); string(body) != "pong" {
		t.Fatalf("body: got %q want pong", body)
	}
	if want := []string{"db.read"}; !reflect.DeepEqual(res.Scope, want) {
		t.Fatalf("scope: got %v want %v", res.Scope, want)
	}
	// The credential the endpoint received round-tripped through Vault (sealed →
	// read_sealed → opened) and was applied as a Bearer token.
	if gotAuth != "Bearer "+secret {
		t.Fatalf("authorization: got %q want %q", gotAuth, "Bearer "+secret)
	}
	// The secret never crosses back through the seam.
	if bytes.Contains(out, []byte(secret)) {
		t.Fatal("secret leaked into the FFI result")
	}
}
