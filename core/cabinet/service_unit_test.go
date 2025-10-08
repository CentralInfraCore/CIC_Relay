package cabinet

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutionContext verifies the functionality of the executionContext struct.
func TestExecutionContext(t *testing.T) {
	initialPayload := map[string]interface{}{"initial_key": "initial_value"}

	t.Run("NewExecutionContext", func(t *testing.T) {
		ec := NewExecutionContext(initialPayload)
		require.NotNil(t, ec, "NewExecutionContext should not return nil")
		require.NotNil(t, ec.variables, "variables map should be initialized")
		require.NotNil(t, ec.materializedModules, "materializedModules map should be initialized")

		// Check if the initial payload is stored correctly under the "input" key.
		inputData, ok := ec.Get("input")
		require.True(t, ok, "'input' key should exist in the context")
		assert.Equal(t, initialPayload, inputData, "'input' data should match the initial payload")
	})

	t.Run("Get and Set Variables", func(t *testing.T) {
		ec := NewExecutionContext(initialPayload)

		// Test setting a new value
		ec.Set("new_key", "new_value")
		val, ok := ec.Get("new_key")
		require.True(t, ok, "Should be able to get a newly set value")
		assert.Equal(t, "new_value", val, "The retrieved value should match the set value")

		// Test overwriting a value
		ec.Set("new_key", "overwritten_value")
		val, ok = ec.Get("new_key")
		require.True(t, ok, "Should be able to get an overwritten value")
		assert.Equal(t, "overwritten_value", val, "The retrieved value should match the overwritten value")

		// Test getting a non-existent value
		_, ok = ec.Get("non_existent_key")
		assert.False(t, ok, "Getting a non-existent key should return false")
	})

	t.Run("Get and Store Modules", func(t *testing.T) {
		ec := NewExecutionContext(initialPayload)

		// Create a mock module descriptor
		module, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: "test-mod", Version: "1.0"}, "test", nil, nil, nil)
		require.NoError(t, err)

		// Test storing a module
		ec.StoreModule(module)
		retrievedMod, ok := ec.GetModule("test-mod@1.0")
		require.True(t, ok, "Should be able to get a stored module")
		assert.Equal(t, module, retrievedMod, "The retrieved module should be the same as the stored one")

		// Test getting a non-existent module
		_, ok = ec.GetModule("non-existent-mod@1.0")
		assert.False(t, ok, "Getting a non-existent module should return false")
	})
}

// TestSetx_Wasm_Unit provides unit tests for the WASM execution path of Setx using mocks.
func TestSetx_Wasm_Unit(t *testing.T) {
	ctx := context.Background()

	// 1. Setup common test objects
	modStore := NewMemModuleStore()
	wfStore := NewMemWorkflowStore()

	// Create a WASM module descriptor
	wasmMod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test.wasm", Version: "1.0"}, "wasm/test", nil, nil, []byte{0x00, 0x61, 0x73, 0x6d})
	require.NoError(t, err)
	modStore.Put(wasmMod)

	// --- Sub-test: Successful WASM execution ---
	t.Run("success path", func(t *testing.T) {
		// a. Setup Mocks
		mockWasmManager := new(MockWasmManager)
		mockCicWasmHost := new(MockCicWasmHost)

		// b. Define Mock Expectations
		mockWasmManager.On("NewHostInstance", ctx, wasmMod.ID()).Return(cicWasmHost(mockCicWasmHost), nil).Once()
		mockCicWasmHost.On("Process", ctx, wasmMod.ID(), "{\"input\":{\"msg\":\"test\"}}").Return(`{"data":"success"}`, "null").Once()
		mockCicWasmHost.On("Close", ctx).Return(nil).Once()

		// c. Create service with the mock manager
		svc := &cabinetService{
			modules:     modStore,
			workflows:   wfStore,
			wasmManager: mockWasmManager,
		}

		// Create a workflow that uses the WASM module
		wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.wasm.success", Version: "1.0"}, []string{"test.wasm@1.0.Process(input)"})
		require.NoError(t, err)
		svc.workflows.Put(wf)

		// d. Execute Setx
		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": map[string]any{"msg": "test"}}}
		resp, err := svc.Setx(ctx, payload)

		// e. Verify results
		require.NoError(t, err)
		assert.Equal(t, "success", resp.Results["result"].(map[string]interface{})["data"])
		mockWasmManager.AssertExpectations(t)
		mockCicWasmHost.AssertExpectations(t)
	})

	// --- Sub-test: NewHostInstance fails ---
	t.Run("NewHostInstance failure", func(t *testing.T) {
		// a. Setup Mocks
		mockWasmManager := new(MockWasmManager)
		expectedErr := fmt.Errorf("wasm host error")

		// b. Define Mock Expectations
		mockWasmManager.On("NewHostInstance", ctx, wasmMod.ID()).Return(nil, expectedErr).Once()

		// c. Create service with the mock manager
		svc := &cabinetService{
			modules:     modStore,
			workflows:   wfStore,
			wasmManager: mockWasmManager,
		}

		// Create a workflow that uses the WASM module
		wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.wasm.host.fail", Version: "1.0"}, []string{"test.wasm@1.0.Process(input)"})
		require.NoError(t, err)
		svc.workflows.Put(wf)

		// d. Execute Setx
		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": "data"}}
		_, err = svc.Setx(ctx, payload)

		// e. Verify results
		require.Error(t, err)
		assert.Contains(t, err.Error(), expectedErr.Error())
		mockWasmManager.AssertExpectations(t)
	})

	// --- Sub-test: Unsupported WASM operation ---
	t.Run("unsupported wasm operation", func(t *testing.T) {
		mockWasmManager := new(MockWasmManager)
		mockCicWasmHost := new(MockCicWasmHost)

		mockWasmManager.On("NewHostInstance", ctx, wasmMod.ID()).Return(cicWasmHost(mockCicWasmHost), nil).Once()
		mockCicWasmHost.On("Close", ctx).Return(nil).Once()

		svc := &cabinetService{modules: modStore, workflows: wfStore, wasmManager: mockWasmManager}

		wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.wasm.bad.op", Version: "1.0"}, []string{"test.wasm@1.0.Delete(input)"}) // Delete is not a valid op
		require.NoError(t, err)
		svc.workflows.Put(wf)

		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": "data"}}
		_, err = svc.Setx(ctx, payload)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not support operation \"Delete\"")
		mockWasmManager.AssertExpectations(t)
	})

	// --- Sub-test: Wasm host returns an error ---
	t.Run("wasm host returns error", func(t *testing.T) {
		mockWasmManager := new(MockWasmManager)
		mockCicWasmHost := new(MockCicWasmHost)

		mockWasmManager.On("NewHostInstance", ctx, wasmMod.ID()).Return(cicWasmHost(mockCicWasmHost), nil).Once()
		mockCicWasmHost.On("Process", ctx, wasmMod.ID(), "{\"input\":{\"msg\":\"test\"}}").Return("null", `{"code":"GUEST_ERROR","message":"guest failed"}`).Once()
		mockCicWasmHost.On("Close", ctx).Return(nil).Once()

		svc := &cabinetService{modules: modStore, workflows: wfStore, wasmManager: mockWasmManager}

		wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.wasm.guest.fail", Version: "1.0"}, []string{"test.wasm@1.0.Process(input)"})
		require.NoError(t, err)
		svc.workflows.Put(wf)

		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": map[string]any{"msg": "test"}}}
		_, err = svc.Setx(ctx, payload)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "WASM function test.wasm@1.0.Process returned error")
		assert.Contains(t, err.Error(), "GUEST_ERROR")
		mockWasmManager.AssertExpectations(t)
		mockCicWasmHost.AssertExpectations(t)
	})

	// --- Sub-test: JSON marshaling of input fails ---
	t.Run("json marshal fails", func(t *testing.T) {
		mockWasmManager := new(MockWasmManager)
		mockCicWasmHost := new(MockCicWasmHost)

		mockWasmManager.On("NewHostInstance", ctx, wasmMod.ID()).Return(cicWasmHost(mockCicWasmHost), nil).Once()
		mockCicWasmHost.On("Close", ctx).Return(nil).Once()

		svc := &cabinetService{modules: modStore, workflows: wfStore, wasmManager: mockWasmManager}

		wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.wasm.marshal.fail", Version: "1.0"}, []string{"test.wasm@1.0.Process(input)"})
		require.NoError(t, err)
		svc.workflows.Put(wf)

		// Channels cannot be marshaled to JSON
		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": make(chan int)}}
		_, err = svc.Setx(ctx, payload)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to marshal input data")
		mockWasmManager.AssertExpectations(t)
	})
}

// TestCabinetService_PutAndGet_Success verifies the core Put/Get functionality of the service.
func TestCabinetService_PutAndGet_Success(t *testing.T) {
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := NewCabinetService(context.Background(), NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(), config)
	require.NoError(t, err)

	t.Run("Schema Put/Get", func(t *testing.T) {
		schema, err := NewSchema(context.Background(), BaseBlock{Name: "s1", Version: "v1"}, SchemaDef{})
		require.NoError(t, err, "NewSchema should succeed for valid input")

		err = svc.PutSchema(schema)
		assert.NoError(t, err, "PutSchema should succeed for a valid schema")

		retrieved, err := svc.GetSchema(schema.ID())
		require.NoError(t, err, "GetSchema should find the stored schema")
		assert.Equal(t, schema.ID(), retrieved.ID())
		assert.Equal(t, schema.Checksum(), retrieved.Checksum(), "Checksum of retrieved object should match")
	})

	t.Run("Module Put/Get", func(t *testing.T) {
		module, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: "m1", Version: "v1"}, "executor", nil, nil, nil)
		require.NoError(t, err, "NewModuleDescriptor should succeed for valid input")

		err = svc.PutModule(module)
		assert.NoError(t, err, "PutModule should succeed for a valid module")

		retrieved, err := svc.GetModule(module.ID())
		require.NoError(t, err, "GetModule should find the stored module")
		assert.Equal(t, module.ID(), retrieved.ID())
		assert.Equal(t, module.Checksum(), retrieved.Checksum(), "Checksum of retrieved object should match")
	})

	t.Run("Workflow Put/Get", func(t *testing.T) {
		workflow, err := NewWorkflow(context.Background(), BaseBlock{Name: "wf1", Version: "v1"}, []string{"step1"})
		require.NoError(t, err, "NewWorkflow should succeed for valid input")

		err = svc.PutWorkflow(workflow)
		assert.NoError(t, err, "PutWorkflow should succeed for a valid workflow")

		retrieved, err := svc.GetWorkflow(workflow.ID())
		require.NoError(t, err, "GetWorkflow should find the stored workflow")
		assert.Equal(t, workflow.ID(), retrieved.ID())
		assert.Equal(t, workflow.Checksum(), retrieved.Checksum(), "Checksum of retrieved object should match")
	})
}

func TestCabinetService_Get_NotFound(t *testing.T) {
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := NewCabinetService(context.Background(), NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(), config)
	require.NoError(t, err)

	_, err = svc.GetSchema("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound, "GetSchema should return ErrNotFound for nonexistent ID")

	_, err = svc.GetModule("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound, "GetModule should return ErrNotFound for nonexistent ID")

	_, err = svc.GetWorkflow("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound, "GetWorkflow should return ErrNotFound for nonexistent ID")
}

func TestCabinetService_Close(t *testing.T) {
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := NewCabinetService(context.Background(), NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(), config)
	require.NoError(t, err)

	// Call Close and assert no error is returned.
	// The actual implementation of Close for MemStores is a no-op, so it should always succeed.
	err = svc.Close(context.Background())
	assert.NoError(t, err, "Close should not return an error")
}
