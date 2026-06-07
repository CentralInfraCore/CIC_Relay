package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"centralrelay/core/cabinet"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestProofVerifyServer(t *testing.T) *relayServer {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	return &relayServer{
		api: new(mockCabinetService),
		config: &config{
			MaxJSONInputSize: 1024 * 1024,
			HashingTimeout:   5 * time.Second,
		},
		logger: logger,
	}
}

func validProofVerifyBody(t *testing.T) []byte {
	t.Helper()
	steps := []cabinet.ProofTraceStep{
		{Name: "step1", Module: "mod@1.0", InputHash: "aaa", OutputHash: "bbb"},
	}
	a := &ProofArtifact{
		WorkflowID:   "wf@1.0",
		SourceDigest: "src123",
		ChainHash:    cabinet.ComputeChainHashV1("wf@1.0", "src123", steps),
		Steps: []ProofArtifactStep{
			{Name: "step1", Module: "mod@1.0", InputHash: "aaa", OutputHash: "bbb"},
		},
		Timestamp: "2026-01-01T00:00:00Z",
	}
	b, err := json.Marshal(ProofVerifyRequest{ProofArtifact: a})
	require.NoError(t, err)
	return b
}

func TestProofVerifyHandler_MethodNotAllowed(t *testing.T) {
	s := newTestProofVerifyServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/proof/verify", nil)
	w := httptest.NewRecorder()
	s.proofVerifyHandler(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assertErrorJSON(t, w, "method_not_allowed")
}

func TestProofVerifyHandler_BodyTooLarge(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	s := &relayServer{
		api:    new(mockCabinetService),
		config: &config{MaxJSONInputSize: 10},
		logger: logger,
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/proof/verify", bytes.NewReader(bytes.Repeat([]byte("x"), 20)))
	w := httptest.NewRecorder()
	s.proofVerifyHandler(w, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assertErrorJSON(t, w, "body_too_large")
}

func TestProofVerifyHandler_InvalidJSON(t *testing.T) {
	s := newTestProofVerifyServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/proof/verify", bytes.NewReader([]byte(`{not json`)))
	w := httptest.NewRecorder()
	s.proofVerifyHandler(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorJSON(t, w, "invalid_json")
}

func TestProofVerifyHandler_MissingProofArtifact(t *testing.T) {
	s := newTestProofVerifyServer(t)
	body, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/v1/proof/verify", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.proofVerifyHandler(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorJSON(t, w, "missing_fields")
}

func TestProofVerifyHandler_ReadBodyError(t *testing.T) {
	s := newTestProofVerifyServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/proof/verify", io.NopCloser(&errorReader{}))
	w := httptest.NewRecorder()
	s.proofVerifyHandler(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorJSON(t, w, "read_error")
}

func TestProofVerifyHandler_ValidArtifact(t *testing.T) {
	s := newTestProofVerifyServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/proof/verify", bytes.NewReader(validProofVerifyBody(t)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.proofVerifyHandler(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp ProofVerifyResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Valid)
	assert.Empty(t, resp.Errors)
}

func TestProofVerifyHandler_InvalidChainHash(t *testing.T) {
	s := newTestProofVerifyServer(t)

	a := &ProofArtifact{
		WorkflowID:   "wf@1.0",
		SourceDigest: "src123",
		ChainHash:    strings.Repeat("d", 64), // wrong hash
		Steps: []ProofArtifactStep{
			{Name: "step1", Module: "mod@1.0", InputHash: "aaa", OutputHash: "bbb"},
		},
		Timestamp: "2026-01-01T00:00:00Z",
	}
	body, _ := json.Marshal(ProofVerifyRequest{ProofArtifact: a})

	req := httptest.NewRequest(http.MethodPost, "/v1/proof/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.proofVerifyHandler(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp ProofVerifyResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Valid)
	require.NotEmpty(t, resp.Errors)
	assert.Contains(t, resp.Errors[0], "chain_hash mismatch")
}
