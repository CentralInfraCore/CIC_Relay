package cabinet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"centralrelay/pkg/canonicaljson"
	"github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
)

// Kind constants for checksum discrimination.
const (
	KindSpec   = "spec|"
	KindWf     = "wf|"
	KindModule = "wasm|"
)

// MaxWASMModuleSize defines the maximum allowed size for a WASM module in bytes (16MB).
const MaxWASMModuleSize = 16 * 1024 * 1024

// stripWasmModuleFunc uses wabin to strip all custom sections from a WASM module,
// producing a canonical byte representation.
var stripWasmModuleFunc = func(rawData []byte) ([]byte, error) {
	// 1. Decode the module using wabin's decoder.
	// This also performs validation.
	module, err := binary.DecodeModule(rawData, wasm.CoreFeaturesV2)
	if err != nil {
		return nil, fmt.Errorf("failed to decode wasm module with wabin: %w", err)
	}

	// 2. Strip all custom sections by setting the field to nil.
	// This includes potentially non-deterministic sections like 'name' and 'producers'.
	module.CustomSections = nil

	// 3. Re-encode the module without the custom sections.
	encodedBytes := binary.EncodeModule(module)

	return encodedBytes, nil
}

// SetStripWasmModuleFunc allows overriding the default WASM stripping function for testing purposes.
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
// It now accepts a context to handle timeouts during long-running operations.
func computeChecksum(ctx context.Context, meta *BaseBlock, kind string, data any) (string, error) {
	metaForHash := metaForChecksum{
		Name:        meta.Name,
		Version:     meta.Version,
		Description: meta.Description,
		CreatedBy:   meta.CreatedBy,
		Timestamp:   meta.Timestamp,
	}

	// Check for context cancellation before serialization
	if err := ctx.Err(); err != nil {
		return "", err
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
		// Check for context cancellation before serialization
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// For all other types, use canonical JSON serialization.
		dataBytes, err = canonicaljson.ToJSON(data)
		if err != nil {
			return "", fmt.Errorf("failed to canonicalize data: %w", err)
		}
	}

	kindBytes := []byte(kind)

	hasher := sha256.New()

	// Check for context cancellation before writing to the hasher
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hasher.Write(kindBytes)
	hasher.Write(metaBytes)
	hasher.Write(dataBytes)
	hash := hasher.Sum(nil)

	return hex.EncodeToString(hash[:]), nil
}

// validateAndChecksum is the single central entry point for validation and checksum calculation.
// It now accepts a context to handle timeouts.
func validateAndChecksum(ctx context.Context, meta *BaseBlock, kind string, data any) (string, error) {
	// 1. Check for context cancellation at the beginning.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// 2. Validate basic invariants.
	if meta.Name == "" || meta.Version == "" {
		return "", fmt.Errorf("%w: name and version are required", ErrInvalid)
	}

	// 3. Add WASM module size limit check and canonicalization
	if kind == KindModule {
		if rawData, ok := data.([]byte); ok {
			if len(rawData) > MaxWASMModuleSize {
				return "", fmt.Errorf("%w: WASM module size exceeds limit of %d bytes", ErrInvalid, MaxWASMModuleSize)
			}

			// Check for context cancellation before stripping WASM
			if err := ctx.Err(); err != nil {
				return "", err
			}
			// Canonicalize the WASM module by stripping non-essential sections
			strippedData, err := stripWasmModuleFunc(rawData)
			if err != nil {
				return "", fmt.Errorf("%w: failed to canonicalize WASM module: %v", ErrInvalid, err)
			}
			data = strippedData // Use the stripped data for checksum calculation
		}
	}

	// 4. Calculate the expected checksum based on the current data.
	expectedSum, err := computeChecksum(ctx, meta, kind, data)
	if err != nil {
		return "", err
	}

	// 5. If a checksum is already present, verify it.
	if meta.Checksum != "" {
		if meta.Checksum != expectedSum {
			meta.Valid = false
			return "", fmt.Errorf("%w: checksum mismatch (existing: %s, calculated: %s)", ErrInvalid, meta.Checksum, expectedSum)
		}
		// If it matches, we're good. The object is valid.
		meta.Valid = true
		return meta.Checksum, nil
	}

	// 6. If no checksum is present, return the newly calculated one to be set by the constructor.
	return expectedSum, nil
}
