package cabinet

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetx_WasmExecution tests the WASM execution path with a simple no-op module using the iSDK.
func TestSetx_WasmExecution(t *testing.T) {
	// 1. Read the ABI-compatible WASM module from testdata.
	wasmBytes, err := os.ReadFile(filepath.Join("testdata", "abi_compatible.wasm"))
	require.NoError(t, err, "failed to read abi_compatible.wasm")

	// 2. Setup Cabinet service with WasmManager configured.
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := NewCabinetService(context.Background(), NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(), config)
	require.NoError(t, err)
	ctx := context.Background()

	workflowID := "wf.wasm.test.isdk@1.0"

	// 3. Register the WASM module with its binary code.
	mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "wasm.noop.isdk", Version: "1.0"}, "wasm/test", nil, nil, wasmBytes)
	require.NoError(t, err)
	require.NoError(t, svc.PutModule(mod))

	// 4. Register a workflow that calls the "Process" operation on the WASM module.
	wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.wasm.test.isdk", Version: "1.0"}, []string{"wasm.noop.isdk@1.0.Process(input)"})
	require.NoError(t, err)
	require.NoError(t, svc.PutWorkflow(wf))

	// 5. Prepare the payload and call Setx.
	payload := &SetPayload{
		WorkflowID: workflowID,
		Payload:    map[string]any{"message": "hello wasm"},
	}

	resp, err := svc.Setx(ctx, payload)

	// 6. Verify the results.
	require.NoError(t, err, "Setx should execute the simple WASM call without error")
	require.NotNil(t, resp, "Response should not be nil")

	// The abi_compatible.wasm module's Call function returns a packed 0 (ptr=0, len=0).
	// The CicWasmHost should interpret this as `null` data and `null` error.
	// The service then places this `nil` value into the results map.
	result, ok := resp.Results["result"]
	assert.True(t, ok, "The 'result' key should exist in the response")
	assert.Nil(t, result, "The result of a no-op WASM call should be nil")
}
