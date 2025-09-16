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
	"strings"
	"testing"

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
	dummyServer := &relayServer{api: dummyAPI}

	err := run(":12345", logger, dummyServer)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !called {
		t.Fatalf("listenAndServe was not called")
	}
	if gotAddr != ":12345" {
		t.Fatalf("listenAndServe addr = %q, want %q", gotAddr, ":12345")
	}

	// Ellenőrizzük, hogy a handler valóban kiszolgálja a "/"-t a várt JSON-nal
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
	dummyServer := &relayServer{api: dummyAPI}

	err := run(":0", logger, dummyServer)
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
	pingSchema := &cabinet.Schema{Meta: cabinet.BaseBlock{Name: "core.ping", Version: "1.0.0"}}
	pingModule := &cabinet.ModuleDescriptor{Meta: cabinet.BaseBlock{Name: "core.ping", Version: "1.0.0"}, Accepts: []string{pingSchema.ID()}}
	pingWorkflow := &cabinet.Workflow{Meta: cabinet.BaseBlock{Name: "core.ping.flow", Version: "1.0.0"}, Steps: []string{pingSchema.ID()}}

	require.NoError(t, cabinetSvc.PutSchema(pingSchema))
	require.NoError(t, cabinetSvc.PutWorkflow(pingWorkflow))
	require.NoError(t, cabinetSvc.PutModule(pingModule))

	api := cabinet.NewAPI(cabinetSvc)
	server := &relayServer{api: api}

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
		mockServer := &relayServer{api: mockAPI}

		payload := `{"workflow_id": "any", "modules": ["any"]}`
		req := httptest.NewRequest(http.MethodPost, "/set", strings.NewReader(payload))
		w := httptest.NewRecorder()

		mockServer.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		assert.Contains(t, string(body), "Internal server error")
	})

	t.Run("request body too large", func(t *testing.T) {
		// Create a request body that exceeds MaxJSONInputSize
		largePayload := strings.Repeat("a", MaxJSONInputSize+1) // One byte over the limit
		req := httptest.NewRequest(http.MethodPost, "/set", strings.NewReader(largePayload))
		w := httptest.NewRecorder()

		server.setHandler(w, req)

		res := w.Result()
		assert.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		assert.Contains(t, string(body), fmt.Sprintf("Request body too large, exceeds %d bytes", MaxJSONInputSize))
	})
}
