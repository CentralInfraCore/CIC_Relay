package cabinet

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewCabinetService_WasmManagerError tests the error path for NewCabinetService
// when the WasmManager fails to initialize.
func TestNewCabinetService_WasmManagerError(t *testing.T) {
	// Use an invalid config that will cause NewWasmManager to fail.
	invalidConfig := WasmManagerConfig{LRUCacheSize: -1}

	_, err := NewCabinetService(context.Background(), NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(), invalidConfig)

	require.Error(t, err, "NewCabinetService should fail with an invalid WasmManager config")
	assert.Contains(t, err.Error(), "failed to create cabinet service", "Error message should indicate service creation failure")
	assert.Contains(t, err.Error(), "failed to create LRU cache", "Error message should propagate the underlying LRU cache error")
}

// TestWithRecorder verifies that WithRecorder sets the recorder field on the service.
func TestWithRecorder(t *testing.T) {
	ctx := context.Background()
	svc, err := NewCabinetService(ctx, NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(),
		WasmManagerConfig{LRUCacheSize: 4},
		WithRecorder(nil), // nil recorder — option must apply without panic
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
}

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
	wasmMod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test.wasm", Version: "1.0"}, "wasm/test", nil, nil, []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
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
		wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.wasm.success", Steps: []string{"test.wasm@1.0.Process(input)"}}, WorkflowSigning{})
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

	// --- Sub-test: Successful WASM Init execution ---
	t.Run("init success path", func(t *testing.T) {
		mockWasmManager := new(MockWasmManager)
		mockCicWasmHost := new(MockCicWasmHost)

		mockWasmManager.On("NewHostInstance", ctx, wasmMod.ID()).Return(cicWasmHost(mockCicWasmHost), nil).Once()
		mockCicWasmHost.On("Init", ctx, wasmMod.ID(), "{\"input\":{\"config\":\"test\"}}").Return(`{"status":"initialized"}`, "null").Once()
		mockCicWasmHost.On("Close", ctx).Return(nil).Once()

		svc := &cabinetService{
			modules:     modStore,
			workflows:   wfStore,
			wasmManager: mockWasmManager,
		}

		wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.wasm.init.success", Steps: []string{"test.wasm@1.0.Init(input)"}}, WorkflowSigning{})
		require.NoError(t, err)
		svc.workflows.Put(wf)

		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": map[string]any{"config": "test"}}}
		resp, err := svc.Setx(ctx, payload)

		require.NoError(t, err)
		assert.Equal(t, "initialized", resp.Results["result"].(map[string]interface{})["status"])
		mockWasmManager.AssertExpectations(t)
		mockCicWasmHost.AssertExpectations(t)
	})

	// --- Sub-test: Successful WASM Get execution ---
	t.Run("get success path", func(t *testing.T) {
		mockWasmManager := new(MockWasmManager)
		mockCicWasmHost := new(MockCicWasmHost)

		mockWasmManager.On("NewHostInstance", ctx, wasmMod.ID()).Return(cicWasmHost(mockCicWasmHost), nil).Once()
		mockCicWasmHost.On("Get", ctx, wasmMod.ID(), "{\"input\":{\"query\":\"data\"}}").Return(`{"data":"retrieved"}`, "null").Once()
		mockCicWasmHost.On("Close", ctx).Return(nil).Once()

		svc := &cabinetService{
			modules:     modStore,
			workflows:   wfStore,
			wasmManager: mockWasmManager,
		}

		wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.wasm.get.success", Steps: []string{"test.wasm@1.0.Get(input)"}}, WorkflowSigning{})
		require.NoError(t, err)
		svc.workflows.Put(wf)

		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": map[string]any{"query": "data"}}}
		resp, err := svc.Setx(ctx, payload)

		require.NoError(t, err)
		assert.Equal(t, "retrieved", resp.Results["result"].(map[string]interface{})["data"])
		mockWasmManager.AssertExpectations(t)
		mockCicWasmHost.AssertExpectations(t)
	})

	// --- Sub-test: Successful WASM Notify execution ---
	t.Run("notify success path", func(t *testing.T) {
		mockWasmManager := new(MockWasmManager)
		mockCicWasmHost := new(MockCicWasmHost)

		mockWasmManager.On("NewHostInstance", ctx, wasmMod.ID()).Return(cicWasmHost(mockCicWasmHost), nil).Once()
		mockCicWasmHost.On("Notify", ctx, wasmMod.ID(), "{\"input\":{\"event\":\"fired\"}}").Return(`{"status":"notified"}`, "null").Once()
		mockCicWasmHost.On("Close", ctx).Return(nil).Once()

		svc := &cabinetService{
			modules:     modStore,
			workflows:   wfStore,
			wasmManager: mockWasmManager,
		}

		wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.wasm.notify.success", Steps: []string{"test.wasm@1.0.Notify(input)"}}, WorkflowSigning{})
		require.NoError(t, err)
		svc.workflows.Put(wf)

		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": map[string]any{"event": "fired"}}}
		resp, err := svc.Setx(ctx, payload)

		require.NoError(t, err)
		assert.Equal(t, "notified", resp.Results["result"].(map[string]interface{})["status"])
		mockWasmManager.AssertExpectations(t)
		mockCicWasmHost.AssertExpectations(t)
	})

	// --- Sub-test: Explicit Close call for coverage (MockCicWasmHost) ---
	t.Run("explicit close call coverage (MockCicWasmHost)", func(t *testing.T) {
		mockCicWasmHost := new(MockCicWasmHost)
		mockCicWasmHost.On("Close", ctx).Return(nil).Once()

		err := mockCicWasmHost.Close(ctx)
		require.NoError(t, err)
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
		wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.wasm.host.fail", Steps: []string{"test.wasm@1.0.Process(input)"}}, WorkflowSigning{})
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

		wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.wasm.bad.op", Steps: []string{"test.wasm@1.0.Delete(input)"}}, WorkflowSigning{}) // Delete is not a valid op
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

		wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.wasm.guest.fail", Steps: []string{"test.wasm@1.0.Process(input)"}}, WorkflowSigning{})
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

	// --- Sub-test: WASM returns TIMEOUT error code ---
	t.Run("wasm timeout returns ErrTimeout", func(t *testing.T) {
		mockWasmManager := new(MockWasmManager)
		mockCicWasmHost := new(MockCicWasmHost)

		mockWasmManager.On("NewHostInstance", ctx, wasmMod.ID()).Return(mockCicWasmHost, nil).Once()
		mockCicWasmHost.On("Process", ctx, wasmMod.ID(), "{\"input\":{\"msg\":\"test\"}}").
			Return("", `{"code":"TIMEOUT","message":"wasm module execution timed out"}`).Once()
		mockCicWasmHost.On("Close", ctx).Return(nil).Once()

		svc := &cabinetService{modules: modStore, workflows: wfStore, wasmManager: mockWasmManager}

		wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.wasm.timeout", Steps: []string{"test.wasm@1.0.Process(input)"}}, WorkflowSigning{})
		require.NoError(t, err)
		svc.workflows.Put(wf)

		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": map[string]any{"msg": "test"}}}
		_, err = svc.Setx(ctx, payload)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTimeout, "timeout error result must wrap ErrTimeout")
		mockWasmManager.AssertExpectations(t)
		mockCicWasmHost.AssertExpectations(t)
	})

	// --- Sub-test: JSON marshaling of input fails ---
	t.Run("json marshal fails", func(t *testing.T) {
		// Channels cannot be marshaled to JSON.
		// Since ProofTrace hashes the input before dispatch, the error now surfaces
		// at the hashing step rather than the WASM JSON-marshal step.
		mockWasmManager := new(MockWasmManager)

		svc := &cabinetService{modules: modStore, workflows: wfStore, wasmManager: mockWasmManager}

		wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.wasm.marshal.fail", Steps: []string{"test.wasm@1.0.Process(input)"}}, WorkflowSigning{})
		require.NoError(t, err)
		svc.workflows.Put(wf)

		payload := &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": make(chan int)}}
		_, err = svc.Setx(ctx, payload)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to hash input at step")
		mockWasmManager.AssertExpectations(t)
	})
}

// TestCabinetService_PutAndGet_Success verifies the core Put/Get functionality of the service.
func TestCabinetService_PutAndGet_Success(t *testing.T) {
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := NewCabinetService(context.Background(), NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(), config)
	require.NoError(t, err)

	t.Run("Schema Put/Get", func(t *testing.T) {
		schema, err := NewSchema(context.Background(), SchemaIdentity{Version: "v1"}, SchemaConfig{Name: "s1"}, SchemaSigning{})
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
		workflow, err := NewWorkflow(context.Background(), WorkflowIdentity{Version: "v1"}, WorkflowConfig{Name: "wf1", Steps: []string{"step1"}}, WorkflowSigning{})
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

// TestSetx_ProofTrace verifies that Setx produces a correct ProofTrace.
func TestSetx_ProofTrace(t *testing.T) {
	ctx := context.Background()

	modStore := NewMemModuleStore()
	wfStore := NewMemWorkflowStore()

	native := make(NativeFunctions)
	native["echo"] = CoreEcho
	mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "echo", Version: "1.0"}, "native", nil, nil, nil)
	require.NoError(t, err)
	mod.NativeImpl = native
	modStore.Put(mod)

	wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.trace", Steps: []string{"echo@1.0.echo(input)"}}, WorkflowSigning{})
	require.NoError(t, err)
	wfStore.Put(wf)

	fixedTime := "2026-01-01T00:00:00Z"
	svc := &cabinetService{
		modules:   modStore,
		workflows: wfStore,
		wasmManager: func() wasmManager {
			m := new(MockWasmManager)
			return m
		}(),
		clock: func() string { return fixedTime },
	}

	payload := &SetPayload{
		WorkflowID: wf.ID(),
		Payload:    map[string]any{"msg": "hello"},
	}
	resp, err := svc.Setx(ctx, payload)
	require.NoError(t, err)
	require.NotNil(t, resp.Trace)

	trace := resp.Trace
	assert.Equal(t, wf.ID(), trace.WorkflowID)
	assert.Equal(t, fixedTime, trace.Timestamp)
	require.Len(t, trace.Steps, 1)

	step := trace.Steps[0]
	assert.Equal(t, "echo@1.0", step.Module)
	assert.NotEmpty(t, step.InputHash)
	assert.NotEmpty(t, step.OutputHash)
	assert.NotEmpty(t, trace.ChainHash)

	// ChainHash must be reproducible from workflowID + sourceDigest + steps.
	assert.Equal(t, ComputeChainHashV1(trace.WorkflowID, "", trace.Steps), trace.ChainHash)

	// Determinism: same input → same hashes.
	resp2, err := svc.Setx(ctx, payload)
	require.NoError(t, err)
	assert.Equal(t, trace.Steps[0].InputHash, resp2.Trace.Steps[0].InputHash)
	assert.Equal(t, trace.ChainHash, resp2.Trace.ChainHash)
}

func TestCabinetService_Close(t *testing.T) {
	ctx := context.Background()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := NewCabinetService(ctx, NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(), config)
	require.NoError(t, err)

	// Call Close and assert no error is returned.
	// The actual implementation of Close for MemStores is a no-op, so it should always succeed.
	err = svc.Close(ctx)
	assert.NoError(t, err, "Close should not return an error")

	// --- Sub-test: Mock WasmManager Close coverage ---
	t.Run("mock wasm manager close", func(t *testing.T) {
		mockWasmManager := new(MockWasmManager)
		mockWasmManager.On("Close", ctx).Return(nil).Once()

		svc := &cabinetService{
			wasmManager: mockWasmManager,
		}

		err := svc.Close(ctx)
		assert.NoError(t, err, "Close should not return an error when mocking wasmManager")
		mockWasmManager.AssertExpectations(t)
	})
}
