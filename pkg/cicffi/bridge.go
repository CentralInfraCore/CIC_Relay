//go:build ffi_cgo

// Package cicffi is the Go side of the Rust↔Go FFI bridge. It links the
// cic-ffi staticlib (the unsafe airlock) and exposes safe Go wrappers over
// the C-ABI exports. See features/feature-008/ffi-boundary.md.
//
// This file is compiled only under the `ffi_cgo` build tag with CGO_ENABLED=1;
// the default build uses the stub in bridge_stub.go, so the rest of the tree
// builds without the Rust staticlib.
package cicffi

/*
#cgo LDFLAGS: -lcic_ffi

#include <stddef.h>

// Mirrors the exports in ffi/src/lib.rs (the unsafe airlock). The -L path to
// libcic_ffi.a is supplied at build time via CGO_LDFLAGS (see the Makefile).
unsigned char *cic_ffi_ping(const unsigned char *ptr, size_t len, size_t *out_len);
unsigned char *cic_ffi_connection_request(const unsigned char *ptr, size_t len, size_t *out_len);
void cic_ffi_free(unsigned char *ptr, size_t len);
*/
import "C"

import (
	"errors"
	"unsafe"
)

// invoke runs one airlock call: it hands `in` to a C export `fn`, copies the
// Rust-owned result into Go memory, and frees the Rust buffer. It returns nil
// when the export returned null/empty (its rejection or empty signal), so no
// Rust memory is retained by the caller.
func invoke(in []byte, fn func(*C.uchar, C.size_t, *C.size_t) *C.uchar) []byte {
	var inPtr *C.uchar
	if len(in) > 0 {
		// Valid only for the duration of the call; the Rust airlock copies the
		// bytes into owned memory on ingress and does not retain the pointer.
		inPtr = (*C.uchar)(unsafe.Pointer(&in[0]))
	}

	var outLen C.size_t
	out := fn(inPtr, C.size_t(len(in)), &outLen)
	if out == nil || outLen == 0 {
		return nil
	}
	// Copy Rust-owned bytes into Go memory, then release the Rust buffer.
	defer C.cic_ffi_free(out, outLen)
	return C.GoBytes(unsafe.Pointer(out), C.int(outLen))
}

// Ping sends bytes across the FFI boundary into the Rust airlock and returns
// the echoed bytes (the linkability smoke test). Empty input yields nil.
func Ping(in []byte) []byte {
	return invoke(in, func(p *C.uchar, n C.size_t, o *C.size_t) *C.uchar {
		return C.cic_ffi_ping(p, n, o)
	})
}

// ConnectionRequest builds a canonical Vault Connection request from a JSON
// {"subject": ..., "scope": [...]} document via the Rust core, returning the
// canonical JSON (deduplicated, sorted scope). It returns an error when the
// Rust core rejected the input (malformed JSON or an empty subject).
func ConnectionRequest(reqJSON []byte) ([]byte, error) {
	out := invoke(reqJSON, func(p *C.uchar, n C.size_t, o *C.size_t) *C.uchar {
		return C.cic_ffi_connection_request(p, n, o)
	})
	if out == nil {
		return nil, errors.New("cicffi: connection request rejected (malformed JSON or empty subject)")
	}
	return out, nil
}
