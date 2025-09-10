package cabinet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"centralrelay/pkg/canonicaljson"
)

// metaForChecksum is a helper struct to select only the non-volatile fields from BaseBlock for checksum calculation.
type metaForChecksum struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	CreatedBy   string `json:"createdBy"`
	Timestamp   string `json:"timestamp"`
}

// validateMeta is the internal implementation of the validation logic.
func validateMeta(meta *BaseBlock, data any) error {
	if meta.Name == "" || meta.Version == "" {
		return fmt.Errorf("%w: name and version are required", ErrInvalid)
	}

	// If checksum is present, verify it.
	if meta.Checksum != "" {
		currentSum, err := genericChecksum(meta, data)
		if err != nil {
			return fmt.Errorf("failed to calculate checksum for validation: %w", err)
		}
		if currentSum != meta.Checksum {
			meta.Valid = false
			return fmt.Errorf("%w: checksum mismatch (got: %s, want: %s)", ErrInvalid, currentSum, meta.Checksum)
		}
		meta.Valid = true
		return nil
	}

	// If checksum is not present, calculate and set it.
	newSum, err := genericChecksum(meta, data)
	if err != nil {
		return fmt.Errorf("failed to calculate initial checksum: %w", err)
	}
	meta.Checksum = newSum
	meta.Valid = true
	return nil
}

// validateMetaFunc is a function variable that points to the validation implementation.
// This acts as a "test seam" allowing tests to mock the validation logic.
var validateMetaFunc = validateMeta

// genericChecksum calculates the SHA256 checksum for a given component using canonical JSON.
func genericChecksum(meta *BaseBlock, data any) (string, error) {
	// Use a dedicated struct to select only the fields relevant for the checksum.
	metaForHash := metaForChecksum{
		Name:        meta.Name,
		Version:     meta.Version,
		Description: meta.Description,
		CreatedBy:   meta.CreatedBy,
		Timestamp:   meta.Timestamp,
	}

	// Canonicalize and hash.
	metaBytes, err := canonicaljson.ToJSON(metaForHash)
	if err != nil {
		return "", err
	}
	dataBytes, err := canonicaljson.ToJSON(data)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(append(metaBytes, dataBytes...))
	return hex.EncodeToString(hash[:]), nil
}
