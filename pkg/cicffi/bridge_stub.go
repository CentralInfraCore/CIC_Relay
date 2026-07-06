//go:build !ffi_cgo

// Package cicffi is the Go side of the Rust↔Go FFI bridge. This stub is
// compiled when the `ffi_cgo` tag is absent (the default, CGO-disabled build),
// so the rest of the tree compiles without the Rust staticlib. Build with
// `-tags ffi_cgo` and CGO_ENABLED=1 to link the real bridge.
// See features/feature-008/ffi-boundary.md.
package cicffi

// Ping is unavailable in the default (non-cgo) build. Rebuild with the
// `ffi_cgo` tag and CGO_ENABLED=1 to link the Rust airlock.
func Ping(_ []byte) []byte {
	panic("cicffi: built without the ffi_cgo build tag (CGO bridge disabled)")
}
