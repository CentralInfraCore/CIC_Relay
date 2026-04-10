package cabinet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"golang.org/x/sync/singleflight"

	"centralrelay/pkg/obs"
)

// --- Configurable Parameters ---

// WasmManagerConfig defines the configurable parameters for the WasmManager.
type WasmManagerConfig struct {
	DefaultTimeoutSeconds uint32 `yaml:"default_timeout_seconds"`
	DefaultMemoryPages    uint32 `yaml:"default_memory_pages"`
	LRUCacheSize          int    `yaml:"lru_cache_size"`
}

// --- WasmManager: The Caching Compiler and Instance Factory ---

// WasmManager is responsible for the entire WASM lifecycle management.
type WasmManager struct {
	runtime         wazero.Runtime
	modules         ModuleStore
	config          WasmManagerConfig
	compiledModules *lru.Cache[string, wazero.CompiledModule]
	sf              singleflight.Group
	closed          atomic.Bool
	logSink         obs.LogSink
	metricSink      obs.MetricSink
}

// SetObsSinks wires the observability sinks into the WasmManager.
// Both sinks are nil-safe: passing nil disables the corresponding emissions.
func (m *WasmManager) SetObsSinks(logSink obs.LogSink, metricSink obs.MetricSink) {
	m.logSink = logSink
	m.metricSink = metricSink
}

// NewWasmManager creates a new central WASM manager with the given configuration.
func NewWasmManager(ctx context.Context, modules ModuleStore, config WasmManagerConfig) (*WasmManager, error) {
	// Fail fast if the context is already canceled.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cannot create WasmManager with a canceled context: %w", err)
	}

	runtimeConfig := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	r := wazero.NewRuntimeWithConfig(ctx, runtimeConfig)

	// Instantiate the WASI module, which is required for WASM modules compiled from Go.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		r.Close(context.Background()) // Use background context for cleanup on failure.
		return nil, fmt.Errorf("failed to instantiate wasi: %w", err)
	}

	// Allocate the manager first so the eviction closure can capture it.
	m := &WasmManager{
		runtime: r,
		modules: modules,
		config:  config,
	}

	onEvict := func(key string, mod wazero.CompiledModule) {
		// When a module is evicted from the cache, ensure its native resources are freed.
		// Using context.Background() as this happens asynchronously.
		if err := mod.Close(context.Background()); err != nil {
			if m.logSink != nil {
				m.logSink.RecordLog(obs.LogRecord{
					Timestamp: time.Now().UTC().Format(time.RFC3339),
					Level:     "WARN",
					Message:   "wasm module eviction close failed",
					Resource:  obs.Resource{Component: "cicwasm"},
					Attrs:     map[string]string{"module_key": key, "error": err.Error()},
				})
			}
		}
	}

	lruCache, err := lru.NewWithEvict[string, wazero.CompiledModule](config.LRUCacheSize, onEvict)
	if err != nil {
		r.Close(context.Background()) // Use background context for cleanup on failure.
		return nil, fmt.Errorf("failed to create LRU cache: %w", err)
	}

	m.compiledModules = lruCache
	return m, nil
}

// PreCompileModule compiles a module and stores it in the cache.
func (m *WasmManager) PreCompileModule(ctx context.Context, moduleID string) (wazero.CompiledModule, error) {
	if m.closed.Load() {
		return nil, errors.New("WasmManager is closed")
	}

	moduleDesc, ok := m.modules.Get(moduleID)
	if !ok {
		return nil, fmt.Errorf("module '%s' not found", moduleID)
	}

	// Use the module's pre-computed checksum for content-addressable caching.
	cacheKey := moduleID + ":" + moduleDesc.Checksum()

	if mod, ok := m.compiledModules.Get(cacheKey); ok {
		return mod, nil
	}

	res, err, _ := m.sf.Do(cacheKey, func() (interface{}, error) {
		if mod, ok := m.compiledModules.Get(cacheKey); ok {
			return mod, nil
		}

		if moduleDesc.WasmCode == nil {
			return nil, fmt.Errorf("module '%s' has no WASM code", moduleID)
		}

		start := time.Now()
		compiled, err := m.runtime.CompileModule(ctx, moduleDesc.WasmCode)
		if err != nil {
			return nil, fmt.Errorf("failed to compile module '%s': %w", moduleID, err)
		}
		if m.metricSink != nil {
			m.metricSink.RecordHistogram(obs.HistogramPoint{
				Name:      "relay.wasm.compile_duration_ms",
				Sum:       float64(time.Since(start).Milliseconds()),
				Count:     1,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Resource:  obs.Resource{Component: "cicwasm"},
				Attrs:     map[string]string{"module_id": moduleID},
			})
		}

		m.compiledModules.Add(cacheKey, compiled)
		return compiled, nil
	})

	if err != nil {
		return nil, err
	}

	return res.(wazero.CompiledModule), nil
}

// NewHostInstance creates a new, live instance of a module, applying configured limits.
func (m *WasmManager) NewHostInstance(ctx context.Context, moduleID string) (cicWasmHost, error) {
	if m.closed.Load() {
		return nil, errors.New("WasmManager is closed")
	}

	compiledMod, err := m.PreCompileModule(ctx, moduleID)
	if err != nil {
		return nil, err
	}

	// Don't call the _start function, as these are libraries, not applications.
	moduleConfig := wazero.NewModuleConfig().WithName(moduleID).WithStartFunctions()

	startInstantiation := time.Now()
	instance, err := m.runtime.InstantiateModule(ctx, compiledMod, moduleConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate module '%s': %w", moduleID, err)
	}
	if m.metricSink != nil {
		m.metricSink.RecordHistogram(obs.HistogramPoint{
			Name:      "relay.wasm.instantiate_duration_ms",
			Sum:       float64(time.Since(startInstantiation).Milliseconds()),
			Count:     1,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Resource:  obs.Resource{Component: "cicwasm"},
			Attrs:     map[string]string{"module_id": moduleID},
		})
	}

	timeout := time.Duration(m.config.DefaultTimeoutSeconds) * time.Second
	h, err := newCicWasmHost(ctx, instance, timeout, m.logSink)
	if err != nil {
		instance.Close(ctx)
		return nil, err
	}
	return h, nil
}

// EvictModule removes all versions of a compiled module from the cache.
func (m *WasmManager) EvictModule(ctx context.Context, moduleID string) {
	if m.closed.Load() {
		return
	}

	keysToRemove := []string{}
	for _, key := range m.compiledModules.Keys() {
		if strings.HasPrefix(key, moduleID) {
			keysToRemove = append(keysToRemove, key)
		}
	}

	for _, key := range keysToRemove {
		m.compiledModules.Remove(key)
	}
}

// Close shuts down the entire WASM runtime and clears all caches.
func (m *WasmManager) Close(ctx context.Context) error {
	m.closed.Store(true)
	// Purging will call the onEvict callback for all items, closing them.
	m.compiledModules.Purge()
	return m.runtime.Close(ctx)
}

// --- CicWasmHost: A Handle to a Single Live WASM Instance ---

type CicWasmHost struct {
	instance     api.Module
	callFn       api.Function
	allocateFn   api.Function
	deallocateFn api.Function
	timeout      time.Duration
	logSink      obs.LogSink
}

func newCicWasmHost(ctx context.Context, instance api.Module, timeout time.Duration, logSink obs.LogSink) (*CicWasmHost, error) {
	callFn := instance.ExportedFunction("Call")
	allocateFn := instance.ExportedFunction("allocate")
	deallocateFn := instance.ExportedFunction("deallocate")
	if callFn == nil || allocateFn == nil || deallocateFn == nil {
		return nil, fmt.Errorf("module '%s' does not export required ABI functions", instance.Name())
	}

	return &CicWasmHost{
		instance:     instance,
		callFn:       callFn,
		allocateFn:   allocateFn,
		deallocateFn: deallocateFn,
		timeout:      timeout,
		logSink:      logSink,
	}, nil
}

func (h *CicWasmHost) Close(ctx context.Context) error {
	if h.instance != nil {
		return h.instance.Close(ctx)
	}
	return nil
}

func (h *CicWasmHost) Process(ctx context.Context, authContextJson, inputJson string) (string, string) {
	return h.callGuest(ctx, "process", authContextJson, inputJson)
}

func (h *CicWasmHost) Get(ctx context.Context, authContextJson, queryJson string) (string, string) {
	return h.callGuest(ctx, "get", authContextJson, queryJson)
}

func (h *CicWasmHost) Init(ctx context.Context, authContextJson, configJson string) (string, string) {
	return h.callGuest(ctx, "init", authContextJson, configJson)
}

func (h *CicWasmHost) Notify(ctx context.Context, authContextJson, eventJson string) (string, string) {
	return h.callGuest(ctx, "notify", authContextJson, eventJson)
}

func (h *CicWasmHost) callGuest(ctx context.Context, op, authContextJson, inputJson string) (string, string) {
	callCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	opPtr, opLen, err := h.writeStringToWasm(callCtx, op)
	if err != nil {
		return "", formatErrorJson("HOST_ERROR", fmt.Sprintf("failed to write 'op' to wasm memory: %v", err), nil)
	}
	defer func() {
		if _, err := h.deallocateFn.Call(callCtx, uint64(opPtr), uint64(opLen)); err != nil {
			h.emitDeallocWarn("op", err)
		}
	}()

	authPtr, authLen, err := h.writeStringToWasm(callCtx, authContextJson)
	if err != nil {
		return "", formatErrorJson("HOST_ERROR", fmt.Sprintf("failed to write 'auth_context' to wasm memory: %v", err), nil)
	}
	defer func() {
		if _, err := h.deallocateFn.Call(callCtx, uint64(authPtr), uint64(authLen)); err != nil {
			h.emitDeallocWarn("auth", err)
		}
	}()

	dataPtr, dataLen, err := h.writeStringToWasm(callCtx, inputJson)
	if err != nil {
		return "", formatErrorJson("HOST_ERROR", fmt.Sprintf("failed to write 'input_json' to wasm memory: %v", err), nil)
	}
	defer func() {
		if _, err := h.deallocateFn.Call(callCtx, uint64(dataPtr), uint64(dataLen)); err != nil {
			h.emitDeallocWarn("input", err)
		}
	}()

	results, err := h.callFn.Call(callCtx, uint64(opPtr), uint64(opLen), uint64(authPtr), uint64(authLen), uint64(dataPtr), uint64(dataLen))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", formatErrorJson("TIMEOUT", "wasm module execution timed out", nil)
		}
		return "", formatErrorJson("RUNTIME", fmt.Sprintf("wasm module execution failed: %v", err), nil)
	}

	// The guest packs the result as (size << 32) | pointer.
	packedResult := results[0]
	resultLen := uint32(packedResult >> 32)
	resultPtr := uint32(packedResult)

	defer func() {
		if _, err := h.deallocateFn.Call(callCtx, uint64(resultPtr), uint64(resultLen)); err != nil {
			h.emitDeallocWarn("result", err)
		}
	}()

	// Handle the case where the guest returns a packed 0 (ptr=0, len=0), which means null/empty.
	if packedResult == 0 {
		return "null", "null"
	}

	resultBytes, ok := h.instance.Memory().Read(resultPtr, resultLen)
	if !ok {
		return "", formatErrorJson("HOST_ERROR", fmt.Sprintf("failed to read result from wasm memory at ptr=%d, len=%d", resultPtr, resultLen), nil)
	}

	type GuestResult struct {
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}

	var guestResult GuestResult
	if err := json.Unmarshal(resultBytes, &guestResult); err != nil {
		return "", formatErrorJson("HOST_ERROR", fmt.Sprintf("failed to unmarshal guest result JSON: %v", err), nil)
	}

	dataJsonResult := guestResult.Data
	if dataJsonResult == nil {
		dataJsonResult = []byte("null")
	}

	errorJsonResult := guestResult.Error
	if errorJsonResult == nil {
		errorJsonResult = []byte("null")
	}

	return string(dataJsonResult), string(errorJsonResult)
}

func (h *CicWasmHost) writeStringToWasm(ctx context.Context, s string) (uint32, uint32, error) {
	data := []byte(s)
	dataLen := uint32(len(data))

	results, err := h.allocateFn.Call(ctx, uint64(dataLen))
	if err != nil {
		return 0, 0, err
	}
	ptr64 := results[0]
	ptr := uint32(ptr64)

	if !h.instance.Memory().Write(ptr, data) {
		return 0, 0, fmt.Errorf("Memory.Write failed for length %d", dataLen)
	}

	return ptr, dataLen, nil
}

// emitDeallocWarn emits a WARN log record when a WASM memory deallocation fails.
// It is a no-op if logSink is nil.
func (h *CicWasmHost) emitDeallocWarn(ptrType string, err error) {
	if h.logSink == nil {
		return
	}
	h.logSink.RecordLog(obs.LogRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "WARN",
		Message:   "wasm memory deallocation failed",
		Resource:  obs.Resource{Component: "cicwasm"},
		Attrs:     map[string]string{"ptr_type": ptrType, "error": err.Error()},
	})
}

func formatErrorJson(code, message string, details interface{}) string {
	errorObj := map[string]interface{}{
		"code":    code,
		"message": message,
	}
	if details != nil {
		errorObj["details"] = details
	}

	bytes, err := json.Marshal(errorObj)
	if err != nil {
		return `{"code":"HOST_INTERNAL", "message":"failed to marshal error object"}`
	}
	return string(bytes)
}
