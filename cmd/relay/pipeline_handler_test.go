package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"centralrelay/core/cabinet"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestPipelineServer(t *testing.T, mockAPI *mockCabinetService) *relayServer {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	return &relayServer{
		api: mockAPI,
		config: &config{
			MaxJSONInputSize:       1024 * 1024,
			HashingTimeout:         5 * time.Second,
			SchemaBuildDir:         "/build",
			SchemaBuilderContainer: "cic-schemas-builder",
		},
		logger: logger,
	}
}

func TestPipelineHandler_MethodNotAllowed(t *testing.T) {
	server := newTestPipelineServer(t, new(mockCabinetService))
	req := httptest.NewRequest(http.MethodGet, "/v1/schemas/pipeline", nil)
	w := httptest.NewRecorder()
	server.pipelineHandler(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assertErrorJSON(t, w, "method_not_allowed")
}

func TestPipelineHandler_BodyTooLarge(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	server := &relayServer{
		api:    new(mockCabinetService),
		config: &config{MaxJSONInputSize: 10},
		logger: logger,
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/schemas/pipeline", bytes.NewReader(bytes.Repeat([]byte("x"), 20)))
	w := httptest.NewRecorder()
	server.pipelineHandler(w, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assertErrorJSON(t, w, "body_too_large")
}

func TestPipelineHandler_InvalidJSON(t *testing.T) {
	server := newTestPipelineServer(t, new(mockCabinetService))
	req := httptest.NewRequest(http.MethodPost, "/v1/schemas/pipeline", bytes.NewReader([]byte(`{not json`)))
	w := httptest.NewRecorder()
	server.pipelineHandler(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorJSON(t, w, "invalid_json")
}

func TestPipelineHandler_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing schema", map[string]any{"version": "v1.0.0", "branch": "main", "repo_url": "https://x"}},
		{"missing version", map[string]any{"schema": "pg", "branch": "main", "repo_url": "https://x"}},
		{"missing branch", map[string]any{"schema": "pg", "version": "v1.0.0", "repo_url": "https://x"}},
		{"missing repo_url", map[string]any{"schema": "pg", "version": "v1.0.0", "branch": "main"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newTestPipelineServer(t, new(mockCabinetService))
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/v1/schemas/pipeline", bytes.NewReader(body))
			w := httptest.NewRecorder()
			server.pipelineHandler(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrorJSON(t, w, "missing_fields")
		})
	}
}

func TestPipelineHandler_Success(t *testing.T) {
	mockAPI := new(mockCabinetService)
	mockAPI.On("Setx", mock.Anything, mock.MatchedBy(func(sp *cabinet.SetPayload) bool {
		return sp.WorkflowID == "cic.schemas.pipeline@1.0" &&
			sp.Payload["schema"] == "postgresql" &&
			sp.Payload["version"] == "v0.18.0" &&
			sp.Payload["branch"] == "postgres/dev" &&
			sp.Payload["repo_url"] == "file:///src/cic-schemas" &&
			sp.Payload["build_dir"] == "/build" &&
			sp.Payload["builder_container"] == "cic-schemas-builder"
	})).Return(&cabinet.SetResponse{
		Results: map[string]any{
			"signed_artifact": map[string]any{
				"build_hash":        "sha256:abc123",
				"verification_root": "deadbeef",
			},
		},
		Trace: &cabinet.ProofTrace{ChainHash: "cafebabe"},
	}, nil)

	server := newTestPipelineServer(t, mockAPI)
	body, _ := json.Marshal(map[string]any{
		"schema":   "postgresql",
		"version":  "v0.18.0",
		"branch":   "postgres/dev",
		"repo_url": "file:///src/cic-schemas",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/schemas/pipeline", bytes.NewReader(body))
	w := httptest.NewRecorder()
	server.pipelineHandler(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp PipelineResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	artifact := resp.Artifact.(map[string]any)
	assert.Equal(t, "sha256:abc123", artifact["build_hash"])
	require.NotNil(t, resp.ProofTrace)
	assert.Equal(t, "cafebabe", resp.ProofTrace.ChainHash)

	mockAPI.AssertExpectations(t)
}

func TestPipelineHandler_RequestOverridesConfig(t *testing.T) {
	mockAPI := new(mockCabinetService)
	mockAPI.On("Setx", mock.Anything, mock.MatchedBy(func(sp *cabinet.SetPayload) bool {
		// Per-request overrides must win over config defaults.
		return sp.Payload["build_dir"] == "/custom-build" &&
			sp.Payload["builder_container"] == "my-builder"
	})).Return(&cabinet.SetResponse{Results: map[string]any{}}, nil)

	server := newTestPipelineServer(t, mockAPI)
	body, _ := json.Marshal(map[string]any{
		"schema":            "postgresql",
		"version":           "v0.18.0",
		"branch":            "main",
		"repo_url":          "file:///src",
		"build_dir":         "/custom-build",
		"builder_container": "my-builder",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/schemas/pipeline", bytes.NewReader(body))
	w := httptest.NewRecorder()
	server.pipelineHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAPI.AssertExpectations(t)
}

func TestPipelineHandler_ConfigDefaultsUsed(t *testing.T) {
	// When request does not set build_dir/builder_container, config values are used.
	mockAPI := new(mockCabinetService)
	mockAPI.On("Setx", mock.Anything, mock.MatchedBy(func(sp *cabinet.SetPayload) bool {
		return sp.Payload["build_dir"] == "/build" &&
			sp.Payload["builder_container"] == "cic-schemas-builder"
	})).Return(&cabinet.SetResponse{Results: map[string]any{}}, nil)

	server := newTestPipelineServer(t, mockAPI)
	body, _ := json.Marshal(map[string]any{
		"schema": "pg", "version": "v1.0.0", "branch": "main", "repo_url": "file:///src",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/schemas/pipeline", bytes.NewReader(body))
	w := httptest.NewRecorder()
	server.pipelineHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAPI.AssertExpectations(t)
}

func TestPipelineHandler_Timeout(t *testing.T) {
	mockAPI := new(mockCabinetService)
	mockAPI.On("Setx", mock.Anything, mock.Anything).Return(
		nil, fmt.Errorf("%w: pipeline timed out", cabinet.ErrTimeout),
	)
	server := newTestPipelineServer(t, mockAPI)
	body, _ := json.Marshal(map[string]any{
		"schema": "pg", "version": "v1.0.0", "branch": "main", "repo_url": "file:///src",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/schemas/pipeline", bytes.NewReader(body))
	w := httptest.NewRecorder()
	server.pipelineHandler(w, req)
	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
	assertErrorJSON(t, w, "timeout")
}

func TestPipelineHandler_InternalError(t *testing.T) {
	mockAPI := new(mockCabinetService)
	mockAPI.On("Setx", mock.Anything, mock.Anything).Return(
		nil, fmt.Errorf("git clone failed"),
	)
	server := newTestPipelineServer(t, mockAPI)
	body, _ := json.Marshal(map[string]any{
		"schema": "pg", "version": "v1.0.0", "branch": "main", "repo_url": "file:///src",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/schemas/pipeline", bytes.NewReader(body))
	w := httptest.NewRecorder()
	server.pipelineHandler(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorJSON(t, w, "internal_error")
}

func TestPipelineHandler_ReadBodyError(t *testing.T) {
	server := newTestPipelineServer(t, new(mockCabinetService))
	req := httptest.NewRequest(http.MethodPost, "/v1/schemas/pipeline", io.NopCloser(&errorReader{}))
	w := httptest.NewRecorder()
	server.pipelineHandler(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorJSON(t, w, "read_error")
}
