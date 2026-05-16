package cabinet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"

	"centralrelay/pkg/obs"
)

// loadWasmFile is a test helper to load a compiled WASM module from the testdata directory.
func loadWasmFile(t *testing.T, wasmFile string) []byte {
	t.Helper()
	path := filepath.Join("testdata", wasmFile)
	wasm, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read wasm file: %s", path)
	return wasm
}

// mockModuleStore is a mock implementation of the ModuleStore interface for testing.
type mockModuleStore struct {
	mu      sync.RWMutex
	modules map[string]*ModuleDescriptor
}

func newMockModuleStore() *mockModuleStore {
	return &mockModuleStore{
		modules: make(map[string]*ModuleDescriptor),
	}
}

func (s *mockModuleStore) Get(id string) (*ModuleDescriptor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.modules[id]
	return m, ok
}

func (s *mockModuleStore) Put(m *ModuleDescriptor) (prev *ModuleDescriptor, existed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := m.ID()
	prev, existed = s.modules[id]
	s.modules[id] = m
	return prev, existed
}

func (s *mockModuleStore) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.modules[id]
	return ok
}

// TestWasmManager_NewAndClose tests the basic creation and destruction of the WasmManager.
func TestWasmManager_NewAndClose(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()

	t.Run("successful creation and close", func(t *testing.T) {
		config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
		m, err := NewWasmManager(ctx, store, config)
		require.NoError(t, err)
		require.NotNil(t, m)

		// Closing the manager should not return an error.
		err = m.Close(ctx)
		assert.NoError(t, err)

		// Operations after closing should fail.
		_, err = m.NewHostInstance(ctx, "any-module")
		assert.Error(t, err, "expected error on closed manager")
	})

	t.Run("fail with canceled context", func(t *testing.T) {
		config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
		closedCtx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := NewWasmManager(closedCtx, store, config)
		assert.Error(t, err, "NewWasmManager should fail with a closed context")
	})

	t.Run("fail with invalid LRU size", func(t *testing.T) {
		config := WasmManagerConfig{LRUCacheSize: -1, DefaultTimeoutSeconds: 1}
		_, err := NewWasmManager(ctx, store, config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create LRU cache")
	})
}

// TestWasmManager_PreCompile_Simple tests a single, successful compilation and caching.
func TestWasmManager_PreCompile_Simple(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// Use the ABI-compatible module for this test.
	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	// 1. First call should compile the module.
	compiled, err := m.PreCompileModule(ctx, md.ID())
	require.NoError(t, err)
	require.NotNil(t, compiled)

	// 2. Check that the module is now in the cache.
	cacheKey := md.ID() + ":" + md.Checksum()
	_, ok := m.compiledModules.Get(cacheKey)
	assert.True(t, ok, "module should be in the cache after first compilation")

	// 3. Second call should return the same cached instance.
	compiled2, err := m.PreCompileModule(ctx, md.ID())
	require.NoError(t, err)
	assert.Same(t, compiled, compiled2, "second call should return the same compiled module instance")
}

// TestWasmManager_NewHostInstance tests creating a new host instance.
func TestWasmManager_NewHostInstance(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// Use the ABI-compatible module for this test.
	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	// 1. First, test with a module that doesn't exist.
	_, err = m.NewHostInstance(ctx, "non-existent-module")
	assert.Error(t, err, "expected error for non-existent module")

	// 2. Now, test with a module that exists.
	// Pre-compile it first.
	_, err = m.PreCompileModule(ctx, md.ID())
	require.NoError(t, err)

	// Now create an instance.
	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	require.NotNil(t, instance)

	// Close the instance
	err = instance.Close(ctx)
	assert.NoError(t, err)
}

// TestWasmManager_EvictModule tests evicting a module from the cache.
func TestWasmManager_EvictModule(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// Use the ABI-compatible module for this test.
	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	// 1. Compile the module to get it into the cache.
	_, err = m.PreCompileModule(ctx, md.ID())
	require.NoError(t, err)

	// 2. Check that the module is in the cache.
	cacheKey := md.ID() + ":" + md.Checksum()
	_, ok := m.compiledModules.Get(cacheKey)
	assert.True(t, ok, "module should be in the cache after compilation")

	// 3. Evict the module.
	m.EvictModule(ctx, md.ID())

	// 4. Check that the module is no longer in the cache.
	_, ok = m.compiledModules.Get(cacheKey)
	assert.False(t, ok, "module should not be in the cache after eviction")
}

// TestCicWasmHost_Process tests the Process method of a host instance.
func TestCicWasmHost_Process(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// Use the ABI-compatible module which returns a packed 0 (ptr=0, len=0).
	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	require.NotNil(t, instance)
	defer instance.Close(ctx)

	// Call Process. The abi_compatible.wasm module's Call function returns a packed 0.
	// The host should interpret this as null data and null error.
	authContext := "{}"
	input := "{}"
	dataJson, errorJson := instance.Process(ctx, authContext, input)

	assert.Equal(t, "null", dataJson)
	assert.Equal(t, "null", errorJson)
}

// TestCicWasmHost_Get tests the Get method of a host instance.
func TestCicWasmHost_Get(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// Use the ABI-compatible module.
	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	defer instance.Close(ctx)

	dataJson, errorJson := instance.Get(ctx, "{}", "{}")

	assert.Equal(t, "null", dataJson)
	assert.Equal(t, "null", errorJson)
}

// TestCicWasmHost_Init tests the Init method of a host instance.
func TestCicWasmHost_Init(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// Use the ABI-compatible module.
	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	defer instance.Close(ctx)

	dataJson, errorJson := instance.Init(ctx, "{}", "{}")

	assert.Equal(t, "null", dataJson)
	assert.Equal(t, "null", errorJson)
}

// TestCicWasmHost_Notify tests the Notify method of a host instance.
func TestCicWasmHost_Notify(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// Use the ABI-compatible module.
	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	defer instance.Close(ctx)

	dataJson, errorJson := instance.Notify(ctx, "{}", "{}")

	assert.Equal(t, "null", dataJson)
	assert.Equal(t, "null", errorJson)
}

// TestWasmManager_NewHostInstance_MissingABI tests creating an instance from a module that is missing a required function.
func TestWasmManager_NewHostInstance_MissingABI(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// This wasm module is intentionally missing the "deallocate" function.
	missingAbiWasm := loadWasmFile(t, "missing_abi.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "missing-abi-module", Version: "1.0"}, "wasm", nil, nil, missingAbiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	// This should fail because the wasm module is missing the "deallocate" function.
	_, err = m.NewHostInstance(ctx, md.ID())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not export required ABI functions")
}

// TestWasmManager_PreCompile_NilCode tests pre-compiling a module with no code.
func TestWasmManager_PreCompile_NilCode(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "nil-code-module", Version: "1.0"}, "wasm", nil, nil, nil)
	require.NoError(t, err)
	store.Put(md)
	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	_, err = m.PreCompileModule(ctx, md.ID())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no WASM code")
}

// TestCicWasmHost_Process_BadRead tests when the guest returns a pointer to invalid memory.
func TestCicWasmHost_Process_BadRead(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	badReadWasm := loadWasmFile(t, "bad_read.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "bad-read-module", Version: "1.0"}, "wasm", nil, nil, badReadWasm)
	require.NoError(t, err)
	store.Put(md)
	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)
	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	defer instance.Close(ctx)

	dataJson, errorJson := instance.Process(ctx, "{}", "{}")

	assert.Equal(t, "", dataJson)
	assert.Contains(t, errorJson, `"code":"HOST_ERROR"`)
	assert.Contains(t, errorJson, "failed to read result from wasm memory")
}

// TestCicWasmHost_Process_TrapDealloc tests that a trap in deallocate is handled.
func TestCicWasmHost_Process_TrapDealloc(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	trapDeallocWasm := loadWasmFile(t, "trap_dealloc.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "trap-dealloc-module", Version: "1.0"}, "wasm", nil, nil, trapDeallocWasm)
	require.NoError(t, err)
	store.Put(md)
	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)
	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	defer instance.Close(ctx)

	dataJson, errorJson := instance.Process(ctx, "{}", "{}")

	assert.Equal(t, "null", dataJson)
	assert.Equal(t, "null", errorJson)
}

// TestCicWasmHost_Process_Timeout tests the timeout functionality.
func TestCicWasmHost_Process_Timeout(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	// Set a very short timeout to ensure the wasm module will time out.
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	longRunningWasm := loadWasmFile(t, "long_running.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "long-running-module", Version: "1.0"}, "wasm", nil, nil, longRunningWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	defer instance.Close(ctx)

	dataJson, errorJson := instance.Process(ctx, "{}", "{}")

	assert.Equal(t, "", dataJson)
	assert.Contains(t, errorJson, `"code":"TIMEOUT"`)
}

// TestFormatErrorJson_WithDetails tests that the details field is correctly included.
func TestFormatErrorJson_WithDetails(t *testing.T) {
	details := map[string]interface{}{"extra": "info"}
	errorJson := formatErrorJson("TEST_CODE", "test message", details)
	assert.Contains(t, errorJson, `"details":{"extra":"info"}`)
}

// TestFormatErrorJson_MarshalError tests the error path of formatErrorJson.
func TestFormatErrorJson_MarshalError(t *testing.T) {
	// Channels cannot be marshaled to JSON, so this should trigger the internal error.
	unsupportedDetails := make(chan int)
	errorJson := formatErrorJson("TEST_CODE", "test message", unsupportedDetails)
	assert.Equal(t, `{"code":"HOST_INTERNAL", "message":"failed to marshal error object"}`, errorJson)
}

// TestCicWasmHost_Close_NilInstance tests closing a nil instance.
func TestCicWasmHost_Close_NilInstance(t *testing.T) {
	host := &CicWasmHost{}
	err := host.Close(context.Background())
	assert.NoError(t, err)
}

// TestWasmManager_EvictModule_Prefix tests that EvictModule evicts all versions of a module.
func TestWasmManager_EvictModule_Prefix(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// Use the ABI-compatible module for this test.
	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md1, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md1)

	md2, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module", Version: "2.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md2)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	// Compile both modules to get them into the cache.
	_, err = m.PreCompileModule(ctx, md1.ID())
	require.NoError(t, err)
	_, err = m.PreCompileModule(ctx, md2.ID())
	require.NoError(t, err)

	// Evict the module by its base name.
	m.EvictModule(ctx, "test-module")

	// Check that both versions are no longer in the cache.
	cacheKey1 := md1.ID() + ":" + md1.Checksum()
	_, ok1 := m.compiledModules.Get(cacheKey1)
	assert.False(t, ok1, "module version 1.0 should be evicted")

	cacheKey2 := md2.ID() + ":" + md2.Checksum()
	_, ok2 := m.compiledModules.Get(cacheKey2)
	assert.False(t, ok2, "module version 2.0 should be evicted")
}

// TestCicWasmHost_callGuest_Errors covers various error scenarios in the callGuest function.
func TestCicWasmHost_callGuest_Errors(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	// Note: The following tests require specific wasm files in the testdata directory.
	// - trap.wasm: A module whose "Call" function immediately traps.
	// - echo_json.wasm: A module whose "Call" function returns a hardcoded JSON string based on the op.

	t.Run("writeStringToWasm fails", func(t *testing.T) {
		abiWasm := loadWasmFile(t, "abi_compatible.wasm")
		md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "test-module-write-fail", Version: "1.0"}, "wasm", nil, nil, abiWasm)
		require.NoError(t, err)
		store.Put(md)

		m, err := NewWasmManager(ctx, store, config)
		require.NoError(t, err)
		defer m.Close(ctx)

		instance, err := m.NewHostInstance(ctx, md.ID())
		require.NoError(t, err)
		defer instance.Close(ctx)

		hostInstance, ok := instance.(*CicWasmHost)
		require.True(t, ok)

		// Use a canceled context to cause writeStringToWasm to fail.
		callCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, errorJson := hostInstance.callGuest(callCtx, "any_op", "{}", "{}")
		assert.Contains(t, errorJson, "HOST_ERROR")
		assert.Contains(t, errorJson, "failed to write 'op' to wasm memory")
	})

	t.Run("guest call traps", func(t *testing.T) {
		trapWasm := loadWasmFile(t, "trap.wasm")
		md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "trap-module", Version: "1.0"}, "wasm", nil, nil, trapWasm)
		require.NoError(t, err)
		store.Put(md)

		m, err := NewWasmManager(ctx, store, config)
		require.NoError(t, err)
		defer m.Close(ctx)

		instance, err := m.NewHostInstance(ctx, md.ID())
		require.NoError(t, err)
		defer instance.Close(ctx)

		hostInstance, ok := instance.(*CicWasmHost)
		require.True(t, ok)

		_, errorJson := hostInstance.callGuest(ctx, "any_op", "{}", "{}")
		assert.Contains(t, errorJson, `"code":"RUNTIME"`)
		assert.Contains(t, errorJson, "wasm module execution failed")
	})

	t.Run("guest returns invalid json", func(t *testing.T) {
		echoWasm := loadWasmFile(t, "echo_json.wasm")
		md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "echo-json-module", Version: "1.0"}, "wasm", nil, nil, echoWasm)
		require.NoError(t, err)
		store.Put(md)

		m, err := NewWasmManager(ctx, store, config)
		require.NoError(t, err)
		defer m.Close(ctx)

		instance, err := m.NewHostInstance(ctx, md.ID())
		require.NoError(t, err)
		defer instance.Close(ctx)

		hostInstance, ok := instance.(*CicWasmHost)
		require.True(t, ok)

		_, errorJson := hostInstance.callGuest(ctx, "get_invalid_json", "{}", "{}")

		assert.Contains(t, errorJson, `"code":"HOST_ERROR"`)
		assert.Contains(t, errorJson, "failed to unmarshal guest result JSON")
	})

	t.Run("guest returns null data", func(t *testing.T) {
		echoWasm := loadWasmFile(t, "echo_json.wasm")
		md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "echo-json-module-null-data", Version: "1.0"}, "wasm", nil, nil, echoWasm)
		require.NoError(t, err)
		store.Put(md)

		m, err := NewWasmManager(ctx, store, config)
		require.NoError(t, err)
		defer m.Close(ctx)

		instance, err := m.NewHostInstance(ctx, md.ID())
		require.NoError(t, err)
		defer instance.Close(ctx)

		hostInstance, ok := instance.(*CicWasmHost)
		require.True(t, ok)

		dataJson, errorJson := hostInstance.callGuest(ctx, "get_null_data", "{}", "{}")

		assert.Equal(t, "null", dataJson)
		assert.JSONEq(t, `{"code":"GUEST_REJECTED"}`, errorJson)
	})

	t.Run("guest returns null error", func(t *testing.T) {
		echoWasm := loadWasmFile(t, "echo_json.wasm")
		md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "echo-json-module-null-error", Version: "1.0"}, "wasm", nil, nil, echoWasm)
		require.NoError(t, err)
		store.Put(md)

		m, err := NewWasmManager(ctx, store, config)
		require.NoError(t, err)
		defer m.Close(ctx)

		instance, err := m.NewHostInstance(ctx, md.ID())
		require.NoError(t, err)
		defer instance.Close(ctx)

		hostInstance, ok := instance.(*CicWasmHost)
		require.True(t, ok)

		dataJson, errorJson := hostInstance.callGuest(ctx, "get_null_error", "{}", "{}")

		assert.Equal(t, "null", errorJson)
		assert.JSONEq(t, `{"status":"ok"}`, dataJson)
	})
}

// TestWasmManager_SetObsSinks_CompileMetric verifies that after SetObsSinks,
// compiling a module emits a relay.wasm.compile_duration_ms histogram point. (M22.8)
func TestWasmManager_SetObsSinks_CompileMetric(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "obs-test-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	sink := &obs.CaptureSink{}
	m.SetObsSinks(sink, sink)

	_, err = m.PreCompileModule(ctx, md.ID())
	require.NoError(t, err)

	require.Len(t, sink.Histos, 1)
	assert.Equal(t, "relay.wasm.compile_duration_ms", sink.Histos[0].Name)
	assert.Equal(t, int64(1), sink.Histos[0].Count)
	assert.Equal(t, "cicwasm", sink.Histos[0].Resource.Component)
	assert.Equal(t, md.ID(), sink.Histos[0].Attrs["module_id"])
}

// TestWasmManager_SetObsSinks_InstantiateMetric verifies that NewHostInstance emits
// a relay.wasm.instantiate_duration_ms histogram point after SetObsSinks. (M22.8)
func TestWasmManager_SetObsSinks_InstantiateMetric(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}

	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "obs-inst-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	sink := &obs.CaptureSink{}
	m.SetObsSinks(sink, sink)

	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	defer instance.Close(ctx)

	// compile + instantiate = 2 histograms
	require.Len(t, sink.Histos, 2)
	names := []string{sink.Histos[0].Name, sink.Histos[1].Name}
	assert.Contains(t, names, "relay.wasm.compile_duration_ms")
	assert.Contains(t, names, "relay.wasm.instantiate_duration_ms")
}

// TestCicWasmHost_Process_NoSpuriousDeallocWarn verifies that no WARN log is emitted
// when Process completes successfully (all four dealloc defers succeed).
// Kills the err != nil → err == nil mutation in each defer closure.
func TestCicWasmHost_Process_NoSpuriousDeallocWarn(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 5}

	abiWasm := loadWasmFile(t, "abi_compatible.wasm")
	md, err := NewModuleDescriptor(ctx, BaseBlock{Name: "no-warn-module", Version: "1.0"}, "wasm", nil, nil, abiWasm)
	require.NoError(t, err)
	store.Put(md)

	m, err := NewWasmManager(ctx, store, config)
	require.NoError(t, err)
	defer m.Close(ctx)

	sink := &obs.CaptureSink{}
	m.SetObsSinks(sink, sink)

	instance, err := m.NewHostInstance(ctx, md.ID())
	require.NoError(t, err)
	defer instance.Close(ctx)

	// Clear any logs from setup (compile/instantiate metrics are histos, not logs)
	sink.Logs = nil

	instance.Process(ctx, "{}", "{}")

	for _, log := range sink.Logs {
		if log.Level == "WARN" {
			t.Errorf("unexpected WARN log emitted during successful Process: message=%q", log.Message)
		}
	}
}

// TestCicWasmHost_EmitDeallocWarn_NilSafe verifies that emitDeallocWarn is a no-op
// when logSink is nil — no panic. (M22.8)
func TestCicWasmHost_EmitDeallocWarn_NilSafe(t *testing.T) {
	h := &CicWasmHost{logSink: nil}
	assert.NotPanics(t, func() {
		h.emitDeallocWarn("op", assert.AnError)
	})
}

// TestCicWasmHost_EmitDeallocWarn_RecordsLog verifies that emitDeallocWarn emits
// a WARN LogRecord when logSink is set. (M22.8)
func TestCicWasmHost_EmitDeallocWarn_RecordsLog(t *testing.T) {
	sink := &obs.CaptureSink{}
	h := &CicWasmHost{logSink: sink}
	h.emitDeallocWarn("result", assert.AnError)

	require.Len(t, sink.Logs, 1)
	assert.Equal(t, "WARN", sink.Logs[0].Level)
	assert.Equal(t, "wasm memory deallocation failed", sink.Logs[0].Message)
	assert.Equal(t, "cicwasm", sink.Logs[0].Resource.Component)
	assert.Equal(t, "result", sink.Logs[0].Attrs["ptr_type"])
}

func TestNewWasmManager_HostSetupFn_Error(t *testing.T) {
	ctx := context.Background()
	store := newMockModuleStore()
	config := WasmManagerConfig{
		LRUCacheSize:          10,
		DefaultTimeoutSeconds: 5,
		HostSetupFn: func(ctx context.Context, r wazero.Runtime) error {
			return fmt.Errorf("host setup intentionally failed")
		},
	}
	_, err := NewWasmManager(ctx, store, config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host module setup failed")
}
