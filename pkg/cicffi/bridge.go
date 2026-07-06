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
void cic_ffi_free(unsigned char *ptr, size_t len);
*/
import "C"

import "unsafe"

// Ping sends bytes across the FFI boundary into the Rust airlock and returns
// the echoed bytes. The Rust-owned result is copied into Go memory and then
// released via cic_ffi_free, so the returned slice is owned by the caller and
// no Rust memory is retained. Empty input yields a nil result.
func Ping(in []byte) []byte {
	var inPtr *C.uchar
	if len(in) > 0 {
		// Valid only for the duration of the call; the Rust airlock copies the
		// bytes into owned memory on ingress and does not retain the pointer.
		inPtr = (*C.uchar)(unsafe.Pointer(&in[0]))
	}

	var outLen C.size_t
	out := C.cic_ffi_ping(inPtr, C.size_t(len(in)), &outLen)
	if out == nil || outLen == 0 {
		return nil
	}
	// Copy Rust-owned bytes into Go memory, then release the Rust buffer.
	defer C.cic_ffi_free(out, outLen)
	return C.GoBytes(unsafe.Pointer(out), C.int(outLen))
}
