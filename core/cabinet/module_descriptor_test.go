package cabinet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/canonicaljson"
)

func TestModuleDescriptor_ID_Basic(t *testing.T) {
	m := &ModuleDescriptor{Meta: BaseBlock{Name: "mod-a", Version: "v1.0"}}
	assert.Equal(t, "mod-a@v1.0", m.ID())
}

func TestModuleDescriptor_ID_EmptyParts(t *testing.T) {
	assert.Equal(t, "@", (&ModuleDescriptor{}).ID(), "üres meta -> @")
	assert.Equal(t, "a@", (&ModuleDescriptor{Meta: BaseBlock{Name: "a"}}).ID(), "üres version -> a@")
	assert.Equal(t, "@v1", (&ModuleDescriptor{Meta: BaseBlock{Version: "v1"}}).ID(), "üres név -> @v1")
}

func TestModuleDescriptor_GetData_ReturnsSpecForNonWasm(t *testing.T) {
	m := &ModuleDescriptor{
		Type:       "executor",
		Accepts:    []string{"schema-a"},
		OutputKeys: []string{"key-a"},
		WasmCode:   nil, // Explicitly nil
	}

	data := m.GetData()

	// Convert the returned data to canonical JSON and compare
	gotJSON, err := canonicaljson.ToJSON(data)
	require.NoError(t, err)

	// The expected JSON should match the structure returned by GetData()
	// Note: JSON keys are lowercase due to `json:"type"` tags in GetData()'s anonymous struct
	expectedJSON := `{"type":"executor","accepts":["schema-a"],"outputKeys":["key-a"]}`
	assert.JSONEq(t, expectedJSON, string(gotJSON), "GetData should return the correct JSON representation for non-WASM modules")
}

func TestModuleDescriptor_GetData_ReturnsWasmCodeWhenPresent(t *testing.T) {
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d} // Minimal WASM header
	m := &ModuleDescriptor{
		Type:     "executor",
		WasmCode: wasmBytes,
	}

	data := m.GetData()

	// Use type assertion to check the type
	retrievedCode, ok := data.([]byte)
	assert.True(t, ok, "GetData should return a byte slice for WASM modules")
	assert.Equal(t, wasmBytes, retrievedCode, "The returned byte slice should be the WasmCode")
}

func TestModuleDescriptor_Validate_WasmModule(t *testing.T) {
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00} // A simple, valid WASM module

	// First, create a module with an empty checksum and let Validate() calculate it.
	mValid := &ModuleDescriptor{
		Meta: BaseBlock{
			Name:    "wasm-mod",
			Version: "1.0",
		},
		WasmCode: wasmBytes,
	}

	err := mValid.Validate()
	require.NoError(t, err, "First validation should succeed and calculate the checksum")
	require.NotEmpty(t, mValid.Meta.Checksum, "Checksum should be populated after first validation")

	t.Run("successful validation with correct checksum", func(t *testing.T) {
		// Now that the checksum is populated, validating again should succeed.
		err := mValid.Validate()
		require.NoError(t, err)
		assert.True(t, mValid.Meta.Valid, "Meta.Valid should be true after successful validation")
	})

	t.Run("validation fails with incorrect checksum", func(t *testing.T) {
		mIncorrect := &ModuleDescriptor{
			Meta: BaseBlock{
				Name:     "wasm-mod",
				Version:  "1.0",
				Checksum: "incorrect-checksum",
			},
			WasmCode: wasmBytes,
		}

		err := mIncorrect.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum mismatch", "Error message should indicate a checksum mismatch")
		assert.False(t, mIncorrect.Meta.Valid, "Meta.Valid should be false after failed validation")
	})
}

func TestModuleDescriptor_Validate_NonWasmModule_CalculatesChecksum(t *testing.T) {
	// For non-WASM modules, Validate should calculate the checksum on the first run.
	m := &ModuleDescriptor{
		Meta: BaseBlock{
			Name:    "spec-mod",
			Version: "1.0",
			// Checksum is intentionally empty
		},
		Type:    "validator",
		Accepts: []string{"schema-b"},
	}

	// First validation should pass and calculate the checksum.
	err := m.Validate()
	require.NoError(t, err)
	assert.True(t, m.Meta.Valid, "Meta.Valid should be true after first validation")
	assert.NotEmpty(t, m.Meta.Checksum, "Checksum should be populated after first validation")

	// Second validation should also pass, now verifying the existing checksum.
	err = m.Validate()
	require.NoError(t, err)
	assert.True(t, m.Meta.Valid, "Meta.Valid should remain true after second validation")
}
