package main

import (
	"github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
)

// checkWasmImportsAgainstManifest closes the gap between what the release manifest DECLARES and what the executed
// wasm ACTUALLY imports (review F3). The V6 decision and the capability grant used to rest on the declared imports
// alone; a module could declare none and still import a cic-flow or git host function. The imports are read from the
// bytes that will be compiled (the executed wasm). Every cic-flow or git function the wasm imports must be declared
// in the manifest, else IMPORT_UNDECLARED. It returns whether the wasm actually imports a cic-flow function, so the
// caller applies V6 to the union of the declared and the actual imports. A declared but unused import is harmless
// (the runtime capability check still binds only authenticated modules) and is not rejected.
func checkWasmImportsAgainstManifest(executedWasm, manifest []byte) (bool, error) {
	mod, err := binary.DecodeModule(executedWasm, wasm.CoreFeaturesV2)
	if err != nil {
		return false, reject(codeModuleRejected, "the executed wasm cannot be decoded to read its imports: %v", err)
	}
	project, perr := parseModuleProject(manifest)
	if perr != nil {
		return false, reject(codeEnvelopeInvalid, "%v", perr)
	}
	declared := map[string]map[string]bool{"cic-flow": project.cicFlowImports(), "git": project.gitImports()}
	actualCICFlow := false
	for _, imp := range mod.ImportSection {
		fns, watched := declared[imp.Module]
		if !watched {
			continue
		}
		if imp.Module == "cic-flow" {
			actualCICFlow = true
		}
		if !fns[imp.Name] {
			return actualCICFlow, reject(codeImportUndeclared, "the wasm imports %s.%s but the manifest does not declare it", clipForError(imp.Module), clipForError(imp.Name))
		}
	}
	return actualCICFlow, nil
}
