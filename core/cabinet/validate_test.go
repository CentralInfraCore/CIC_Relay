package cabinet

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"centralrelay/pkg/canonicaljson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wasmBase is the binary representation of a simple WASM module that exports a single function `add`.
// wat: (module (func (export "add") (param i32 i32) (result i32) local.get 0 local.get 1 i32.add))
var wasmBase = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00, 0x07, 0x07, 0x01, 0x03, 0x61, 0x64, 0x64, 0x00, 0x00, 0x0a, 0x09, 0x01, 0x07,
	0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b,
}

// wasmWithCustomSection is the same module but with an added, non-essential custom section.
var wasmWithCustomSection = append(wasmBase, []byte{
	0x00, // Section ID for custom sections
	0x0d, // Section size (13 bytes)
	0x07, // Name length (7 bytes)
	'p', 'r', 'o', 'd', 'u', 'c', 'e', 'r', 's', // Section name: "producers"
	0x01, // Number of producers
	0x01, // Producer name length
	'A',  // Producer name: "A"
}...,)

func TestValidateAndChecksum(t *testing.T) {
	// Restore original function after all tests in this file
	defer func() {
		canonicaljson.SetToJSONMock(nil)
		// Reset the stripWasmModuleFunc to its default no-op behavior
		SetStripWasmModuleFunc(func(rawData []byte) ([]byte, error) {
			return rawData, nil
		})
	}()

	t.Run("missing name should fail", func(t *testing.T) {
		meta := &BaseBlock{Version: "1.0"}
		_, err := validateAndChecksum(meta, KindSpec, nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "name and version are required")
	})

	t.Run("missing version should fail", func(t *testing.T) {
		meta := &BaseBlock{Name: "test"}
		_, err := validateAndChecksum(meta, KindSpec, nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "name and version are required")
	})

	t.Run("checksum mismatch should fail and set valid=false", func(t *testing.T) {
		meta := &BaseBlock{Name: "test", Version: "1.0", Checksum: "bad-checksum"}
		_, err := validateAndChecksum(meta, KindSpec, "data")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "checksum mismatch")
		assert.False(t, meta.Valid, "meta.Valid should be false on checksum mismatch")
	})

	t.Run("correct existing checksum should pass and set valid=true", func(t *testing.T) {
		// Pre-calculated checksum for the given meta and data
		const correctChecksum = "45e1aee2cbd9775261601c55a041d9228f0cc6a764ffb7b9be218a719fec6276"
		meta := &BaseBlock{Name: "test", Version: "1.0", Checksum: correctChecksum}

		sum, err := validateAndChecksum(meta, KindSpec, "data")

		require.NoError(t, err)
		assert.Equal(t, correctChecksum, sum)
		assert.True(t, meta.Valid, "meta.Valid should be true on checksum match")
	})

	t.Run("no existing checksum should calculate and return a new one", func(t *testing.T) {
		meta := &BaseBlock{Name: "test", Version: "1.0"}
		const expectedChecksum = "45e1aee2cbd9775261601c55a041d9228f0cc6a764ffb7b9be218a719fec6276"

		sum, err := validateAndChecksum(meta, KindSpec, "data")

		require.NoError(t, err)
		assert.Equal(t, expectedChecksum, sum)
		assert.False(t, meta.Valid, "meta.Valid should not be set when checksum is new")
	})

	t.Run("error from computeChecksum should be propagated", func(t *testing.T) {
		defer canonicaljson.SetToJSONMock(nil) // Reset mock after this subtest

		// Inject an error into the canonicalizer for the metadata part
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			return nil, errors.New("canonicalizer failed")
		})

		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := validateAndChecksum(meta, KindSpec, "data")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "canonicalizer failed")
	})

	t.Run("WASM module size exceeds limit should fail", func(t *testing.T) {
		meta := &BaseBlock{Name: "large-wasm", Version: "1.0"}
		// Create a byte slice larger than MaxWASMModuleSize
		largeWasmData := make([]byte, MaxWASMModuleSize+1)
		_, err := validateAndChecksum(meta, KindModule, largeWasmData)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), fmt.Sprintf("WASM module size exceeds limit of %d bytes", MaxWASMModuleSize))
	})

	t.Run("valid WASM module should pass", func(t *testing.T) {
		meta := &BaseBlock{Name: "small-wasm", Version: "1.0"}
		// Use a structurally valid, minimal WASM module.
		_, err := validateAndChecksum(meta, KindModule, wasmBase)
		assert.NoError(t, err)
	})

	t.Run("WASM module canonicalization should affect checksum", func(t *testing.T) {
		meta := &BaseBlock{Name: "test-wasm", Version: "1.0"}

		// Calculate expected checksum based on the stripped data (which is wasmBase)
		expectedChecksum, err := computeChecksum(meta, KindModule, wasmBase)
		require.NoError(t, err)

		// Call validateAndChecksum with the non-canonical data (with custom section)
		actualChecksum, err := validateAndChecksum(meta, KindModule, wasmWithCustomSection)

		assert.NoError(t, err)
		assert.Equal(t, expectedChecksum, actualChecksum, "Checksum should be calculated based on stripped WASM data")
	})

	t.Run("WASM module canonicalization error should be propagated", func(t *testing.T) {
		meta := &BaseBlock{Name: "test-wasm-error", Version: "1.0"}
		rawWasmData := []byte("some raw wasm data")

		// Temporarily override the stripWasmModuleFunc to return an error
		SetStripWasmModuleFunc(func(rawData []byte) ([]byte, error) {
			return nil, errors.New("wasm stripping failed")
		})

		_, err := validateAndChecksum(meta, KindModule, rawWasmData)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "failed to canonicalize WASM module: wasm stripping failed")
	})
}

func TestComputeChecksum_ErrorCases(t *testing.T) {
	// Restore original function after all tests in this file
	defer func() {
		canonicaljson.SetToJSONMock(nil)
	}()

	t.Run("error on meta canonicalization should fail", func(t *testing.T) {
		defer canonicaljson.SetToJSONMock(nil) // Reset mock after this subtest
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			// Fail only when trying to marshal the metaForChecksum struct
			if _, ok := v.(metaForChecksum); ok {
				return nil, errors.New("meta canonicalization failed")
			}
			return canonicaljson.ToJSONOriginal(v) // Call the original implementation
		})

		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := computeChecksum(meta, KindSpec, "data")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to canonicalize meta")
	})

	t.Run("error on data canonicalization should fail", func(t *testing.T) {
		defer canonicaljson.SetToJSONMock(nil) // Reset mock after this subtest
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			// Fail only for the data part (e.g., a string in this test)
			if _, ok := v.(string); ok {
				return nil, errors.New("data canonicalization failed")
			}
			return canonicaljson.ToJSONOriginal(v)
		})

		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := computeChecksum(meta, KindSpec, "some-data")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to canonicalize data")
	})

	t.Run("unsupported type in data should fail", func(t *testing.T) {
		// No mock needed, let the original function fail on an unsupported type
		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := computeChecksum(meta, KindSpec, math.NaN())

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to canonicalize data")
	})
}
