package cabinet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/canonicaljson"
)

func TestNewModuleDescriptor_Error(t *testing.T) {
	t.Run("validation fails for empty name", func(t *testing.T) {
		// Create a BaseBlock with a missing Name to trigger the validation error
		meta := BaseBlock{Version: "1.0"}

		descriptor, err := NewModuleDescriptor(meta, "executor", nil, nil, nil)

		assert.Error(t, err, "NewModuleDescriptor should return an error for invalid metadata")
		assert.Nil(t, descriptor, "ModuleDescriptor should be nil on error")
		assert.ErrorIs(t, err, ErrInvalid, "Error should be of type ErrInvalid")
	})
}

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
	expectedJSON := `{"accepts":["schema-a"],"outputKeys":["key-a"],"type":"executor"}`
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
