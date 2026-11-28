package cabinet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"centralrelay/pkg/canonicaljson"
	"centralrelay/pkg/wasmcanon"
)

// Kind constants for checksum discrimination.
const (
	// KindSpec prefixes the checksum input of a schema.
	KindSpec = "spec|"
	// KindWf prefixes the checksum input of a workflow.
	KindWf = "wf|"
	// KindModule prefixes the checksum input of a module; validateAndChecksum also size-checks and
	// strips the custom sections of a raw []byte WASM payload of this kind before hashing it.
	KindModule = "wasm|"
)

// MaxWASMModuleSize defines the maximum allowed size for a WASM module in bytes (16MB).
const MaxWASMModuleSize = 16 * 1024 * 1024

// stripWasmModuleFunc is the wasm-canon/v1 transformation: pkg/wasmcanon, the single implementation the producer
// (the release tooling) uses as well (O-1 INT-03). It is the former inline function, moved unchanged.
var stripWasmModuleFunc = wasmcanon.Canonicalize

// The transformation is the executed-identity contract wasm-canon/v1 (#162 5a): the relay admits a module only if
// sha256 of its output equals the envelope's executed_wasm_sha256. Nothing in a production build can replace it: the
// only exported override, SetStripWasmModuleFunc, exists solely under the cic_test_seams build tag
// (strip_seam_testhook.go), and inside this package only tests assign stripWasmModuleFunc.

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

// validateAndChecksumSchema validates Schema-specific invariants and computes the canonical checksum.
func validateAndChecksumSchema(ctx context.Context, id SchemaIdentity, cfg SchemaConfig, sig SchemaSigning) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if cfg.Name == "" || id.Version == "" {
		return "", fmt.Errorf("%w: name and version are required", ErrInvalid)
	}

	metaForHash := metaForChecksum{
		Name:        cfg.Name,
		Version:     id.Version,
		Description: cfg.Description,
		CreatedBy:   cfg.CreatedBy,
		Timestamp:   cfg.Timestamp,
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	metaBytes, err := canonicaljson.ToJSON(metaForHash)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize meta: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	dataBytes, err := canonicaljson.ToJSON(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize data: %w", err)
	}

	hasher := sha256.New()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hasher.Write([]byte(KindSpec))
	hasher.Write(metaBytes)
	hasher.Write(dataBytes)
	expectedSum := hex.EncodeToString(hasher.Sum(nil))

	if sig.Checksum != "" {
		if sig.Checksum != expectedSum {
			return "", fmt.Errorf("%w: checksum mismatch (existing: %s, calculated: %s)", ErrInvalid, sig.Checksum, expectedSum)
		}
		return sig.Checksum, nil
	}
	return expectedSum, nil
}

// validateAndChecksumWorkflow validates Workflow-specific invariants and computes the canonical checksum.
func validateAndChecksumWorkflow(ctx context.Context, id WorkflowIdentity, cfg WorkflowConfig, sig WorkflowSigning) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if cfg.Name == "" || id.Version == "" {
		return "", fmt.Errorf("%w: name and version are required", ErrInvalid)
	}

	metaForHash := metaForChecksum{
		Name:        cfg.Name,
		Version:     id.Version,
		Description: cfg.Description,
		CreatedBy:   cfg.CreatedBy,
		Timestamp:   cfg.Timestamp,
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	metaBytes, err := canonicaljson.ToJSON(metaForHash)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize meta: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	dataBytes, err := canonicaljson.ToJSON(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize data: %w", err)
	}

	hasher := sha256.New()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hasher.Write([]byte(KindWf))
	hasher.Write(metaBytes)
	hasher.Write(dataBytes)
	expectedSum := hex.EncodeToString(hasher.Sum(nil))

	if sig.Checksum != "" {
		if sig.Checksum != expectedSum {
			return "", fmt.Errorf("%w: checksum mismatch (existing: %s, calculated: %s)", ErrInvalid, sig.Checksum, expectedSum)
		}
		return sig.Checksum, nil
	}
	return expectedSum, nil
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
