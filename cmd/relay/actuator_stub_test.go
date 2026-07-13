//go:build !ffi_cgo

package main

import "testing"

// TestNewActuatorNilInDefaultBuild pins the default-build contract: without the
// ffi_cgo bridge there is no actuator, so registerNativeComponents skips the
// http.request capability and the relay behaves exactly as before. (The adaptive
// module-count assertion in TestRegisterNativeComponents confirms the capability
// is absent from the registered set in this build.)
func TestNewActuatorNilInDefaultBuild(t *testing.T) {
	if newActuator() != nil {
		t.Fatal("newActuator() must be nil in the default (non-ffi_cgo) build")
	}
}
