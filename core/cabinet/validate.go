package cabinet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"centralrelay/pkg/canonicaljson"
)

// Kind constants for checksum discrimination.
const (
	KindSpec   = "spec|"
	KindWf     = "wf|"
	KindModule = "wasm|"
)

// MaxWASMModuleSize defines the maximum allowed size for a WASM module in bytes (16MB).
const MaxWASMModuleSize = 16 * 1024 * 1024

// stripWasmModuleFunc is a function pointer that can be overridden for testing.
// In production, this would point to a real WASM stripping utility.
var stripWasmModuleFunc = func(rawData []byte) ([]byte, error) {
	// Default no-op implementation. In a real scenario, this would perform the stripping.
	return rawData, nil
}

// SetStripWasmModuleFunc allows overriding the default WASM stripping function for testing.
func SetStripWasmModuleFunc(f func([]byte) ([]byte, error)) {
	stripWasmModuleFunc = f
}

// metaForChecksum is a helper struct to select only the non-volatile fields from BaseBlock for checksum calculation.
type metaForChecksum struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	CreatedBy   string `json:"createdBy,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
}

// computeChecksum is a private helper that calculates the canonical checksum.
// It now correctly handles raw byte slices (for WASM) vs other data types.
func computeChecksum(meta *BaseBlock, kind string, data any) (string, error) {
	metaForHash := metaForChecksum{
		Name:        meta.Name,
		Version:     meta.Version,
		Description: meta.Description,
		CreatedBy:   meta.CreatedBy,
		Timestamp:   meta.Timestamp,
	}

	metaBytes, err := canonicaljson.ToJSON(metaForHash)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize meta: %w", err)
	}

	var dataBytes []byte
	// Special handling for raw binary data (like WASM code).
	if rawData, ok := data.([]byte); ok {
		dataBytes = rawData
	} else {
		// For all other types, use canonical JSON serialization.
		dataBytes, err = canonicaljson.ToJSON(data)
		if err != nil {
			return "", fmt.Errorf("failed to canonicalize data: %w", err)
		}
	}

	kindBytes := []byte(kind)

	hasher := sha256.New()
	hasher.Write(kindBytes)
	hasher.Write(metaBytes)
	hasher.Write(dataBytes)
	hash := hasher.Sum(nil)

	return hex.EncodeToString(hash[:]), nil
}

// validateAndChecksum is the single central entry point for validation and checksum calculation.
// It implements the "check-then-calculate" logic.
func validateAndChecksum(meta *BaseBlock, kind string, data any) (string, error) {
	// 1. Validate basic invariants.
	if meta.Name == "" || meta.Version == "" {
		return "", fmt.Errorf("%w: name and version are required", ErrInvalid)
	}

	// Add WASM module size limit check and canonicalization
	if kind == KindModule {
		if rawData, ok := data.([]byte); ok {
			if len(rawData) > MaxWASMModuleSize {
				return "", fmt.Errorf("%w: WASM module size exceeds limit of %d bytes", ErrInvalid, MaxWASMModuleSize)
			}
			
			// Canonicalize the WASM module by stripping non-essential sections
			strippedData, err := stripWasmModuleFunc(rawData)
			if err != nil {
				return "", fmt.Errorf("%w: failed to canonicalize WASM module: %v", ErrInvalid, err)
			}
			data = strippedData // Use the stripped data for checksum calculation
		}
	}

	// 2. Calculate the expected checksum based on the current data.
	expectedSum, err := computeChecksum(meta, kind, data)
	if err != nil {
		return "", err
	}

	// 3. If a checksum is already present, verify it.
	if meta.Checksum != "" {
		if meta.Checksum != expectedSum {
			meta.Valid = false
			return "", fmt.Errorf("%w: checksum mismatch (existing: %s, calculated: %s)", ErrInvalid, meta.Checksum, expectedSum)
		}
		// If it matches, we're good. The object is valid.
		meta.Valid = true
		return meta.Checksum, nil
	}

	// 4. If no checksum is present, return the newly calculated one to be set by the constructor.
	return expectedSum, nil
}
