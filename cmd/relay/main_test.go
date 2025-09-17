package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"centralrelay/core/cabinet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock Cabinet Service for testing error propagation ---
// This mock implements the full cabinet.Cabinet interface.
type mockCabinetService struct {
	SetFunc func(ctx context.Context, p *cabinet.SetPayload) (*cabinet.SetResponse, error)
}

func (m *mockCabinetService) Set(ctx context.Context, p *cabinet.SetPayload) (*cabinet.SetResponse, error) {
	if m.SetFunc != nil {
		return m.SetFunc(ctx, p)
	}
	return &cabinet.SetResponse{}, nil // Default success
}

// GetWorkflow simulates finding a workflow to allow ValidateSet to pass.
func (m *mockCabinetService) GetWorkflow(id string) (*cabinet.Workflow, error) {
	return &cabinet.Workflow{}, nil
}

// GetModule simulates finding a module to allow ValidateSet to pass.
func (m *mockCabinetService) GetModule(id string) (*cabinet.ModuleDescriptor, error) {
	return &cabinet.ModuleDescriptor{}, nil
}

// Other interface methods are stubbed out.
func (m *mockCabinetService) PutSchema(s *cabinet.Schema) error { return nil }
func (m *mockCabinetService) GetSchema(id string) (*cabinet.Schema, error) {
	return &cabinet.Schema{}, nil
}
func (m *mockCabinetService) PutModule(md *cabinet.ModuleDescriptor) error { return nil }
func (m *mockCabinetService) PutWorkflow(w *cabinet.Workflow) error        { return nil }

// Close implements the Close method of the cabinet.Cabinet interface.
func (m *mockCabinetService) Close(ctx context.Context) error {
	return nil // No-op for mock
}

// failingReader is a mock reader that always returns an error.
type failingReader struct{}

func (fr *failingReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("forced read error")
}

func TestRun_UsesGivenPort_AndServesRoot(t *testing.T) {
	// stub ListenAndServe: ne nyisson portot, csak ellenőrizzük a paramétereket
	called := false
	var gotAddr string
	var gotHandler http.Handler

	defer func(prev func(addr string, handler http.Handler) error) { listenAndServe = prev }(listenAndServe)

	listenAndServe = func(addr string, handler http.Handler) error {
		called = true
		gotAddr = addr
		gotHandler = handler
		// ne blokkoljon: azonnal térjen vissza sikerrel
		return nil
	}

	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)

	// Create a dummy relayServer for the test
	dummyAPI := cabinet.NewAPI(&mockCabinetService{}) // Minimal API for test
	// Initialize a default config for the test server
	defaultConfig, err := loadConfig()
	require.NoError(t, err)
	dummyServer := &relayServer{api: dummyAPI, config: defaultConfig}

	err = run(":12345", logger, dummyServer)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !called {
		t.Fatalf("listenAndServe was not called")
	}
	if gotAddr != ":12345" {
		t.Fatalf("listenAndServe addr = %q, want %q", gotAddr, ":12345")
	}

	// Ellenőrizzük, hogy a handler valóban kiszolgálja a "/"'-t a várt JSON-nal
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	gotHandler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	body := strings.TrimSuffix(w.Body.String(), "\n") // Fprintln miatt \n a végén
	var m map[string]string
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("response not JSON: %v; body=%q", err, body)
	}
	if m["status"] != "ok" || m["message"] != "central relay responding" {
		t.Fatalf("unexpected JSON: %+v", m)
	}

	// Log sor ellenőrzése
	if !strings.Contains(logBuf.String(), "Central relay API started on :12345") {
		t.Fatalf("startup log missing/incorrect: %q", logBuf.String())
	}
}

func TestRun_PropagatesListenError(t *testing.T) {
	defer func(prev func(addr string, handler http.Handler) error) { listenAndServe = prev }(listenAndServe)

	wantErr := errors.New("bind failed")
	listenAndServe = func(addr string, handler http.Handler) error {
		return wantErr
	}

	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)

	// Create a dummy relayServer for the test
	dummyAPI := cabinet.NewAPI(&mockCabinetService{}) // Minimal API for test
	// Initialize a default config for the test server
	defaultConfig, err := loadConfig()
	require.NoError(t, err)
	dummyServer := &relayServer{api: dummyAPI, config: defaultConfig}

	err = run(":0", logger, dummyServer)
	if !errors.Is(err, wantErr) {
		t.Fatalf("run err = %v, want %v", err, wantErr)
	}
}

// --- New tests for setHandler ---

func TestSetHandler(t *testing.T) {
	// 1. Setup
	// Create a real cabinet service and load the necessary components for the tests.
	schemaStore := cabinet.NewMemSchemaStore()
	workflowStore := cabinet.NewMemWorkflowStore()
	moduleStore := cabinet.NewMemModuleStore()
	cabinetSvc := cabinet.NewCabinetService(schemaStore, moduleStore, workflowStore)
	// We need to load the components used in the test payload
	pingSchema, err := cabinet.NewSchema(context.Background(), cabinet.BaseBlock{Name: "core.ping", Version: "1.0.0"}, cabinet.SchemaDef{})
	require.NoError(t, err)
	pingModule, err := cabinet.NewModuleDescriptor(context.Background(), cabinet.BaseBlock{Name: "core.ping", Version: "1.0.0"}, "executor", []string{pingSchema.ID()}, nil, nil)
	require.NoError(t, err)
	pingWorkflow, err := cabinet.NewWorkflow(context.Background(), cabinet.BaseBlock{Name: "core.ping.flow", Version: "1.0.0"}, []string{pingSchema.ID()})
	require.NoError(t, err)

	require.NoError(t, cabinetSvc.PutSchema(pingSchema))
	require.NoError(t, cabinetSvc.PutWorkflow(pingWorkflow))
	require.NoError(t, cabinetSvc.PutModule(pingModule))

	api := cabinet.NewAPI(cabinetSvc)
	// Initialize a default config for the test server
	defaultConfig, err := loadConfig()
	require.NoError(t, err)
	server := &relayServer{api: api, config: defaultConfig}

	t.Run("successful set", func(t *testing.T) {
		// Corrected payload with modules as a simple array of strings
		payload := `{
			"workflow_id": "core.ping.flow@1.0.0",
			"modules": ["core.ping@1.0.0"]
		}`

		req := httptest.NewRequest(http.MethodPost, "/set", strings.NewReader(payload))
		w := httptest.NewRecorder()

		server.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusOK, res.StatusCode)

		var respBody cabinet.SetResponse
		require.NoError(t, json.NewDecoder(res.Body).Decode(&respBody))
		assert.NotNil(t, respBody.Results)
	})

	t.Run("invalid json", func(t *testing.T) {
		payload := `{"workflow_id": "test"` // Malformed JSON

		req := httptest.NewRequest(http.MethodPost, "/set", strings.NewReader(payload))
		w := httptest.NewRecorder()

		server.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		assert.Contains(t, string(body), "Invalid JSON payload")
	})

	t.Run("empty json payload", func(t *testing.T) {
		payload := "" // Empty JSON payload

		req := httptest.NewRequest(http.MethodPost, "/set", strings.NewReader(payload))
		w := httptest.NewRecorder()

		server.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		assert.Contains(t, string(body), "Invalid JSON payload")
	})

	t.Run("validation error from cabinet", func(t *testing.T) {
		// This workflow does not exist in the store
		payload := `{"workflow_id": "non.existent.flow@1.0.0", "modules": ["core.ping@1.0.0"]}`

		req := httptest.NewRequest(http.MethodPost, "/set", strings.NewReader(payload))
		w := httptest.NewRecorder()

		server.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		assert.Contains(t, string(body), "Invalid request")
		assert.Contains(t, string(body), "workflow_id not found")
	})

	t.Run("unsupported method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/set", nil)
		w := httptest.NewRecorder()

		server.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusMethodNotAllowed, res.StatusCode)
	})

	t.Run("internal server error", func(t *testing.T) {
		// Setup a mock service that always returns a generic error
		mockSvc := &mockCabinetService{
			SetFunc: func(ctx context.Context, p *cabinet.SetPayload) (*cabinet.SetResponse, error) {
				return nil, errors.New("a generic internal error")
			},
		}
		mockAPI := cabinet.NewAPI(mockSvc)
		// Initialize a default config for the mock server
		defaultConfig, err := loadConfig()
		require.NoError(t, err)
		mockServer := &relayServer{api: mockAPI, config: defaultConfig}

		payload := `{"workflow_id": "any", "modules": ["any"]}`
		req := httptest.NewRequest(http.MethodPost, "/set", strings.NewReader(payload))
		w := httptest.NewRecorder()

		mockServer.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		assert.Contains(t, string(body), "Internal server error")
	})

	t.Run("cabinet invalid error", func(t *testing.T) {
		// Setup a mock service that returns a cabinet.ErrInvalid error
		mockSvc := &mockCabinetService{
			SetFunc: func(ctx context.Context, p *cabinet.SetPayload) (*cabinet.SetResponse, error) {
				return nil, fmt.Errorf("%w: specific validation error", cabinet.ErrInvalid)
			},
		}
		mockAPI := cabinet.NewAPI(mockSvc)
		defaultConfig, err := loadConfig()
		require.NoError(t, err)
		mockServer := &relayServer{api: mockAPI, config: defaultConfig}

		payload := `{"workflow_id": "any", "modules": ["any"]}`
		req := httptest.NewRequest(http.MethodPost, "/set", strings.NewReader(payload))
		w := httptest.NewRecorder()

		mockServer.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		// Corrected assertion to match the actual error output from the wrapped error.
		assert.Equal(t, "Invalid request: invalid: specific validation error", strings.TrimSpace(string(body)))
	})

	t.Run("read body error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/set", &failingReader{})
		w := httptest.NewRecorder()

		server.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		assert.Contains(t, string(body), "Failed to read request")
	})

	t.Run("request body too large", func(t *testing.T) {
		// Create a request body that exceeds MaxJSONInputSize
		largePayload := strings.Repeat("a", int(server.config.MaxJSONInputSize)+1) // One byte over the limit
		req := httptest.NewRequest(http.MethodPost, "/set", strings.NewReader(largePayload))
		w := httptest.NewRecorder()

		server.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		assert.Contains(t, string(body), fmt.Sprintf("Request body too large, exceeds %d bytes", server.config.MaxJSONInputSize))
	})
}

func TestLoadConfig(t *testing.T) {
	// Use t.Setenv to ensure test isolation. It automatically restores the original value.
	t.Run("default values", func(t *testing.T) {
		// To test defaults, we need to ensure the vars are not set.
		// The safest way is to save/unset/restore within this subtest.
		originalMaxJSON := os.Getenv("MAX_JSON_INPUT_SIZE")
		originalMaxWASM := os.Getenv("MAX_WASM_MODULE_SIZE")
		originalHashingTimeout := os.Getenv("HASHING_TIMEOUT")
		os.Unsetenv("MAX_JSON_INPUT_SIZE")
		os.Unsetenv("MAX_WASM_MODULE_SIZE")
		os.Unsetenv("HASHING_TIMEOUT")
		defer func() {
			os.Setenv("MAX_JSON_INPUT_SIZE", originalMaxJSON)
			os.Setenv("MAX_WASM_MODULE_SIZE", originalMaxWASM)
			os.Setenv("HASHING_TIMEOUT", originalHashingTimeout)
		}()

		cfg, err := loadConfig()
		require.NoError(t, err)
		assert.Equal(t, int64(4*1024*1024), cfg.MaxJSONInputSize)
		assert.Equal(t, int64(16*1024*1024), cfg.MaxWASMModuleSize)
		assert.Equal(t, 5*time.Second, cfg.HashingTimeout)
	})

	t.Run("valid values from env vars", func(t *testing.T) {
		t.Setenv("MAX_JSON_INPUT_SIZE", "1048576") // 1 MB
		t.Setenv("MAX_WASM_MODULE_SIZE", "8388608")  // 8 MB
		t.Setenv("HASHING_TIMEOUT", "10s")

		cfg, err := loadConfig()
		require.NoError(t, err)
		assert.Equal(t, int64(1048576), cfg.MaxJSONInputSize)
		assert.Equal(t, int64(8388608), cfg.MaxWASMModuleSize)
		assert.Equal(t, 10*time.Second, cfg.HashingTimeout)
	})

	t.Run("invalid max json size", func(t *testing.T) {
		t.Setenv("MAX_JSON_INPUT_SIZE", "not-a-number")
		cfg, err := loadConfig()
		assert.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "invalid value for MAX_JSON_INPUT_SIZE")
	})

	t.Run("invalid max wasm size", func(t *testing.T) {
		t.Setenv("MAX_WASM_MODULE_SIZE", "not-a-number")
		cfg, err := loadConfig()
		assert.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "invalid value for MAX_WASM_MODULE_SIZE")
	})

	t.Run("invalid hashing timeout", func(t *testing.T) {
		t.Setenv("HASHING_TIMEOUT", "not-a-duration")
		cfg, err := loadConfig()
		assert.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "invalid value for HASHING_TIMEOUT")
	})
}
