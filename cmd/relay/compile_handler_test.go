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

func newTestCompileServer(t *testing.T, mockAPI *mockCabinetService) *relayServer {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	return &relayServer{
		api: mockAPI,
		config: &config{
			MaxJSONInputSize: 1024 * 1024,
			HashingTimeout:   5 * time.Second,
		},
		logger: logger,
	}
}

func TestCompileHandler_MethodNotAllowed(t *testing.T) {
	server := newTestCompileServer(t, new(mockCabinetService))
	req := httptest.NewRequest(http.MethodGet, "/v1/schema/compile", nil)
	w := httptest.NewRecorder()
	server.compileHandler(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assertErrorJSON(t, w, "method_not_allowed")
}

func TestCompileHandler_BodyTooLarge(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	server := &relayServer{
		api:    new(mockCabinetService),
		config: &config{MaxJSONInputSize: 10},
		logger: logger,
	}
	body := bytes.Repeat([]byte("x"), 20)
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.compileHandler(w, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assertErrorJSON(t, w, "body_too_large")
}

func TestCompileHandler_InvalidJSON(t *testing.T) {
	server := newTestCompileServer(t, new(mockCabinetService))
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", bytes.NewReader([]byte(`{not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.compileHandler(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorJSON(t, w, "invalid_json")
}

func TestCompileHandler_MissingSchema(t *testing.T) {
	server := newTestCompileServer(t, new(mockCabinetService))
	body, _ := json.Marshal(map[string]any{"version": "v1.0.0"})
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.compileHandler(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorJSON(t, w, "missing_fields")
}

func TestCompileHandler_MissingVersion(t *testing.T) {
	server := newTestCompileServer(t, new(mockCabinetService))
	body, _ := json.Marshal(map[string]any{"schema": "postgresql"})
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.compileHandler(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorJSON(t, w, "missing_fields")
}

func TestCompileHandler_Success(t *testing.T) {
	mockAPI := new(mockCabinetService)
	mockAPI.On("Setx", mock.Anything, mock.MatchedBy(func(sp *cabinet.SetPayload) bool {
		return sp.WorkflowID == "cic.schema.compile@1.0" &&
			sp.Payload["schema"] == "postgresql" &&
			sp.Payload["version"] == "v0.18.dev"
	})).Return(&cabinet.SetResponse{
		Results: map[string]any{
			"signed_artifact": map[string]any{
				"build_hash":        "sha256:abc123",
				"cic_sign":          "unavailable",
				"verification_root": "deadbeef",
			},
		},
		Trace: &cabinet.ProofTrace{ChainHash: "cafebabe"},
	}, nil)

	server := newTestCompileServer(t, mockAPI)
	body, _ := json.Marshal(map[string]any{"schema": "postgresql", "version": "v0.18.dev"})
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.compileHandler(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp CompileResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	artifact, ok := resp.Artifact.(map[string]any)
	require.True(t, ok, "artifact must be a map")
	assert.Equal(t, "sha256:abc123", artifact["build_hash"])
	require.NotNil(t, resp.ProofTrace)
	assert.Equal(t, "cafebabe", resp.ProofTrace.ChainHash)

	mockAPI.AssertExpectations(t)
}

func TestCompileHandler_WithSourceAssertion(t *testing.T) {
	mockAPI := new(mockCabinetService)
	mockAPI.On("Setx", mock.Anything, mock.MatchedBy(func(sp *cabinet.SetPayload) bool {
		sa, _ := sp.Payload["source_assertion"].(string)
		return sp.WorkflowID == "cic.schema.compile@1.0" && sa == "yaml-assertion-here"
	})).Return(&cabinet.SetResponse{
		Results: map[string]any{"signed_artifact": map[string]any{"build_hash": "sha256:def456"}},
	}, nil)

	server := newTestCompileServer(t, mockAPI)
	body, _ := json.Marshal(map[string]any{
		"schema":           "postgresql",
		"version":          "v0.18.dev",
		"source_assertion": "yaml-assertion-here",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.compileHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAPI.AssertExpectations(t)
}

func TestCompileHandler_Timeout(t *testing.T) {
	mockAPI := new(mockCabinetService)
	mockAPI.On("Setx", mock.Anything, mock.Anything).Return(
		nil, fmt.Errorf("%w: step timed out", cabinet.ErrTimeout),
	)
	server := newTestCompileServer(t, mockAPI)
	body, _ := json.Marshal(map[string]any{"schema": "postgresql", "version": "v0.18.dev"})
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.compileHandler(w, req)
	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
	assertErrorJSON(t, w, "timeout")
}

func TestCompileHandler_InternalError(t *testing.T) {
	mockAPI := new(mockCabinetService)
	mockAPI.On("Setx", mock.Anything, mock.Anything).Return(
		nil, fmt.Errorf("unexpected error"),
	)
	server := newTestCompileServer(t, mockAPI)
	body, _ := json.Marshal(map[string]any{"schema": "postgresql", "version": "v0.18.dev"})
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.compileHandler(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorJSON(t, w, "internal_error")
}

func TestCompileHandler_ReadBodyError(t *testing.T) {
	server := newTestCompileServer(t, new(mockCabinetService))
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", io.NopCloser(&errorReader{}))
	w := httptest.NewRecorder()
	server.compileHandler(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorJSON(t, w, "read_error")
}
