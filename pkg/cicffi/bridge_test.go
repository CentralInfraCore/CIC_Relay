//go:build ffi_cgo

package cicffi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
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
