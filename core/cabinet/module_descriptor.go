package cabinet

import (
	"context"
	"fmt"
)

// NewModuleDescriptor constructs a ModuleDescriptor by calling the single central entry point for validation and checksum calculation.
//
// WASM canonicalization contract: custom sections are stripped from wasmCode *before*
// building the composite data struct used for checksum. This ensures the checksum is
// over the canonical (deterministic) form of the module, regardless of non-functional
// metadata embedded by compilers. The stripped bytes are also stored in the descriptor
// so that stored and executed bytes always match the checksummed form.
func NewModuleDescriptor(ctx context.Context, meta BaseBlock, moduleType string, accepts []string, outputKeys []string, wasmCode []byte) (*ModuleDescriptor, error) {
	// Canonicalize WASM bytes before checksum — strip non-deterministic custom sections.
	canonicalWasm := wasmCode
	if len(wasmCode) > 0 {
		stripped, err := stripWasmModuleFunc(wasmCode)
		if err != nil {
			return nil, fmt.Errorf("%w: wasm canonicalization failed: %v", ErrInvalid, err)
		}
		canonicalWasm = stripped
	}

	// Build composite data from canonical bytes for checksum calculation.
	tempDescriptorForData := &ModuleDescriptor{
		Type:       moduleType,
		Accepts:    accepts,
		OutputKeys: outputKeys,
		WasmCode:   canonicalWasm,
	}
	data := tempDescriptorForData.GetData()

	// validateAndChecksum receives a composite struct (not raw []byte), so the
	// KindModule WASM-stripping branch in validateAndChecksum is intentionally
	// bypassed here — stripping was already performed above.
	sum, err := validateAndChecksum(ctx, &meta, KindModule, data)
	if err != nil {
		return nil, err
	}
	meta.Checksum = sum
	meta.Valid = true

	return &ModuleDescriptor{
		Meta:       meta,
		Type:       moduleType,
		Accepts:    accepts,
		OutputKeys: outputKeys,
		WasmCode:   canonicalWasm, // store canonical form
	}, nil
}

// ID returns the unique module identifier in the form "name@version".
// This is used for lookups in the module store.
func (m *ModuleDescriptor) ID() string {
	return m.Meta.Name + "@" + m.Meta.Version
}

// GetData returns the data part of the module descriptor for checksum calculation.
// It now always returns a composite structure including policy fields and optional WASM code.
func (m *ModuleDescriptor) GetData() any {
	acceptsCopy := make([]string, len(m.Accepts))
	copy(acceptsCopy, m.Accepts)

	outputKeysCopy := make([]string, len(m.OutputKeys))
	copy(outputKeysCopy, m.OutputKeys)

	// Create a composite struct including all relevant fields for hashing.
	// wasmCode is included only if present, using omitempty.
	return struct {
		Type       string   `json:"type"`
		Accepts    []string `json:"accepts"`
		OutputKeys []string `json:"outputKeys"`
		WasmCode   []byte   `json:"wasmCode,omitempty"`
	}{
		Type:       m.Type,
		Accepts:    acceptsCopy,
		OutputKeys: outputKeysCopy,
		WasmCode:   m.WasmCode,
	}
}

// Checksum returns the pre-computed checksum from the Meta block.
func (m *ModuleDescriptor) Checksum() string {
	return m.Meta.Checksum
}
