package cabinet

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCanonicalWasmGolden proves that the WASM stripping logic correctly canonicalizes
// functionally identical modules with different binary representations.
func TestCanonicalWasmGolden(t *testing.T) {
	// This is the binary representation of a simple WASM module that exports a single function `add`.
	// wat: (module (func (export "add") (param i32 i32) (result i32) local.get 0 local.get 1 i32.add))
	wasmBase := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
		0x03, 0x02, 0x01, 0x00, 0x07, 0x07, 0x01, 0x03, 0x61, 0x64, 0x64, 0x00, 0x00, 0x0a, 0x09, 0x01, 0x07,
		0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b,
	}

	// This is the same module but with an added, non-essential custom section.
	// Custom sections are used for metadata (e.g., producer info) and should be stripped.
	customSection := []byte{
		0x00,                                        // Section ID for custom sections
		0x0d,                                        // Section size (13 bytes)
		0x07,                                        // Name length (7 bytes)
		'p', 'r', 'o', 'd', 'u', 'c', 'e', 'r', 's', // Section name: "producers"
		0x01, // Number of producers
		0x01, // Producer name length
		'A',  // Producer name: "A",
	}
	wasmWithCustomSection := append(wasmBase, customSection...)

	// 1. Verify that the two modules are NOT identical before stripping.
	assert.NotEqual(t, wasmBase, wasmWithCustomSection, "The original WASM binaries should be different")

	// 2. Apply the stripping function to both modules.
	strippedBase, err := stripWasmModuleFunc(wasmBase)
	require.NoError(t, err, "Stripping the base module should not produce an error")

	strippedWithCustom, err := stripWasmModuleFunc(wasmWithCustomSection)
	require.NoError(t, err, "Stripping the module with a custom section should not produce an error")

	// 3. Verify that the stripped versions ARE identical.
	// This is the core of the golden test: different inputs produce the same canonical output.
	assert.Equal(t, strippedBase, strippedWithCustom, "The canonical (stripped) representation of the modules should be identical")

	// 4. As a final check, ensure the base module is unchanged by the stripping process (as it has no custom sections).
	assert.Equal(t, wasmBase, strippedBase, "Stripping a module with no custom sections should result in an identical binary")
}

// TestHashingTimeout verifies that the checksum calculation respects the context timeout.
func TestHashingTimeout(t *testing.T) {
	// 1. Create a context that is already cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for the timeout to surely expire.
	time.Sleep(2 * time.Nanosecond)

	// 2. Prepare dummy data for a schema.
	identity := SchemaIdentity{Version: "1.0"}
	config := SchemaConfig{Name: "timeout.test"}

	// 3. Call a constructor function with the cancelled context.
	_, err := NewSchema(ctx, identity, config, SchemaSigning{})

	// 4. Verify that the error is due to the context deadline being exceeded.
	require.Error(t, err, "Expected an error due to timeout")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "The error should be context.DeadlineExceeded")
}
