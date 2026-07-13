//go:build ffi_cgo

package main

import (
	"centralrelay/core/modules/httpcap"
	"centralrelay/pkg/cicffi"
)

// ffiActuator backs the `http.request` capability with the Rust FFI trust-flow:
// its RunFlow is exactly cicffi.RunFlow, so the actuation (module CSR →
// countersign → seal → open → HTTP) runs inside Rust and only the non-secret
// outcome crosses back. Compiled only under the `ffi_cgo` build tag, where the
// staticlib is linked.
type ffiActuator struct{}

// RunFlow delegates to the cgo bridge.
func (ffiActuator) RunFlow(reqJSON []byte) ([]byte, error) { return cicffi.RunFlow(reqJSON) }

// newActuator returns the FFI-backed actuator, so registerNativeComponents
// registers the live `http@1.0` capability in a cgo-built relay.
func newActuator() httpcap.Actuator { return ffiActuator{} }
