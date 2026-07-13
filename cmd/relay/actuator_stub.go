//go:build !ffi_cgo

package main

import "centralrelay/core/modules/httpcap"

// newActuator returns nil in the default (CGO-disabled) build: the `http.request`
// capability is unavailable unless the relay is linked against the Rust FFI
// bridge. registerNativeComponents skips registering `http@1.0` when the actuator
// is nil, so the default relay behaves exactly as before.
//
// Build the relay with `-tags ffi_cgo` and CGO_ENABLED=1 (linking libcic_ffi.a)
// to get the live capability — see actuator_cgo.go.
func newActuator() httpcap.Actuator { return nil }
