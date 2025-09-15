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

func TestModuleDescriptor_GetData_ReturnsCompositeStruct(t *testing.T) {
	// Test case with no WASM code
	m1 := &ModuleDescriptor{
		Type:       "executor",
		Accepts:    []string{"schema-a"},
		OutputKeys: []string{"key-a"},
		WasmCode:   nil, // Explicitly nil
	}

	data1 := m1.GetData()
	gotJSON1, err := canonicaljson.ToJSON(data1)
	require.NoError(t, err)
	expectedJSON1 := `{"accepts":["schema-a"],"outputKeys":["key-a"],"type":"executor"}`
	assert.JSONEq(t, expectedJSON1, string(gotJSON1), "GetData should return correct JSON for non-WASM modules")

	// Test case with WASM code
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d} // Minimal WASM header
	m2 := &ModuleDescriptor{
		Type:       "executor",
		Accepts:    []string{"schema-b"},
		OutputKeys: []string{"key-b"},
		WasmCode:   wasmBytes,
	}

	data2 := m2.GetData()
	gotJSON2, err := canonicaljson.ToJSON(data2)
	require.NoError(t, err)

	// json.Marshal encodes []byte to base64 string
	expectedJSON2 := `{"accepts":["schema-b"],"outputKeys":["key-b"],"type":"executor","wasmCode":"AGFzbQ=="}`
	assert.JSONEq(t, expectedJSON2, string(gotJSON2), "GetData should return correct JSON for WASM modules")

	// Verify the underlying types are correct
	compositeData, ok := data2.(struct {
		Type       string   `json:"type"`
		Accepts    []string `json:"accepts"`
		OutputKeys []string `json:"outputKeys"`
		WasmCode   []byte   `json:"wasmCode,omitempty"`
	})
	require.True(t, ok, "Returned data should be the composite struct")
	assert.Equal(t, wasmBytes, compositeData.WasmCode, "WasmCode in composite struct should match original")
}
