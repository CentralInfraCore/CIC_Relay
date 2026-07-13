//go:build !ffi_cgo

// Package cicffi is the Go side of the Rust↔Go FFI bridge. This stub is
// compiled when the `ffi_cgo` tag is absent (the default, CGO-disabled build),
// so the rest of the tree compiles without the Rust staticlib. Build with
// `-tags ffi_cgo` and CGO_ENABLED=1 to link the real bridge.
// See features/feature-008/ffi-boundary.md.
package cicffi

const disabled = "cicffi: built without the ffi_cgo build tag (CGO bridge disabled)"

// ExpectedABIVersion is the C-ABI version this package targets (see the cgo
// build for the checked value).
const ExpectedABIVersion uint32 = 3

// ABIVersion is unavailable in the default (non-cgo) build.
func ABIVersion() uint32 {
	panic(disabled)
}

// CheckABI is unavailable in the default (non-cgo) build.
func CheckABI() error {
	panic(disabled)
}

// Ping is unavailable in the default (non-cgo) build. Rebuild with the
// `ffi_cgo` tag and CGO_ENABLED=1 to link the Rust airlock.
func Ping(_ []byte) []byte {
	panic(disabled)
}

// ConnectionRequest is unavailable in the default (non-cgo) build. Rebuild with
// the `ffi_cgo` tag and CGO_ENABLED=1 to link the Rust airlock.
func ConnectionRequest(_ []byte) ([]byte, error) {
	panic(disabled)
}

// RunFlow is unavailable in the default (non-cgo) build. Rebuild with the
// `ffi_cgo` tag and CGO_ENABLED=1 to link the Rust airlock.
func RunFlow(_ []byte) ([]byte, error) {
	panic(disabled)
}
