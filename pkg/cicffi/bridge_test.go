//go:build ffi_cgo

package cicffi

import (
	"bytes"
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
