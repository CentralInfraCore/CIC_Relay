//go:build !ffi_cgo

// Package main — default (non-cgo) stub. The real demo needs the Rust FFI bridge
// and is compiled only under the `ffi_cgo` build tag; this stub keeps the default
// `go build ./...` working. Run the demo via `make demo-trustflow`.
package main

import "fmt"

// main only explains how to build and run the demo: without the ffi_cgo tag there is no Rust airlock to call.
func main() {
	fmt.Println("build with `-tags ffi_cgo` and the live cic-ffi staticlib — run via `make demo-trustflow`")
}
