//go:build cic_test_seams

package cabinet

// SetStripWasmModuleFunc overrides the wasm-canon/v1 transformation. It exists ONLY in a build with the
// cic_test_seams tag (#162 AC-162-18): no production build can reach it, so the executed identity of a module can not
// be changed at run time. Tests built with the tag use it to prove that a changed transformation output is rejected
// by the admission (EXECUTED_DIGEST_MISMATCH). It returns the previous transformation, to restore it.
func SetStripWasmModuleFunc(f func([]byte) ([]byte, error)) (previous func([]byte) ([]byte, error)) {
	previous, stripWasmModuleFunc = stripWasmModuleFunc, f
	return previous
}
