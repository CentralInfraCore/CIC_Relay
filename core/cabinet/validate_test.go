package cabinet

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"centralrelay/pkg/canonicaljson"
	testify_assert "github.com/stretchr/testify/assert" // Explicit alias
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
	0x00,                                        // Section ID for custom sections
	0x0d,                                        // Section size (13 bytes)
	0x07,                                        // Name length (7 bytes)
	'p', 'r', 'o', 'd', 'u', 'c', 'e', 'r', 's', // Section name: "producers"
	0x01, // Number of producers
	0x01, // Producer name length
	'A',  // Producer name: "A"
}...)

func TestValidateAndChecksum(t *testing.T) {
	// Save and restore original function to prevent test pollution
	originalStripFunc := stripWasmModuleFunc
	t.Cleanup(func() {
		SetStripWasmModuleFunc(originalStripFunc)
		canonicaljson.SetToJSONMock(nil)
	})

	t.Run("missing name should fail", func(t *testing.T) {
		meta := &BaseBlock{Version: "1.0"}
		_, err := validateAndChecksum(context.Background(), meta, KindSpec, nil)
		testify_assert.Error(t, err)
		testify_assert.ErrorIs(t, err, ErrInvalid)
		testify_assert.Contains(t, err.Error(), "name and version are required")
	})

	t.Run("missing version should fail", func(t *testing.T) {
		meta := &BaseBlock{Name: "test"}
		_, err := validateAndChecksum(context.Background(), meta, KindSpec, nil)
		testify_assert.Error(t, err)
		testify_assert.ErrorIs(t, err, ErrInvalid)
		testify_assert.Contains(t, err.Error(), "name and version are required")
	})

	t.Run("checksum mismatch should fail and set valid=false", func(t *testing.T) {
		meta := &BaseBlock{Name: "test", Version: "1.0", Checksum: "bad-checksum"}
		_, err := validateAndChecksum(context.Background(), meta, KindSpec, "data")
		testify_assert.Error(t, err)
		testify_assert.ErrorIs(t, err, ErrInvalid)
		testify_assert.Contains(t, err.Error(), "checksum mismatch")
		testify_assert.False(t, meta.Valid, "meta.Valid should be false on checksum mismatch")
	})

	t.Run("correct existing checksum should pass and set valid=true", func(t *testing.T) {
		// Pre-calculated checksum for the given meta and data
		const correctChecksum = "45e1aee2cbd9775261601c55a041d9228f0cc6a764ffb7b9be218a719fec6276"
		meta := &BaseBlock{Name: "test", Version: "1.0", Checksum: correctChecksum}

		sum, err := validateAndChecksum(context.Background(), meta, KindSpec, "data")

		require.NoError(t, err)
		testify_assert.Equal(t, correctChecksum, sum)
		testify_assert.True(t, meta.Valid, "meta.Valid should be true on checksum match")
	})

	t.Run("no existing checksum should calculate and return a new one", func(t *testing.T) {
		meta := &BaseBlock{Name: "test", Version: "1.0"}
		const expectedChecksum = "45e1aee2cbd9775261601c55a041d9228f0cc6a764ffb7b9be218a719fec6276"

		sum, err := validateAndChecksum(context.Background(), meta, KindSpec, "data")

		require.NoError(t, err)
		testify_assert.Equal(t, expectedChecksum, sum)
		testify_assert.False(t, meta.Valid, "meta.Valid should not be set when checksum is new")
	})

	t.Run("error from computeChecksum should be propagated", func(t *testing.T) {
		t.Cleanup(func() {
			canonicaljson.SetToJSONMock(nil)
		})

		// Inject an error into the canonicalizer for the metadata part
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			return nil, errors.New("canonicalizer failed")
		})

		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := validateAndChecksum(context.Background(), meta, KindSpec, "data")

		testify_assert.Error(t, err)
		testify_assert.Contains(t, err.Error(), "canonicalizer failed")
	})

	t.Run("WASM module size exceeds limit should fail", func(t *testing.T) {
		meta := &BaseBlock{Name: "large-wasm", Version: "1.0"}
		// Create a byte slice larger than MaxWASMModuleSize
		largeWasmData := make([]byte, MaxWASMModuleSize+1)
		_, err := validateAndChecksum(context.Background(), meta, KindModule, largeWasmData)

		testify_assert.Error(t, err)
		testify_assert.ErrorIs(t, err, ErrInvalid)
		testify_assert.Contains(t, err.Error(), fmt.Sprintf("WASM module size exceeds limit of %d bytes", MaxWASMModuleSize))
	})

	t.Run("WASM exactly at size limit is allowed", func(t *testing.T) {
		orig := stripWasmModuleFunc
		t.Cleanup(func() { stripWasmModuleFunc = orig })
		// Mock to avoid decoding 16 MB of synthetic bytes as real WASM
		stripWasmModuleFunc = func(_ []byte) ([]byte, error) { return wasmBase, nil }

		meta := &BaseBlock{Name: "exact-limit-wasm", Version: "1.0"}
		data := make([]byte, MaxWASMModuleSize) // len == limit, not > limit
		_, err := validateAndChecksum(context.Background(), meta, KindModule, data)
		require.NoError(t, err)
	})

	t.Run("valid WASM module should pass", func(t *testing.T) {
		meta := &BaseBlock{Name: "small-wasm", Version: "1.0"}
		// Use a structurally valid, minimal WASM module.
		_, err := validateAndChecksum(context.Background(), meta, KindModule, wasmBase)
		testify_assert.NoError(t, err)
	})

	t.Run("WASM module canonicalization should affect checksum", func(t *testing.T) {
		meta := &BaseBlock{Name: "test-wasm", Version: "1.0"}

		// Calculate expected checksum based on the stripped data (which is wasmBase)
		expectedChecksum, err := computeChecksum(context.Background(), meta, KindModule, wasmBase)
		require.NoError(t, err)

		// Call validateAndChecksum with the non-canonical data (with custom section)
		actualChecksum, err := validateAndChecksum(context.Background(), meta, KindModule, wasmWithCustomSection)

		testify_assert.NoError(t, err)
		testify_assert.Equal(t, expectedChecksum, actualChecksum, "Checksum should be calculated based on stripped WASM data")
	})

	t.Run("WASM module canonicalization error should be propagated", func(t *testing.T) {
		originalFunc := stripWasmModuleFunc
		t.Cleanup(func() {
			SetStripWasmModuleFunc(originalFunc)
		})

		meta := &BaseBlock{Name: "test-wasm-error", Version: "1.0"}
		rawWasmData := []byte("some raw wasm data")

		// Temporarily override the stripWasmModuleFunc to return an error
		SetStripWasmModuleFunc(func(rawData []byte) ([]byte, error) {
			return nil, errors.New("wasm stripping failed")
		})

		_, err := validateAndChecksum(context.Background(), meta, KindModule, rawWasmData)

		testify_assert.Error(t, err)
		testify_assert.ErrorIs(t, err, ErrInvalid)
		testify_assert.Contains(t, err.Error(), "failed to canonicalize WASM module: wasm stripping failed")
	})

	t.Run("WASM module validation with cancelled context should fail", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		meta := &BaseBlock{Name: "test-wasm", Version: "1.0"}
		_, err := validateAndChecksum(ctx, meta, KindModule, wasmBase)
		require.Error(t, err)
		testify_assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestComputeChecksum_ErrorCases(t *testing.T) {
	// Restore original function after all tests in this file
	t.Cleanup(func() {
		canonicaljson.SetToJSONMock(nil)
	})

	t.Run("error on meta canonicalization should fail", func(t *testing.T) {
		t.Cleanup(func() {
			canonicaljson.SetToJSONMock(nil)
		})
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			// Fail only when trying to marshal the metaForChecksum struct
			if _, ok := v.(metaForChecksum); ok {
				return nil, errors.New("meta canonicalization failed")
			}
			return canonicaljson.ToJSONOriginal(v) // Call the original implementation
		})

		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := computeChecksum(context.Background(), meta, KindSpec, "data")

		testify_assert.Error(t, err)
		testify_assert.Contains(t, err.Error(), "failed to canonicalize meta")
	})

	t.Run("error on data canonicalization should fail", func(t *testing.T) {
		t.Cleanup(func() {
			canonicaljson.SetToJSONMock(nil)
		})
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			// Fail only for the data part (e.g., a string in this test)
			if _, ok := v.(string); ok {
				return nil, errors.New("data canonicalization failed")
			}
			return canonicaljson.ToJSONOriginal(v)
		})

		meta := &BaseBlock{Name: "test", Version: "1.o"}
		_, err := computeChecksum(context.Background(), meta, KindSpec, "some-data")

		testify_assert.Error(t, err)
		testify_assert.Contains(t, err.Error(), "failed to canonicalize data")
	})

	t.Run("unsupported type in data should fail", func(t *testing.T) {
		// No mock needed, let the original function fail on an unsupported type
		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := computeChecksum(context.Background(), meta, KindSpec, math.NaN())

		testify_assert.Error(t, err)
		testify_assert.Contains(t, err.Error(), "failed to canonicalize data")
	})

	t.Run("SetToJSONMock should set and reset mock", func(t *testing.T) {
		t.Cleanup(func() {
			canonicaljson.SetToJSONMock(nil)
		})

		// Set a mock that always returns an error
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			return nil, errors.New("mocked canonicalization error")
		})

		// Try to compute a checksum, which should now fail due to the mock
		meta := &BaseBlock{Name: "mock-test", Version: "1.0"}
		_, err := computeChecksum(context.Background(), meta, KindSpec, "dummy-data")

		testify_assert.Error(t, err)
		testify_assert.Contains(t, err.Error(), "mocked canonicalization error")

		// Reset the mock and verify that canonicalization works again
		canonicaljson.SetToJSONMock(nil)
		_, err = computeChecksum(context.Background(), meta, KindSpec, "dummy-data")
		testify_assert.NoError(t, err, "Canonicalization should work after resetting the mock")
	})
}

func TestStripWasmModuleFunc_Error(t *testing.T) {
	t.Run("invalid wasm data should fail decoding", func(t *testing.T) {
		// Using a truncated WASM module (magic number only) which should
		// cause a decoding error due to incomplete data.
		invalidWasmData := []byte{0x00, 0x61, 0x73, 0x6d} // "\0asm"
		_, err := stripWasmModuleFunc(invalidWasmData)
		require.Error(t, err)
		testify_assert.Contains(t, err.Error(), "failed to decode wasm module with wabin")
	})
}

func TestComputeChecksum_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	meta := &BaseBlock{Name: "test", Version: "1.0"}

	// This should cover the first ctx.Err() check in computeChecksum
	_, err := computeChecksum(ctx, meta, KindSpec, "some data")
	require.Error(t, err)
	testify_assert.ErrorIs(t, err, context.Canceled)

	// This should cover the ctx.Err() check in the []byte data path
	_, err = computeChecksum(ctx, meta, KindModule, []byte("some data"))
	require.Error(t, err)
	testify_assert.ErrorIs(t, err, context.Canceled)
}

func TestNewModuleDescriptor_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context immediately

	_, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test", Version: "1.0"}, "wasm/test", nil, nil, wasmBase)
	require.Error(t, err)
	testify_assert.ErrorIs(t, err, context.Canceled)
}

func TestNewWorkflow_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context immediately

	_, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "test-wf", Steps: []string{"step1"}}, WorkflowSigning{})
	require.Error(t, err)
	testify_assert.ErrorIs(t, err, context.Canceled)
}

func TestNewSchema_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context immediately

	_, err := NewSchema(ctx, SchemaIdentity{Version: "1.0"}, SchemaConfig{Name: "test-schema"}, SchemaSigning{})
	require.Error(t, err)
	testify_assert.ErrorIs(t, err, context.Canceled)
}
