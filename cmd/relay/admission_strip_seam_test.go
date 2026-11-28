//go:build cic_test_seams

package main

// AC-162-18 (tagged half): built only with -tags cic_test_seams, the one build in which the wasm-canon/v1
// transformation can be replaced. If it is replaced, so that the executed bytes are no longer the ones the envelope
// names, the admission rejects the module with EXECUTED_DIGEST_MISMATCH: the seam can not admit a different
// executed identity even where it exists.

import (
	"testing"

	"centralrelay/core/cabinet"
)

// TestAdmission_AC18_ChangedTransformationIsRejected replaces the transformation after the release was signed.
func TestAdmission_AC18_ChangedTransformationIsRejected(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, wasmWithCustom)
	f.resign(t, nil) // signed over the genuine executed digest

	previous := cabinet.SetStripWasmModuleFunc(func(raw []byte) ([]byte, error) { return append([]byte(nil), raw...), nil }) // "no strip"
	t.Cleanup(func() { cabinet.SetStripWasmModuleFunc(previous) })
	requireRejected(t, f, codeExecutedDigestMismatch)
}
