package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"centralrelay/core/cabinet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// postRepoCI sends body (marshalled to JSON) to the handler and returns the recorder.
func postRepoCI(t *testing.T, s *relayServer, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/repo/ci", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	s.repoCIHandler(w, req)
	return w
}

// captureSetx makes the mock accept any Setx call, store the payload and return an empty success.
func captureSetx(m *mockCabinetService) **cabinet.SetPayload {
	var got *cabinet.SetPayload
	m.On("Setx", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(*cabinet.SetPayload) }).
		Return(&cabinet.SetResponse{Results: map[string]any{}}, nil)
	return &got
}

func validRepoCIBody() map[string]any {
	return map[string]any{
		"repo_url":        "file:///src/app",
		"branch":          "main",
		"artifact_output": "dist/app.tar",
	}
}

func TestRepoCIHandler_MethodNotAllowed(t *testing.T) {
	s := newTestPipelineServer(t, new(mockCabinetService))
	req := httptest.NewRequest(http.MethodGet, "/v1/repo/ci", nil)
	w := httptest.NewRecorder()
	s.repoCIHandler(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assertErrorJSON(t, w, "method_not_allowed")
}

func TestRepoCIHandler_ReadBodyError(t *testing.T) {
	s := newTestPipelineServer(t, new(mockCabinetService))
	req := httptest.NewRequest(http.MethodPost, "/v1/repo/ci", io.NopCloser(&errorReader{}))
	w := httptest.NewRecorder()
	s.repoCIHandler(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorJSON(t, w, "read_error")
}

func TestRepoCIHandler_BodyTooLarge(t *testing.T) {
	s := newTestPipelineServer(t, new(mockCabinetService))
	s.config.MaxJSONInputSize = 10
	req := httptest.NewRequest(http.MethodPost, "/v1/repo/ci", bytes.NewReader(bytes.Repeat([]byte("x"), 20)))
	w := httptest.NewRecorder()
	s.repoCIHandler(w, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assertErrorJSON(t, w, "body_too_large")
}

func TestRepoCIHandler_InvalidJSON(t *testing.T) {
	s := newTestPipelineServer(t, new(mockCabinetService))
	req := httptest.NewRequest(http.MethodPost, "/v1/repo/ci", bytes.NewReader([]byte(`{not json`)))
	w := httptest.NewRecorder()
	s.repoCIHandler(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorJSON(t, w, "invalid_json")
}

// Every rejected request must stop before the workflow runs: the mock has no
// Setx expectation, so an unexpected call panics.
func TestRepoCIHandler_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing repo_url", map[string]any{"branch": "main", "artifact_output": "a.tar"}},
		{"missing artifact_output", map[string]any{"repo_url": "file:///x", "branch": "main"}},
		{"neither branch nor commit", map[string]any{"repo_url": "file:///x", "artifact_output": "a.tar"}},
		{"empty body object", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := new(mockCabinetService)
			w := postRepoCI(t, newTestPipelineServer(t, m), tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrorJSON(t, w, "missing_fields")
			m.AssertNotCalled(t, "Setx", mock.Anything, mock.Anything)
		})
	}
}

func TestRepoCIHandler_Success(t *testing.T) {
	m := new(mockCabinetService)
	m.On("Setx", mock.Anything, mock.MatchedBy(func(sp *cabinet.SetPayload) bool {
		return sp.WorkflowID == "cic.repo.ci@1.0" &&
			sp.Payload["repo_url"] == "file:///src/app" &&
			sp.Payload["branch"] == "main" &&
			sp.Payload["artifact_output"] == "dist/app.tar" &&
			sp.Payload["build_dir"] == "/build" && // config default
			sp.Payload["builder_container"] == "cic-schemas-builder" // config default
	})).Return(&cabinet.SetResponse{
		Results: map[string]any{
			"signed_artifact": map[string]any{"build_hash": "sha256:abc", "verification_root": "deadbeef"},
		},
		Trace: &cabinet.ProofTrace{ChainHash: "cafebabe"},
	}, nil)

	w := postRepoCI(t, newTestPipelineServer(t, m), validRepoCIBody())

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var resp RepoCIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	artifact, ok := resp.Artifact.(map[string]any)
	require.True(t, ok, "artifact must be the signed_artifact object")
	assert.Equal(t, "sha256:abc", artifact["build_hash"])
	require.NotNil(t, resp.ProofTrace)
	assert.Equal(t, "cafebabe", resp.ProofTrace.ChainHash)
	m.AssertExpectations(t)
}

// Optional fields are forwarded only when the caller set them.
func TestRepoCIHandler_OptionalFieldsOmittedWhenUnset(t *testing.T) {
	m := new(mockCabinetService)
	got := captureSetx(m)
	w := postRepoCI(t, newTestPipelineServer(t, m), validRepoCIBody())
	require.Equal(t, http.StatusOK, w.Code)

	require.NotNil(t, *got)
	for _, k := range []string{"commit", "make_target"} {
		_, present := (*got).Payload[k]
		assert.False(t, present, "%s must not be forwarded when unset", k)
	}
}

func TestRepoCIHandler_OptionalFieldsForwarded(t *testing.T) {
	m := new(mockCabinetService)
	got := captureSetx(m)
	body := validRepoCIBody()
	body["commit"] = "0123456789abcdef"
	body["make_target"] = "release"
	w := postRepoCI(t, newTestPipelineServer(t, m), body)
	require.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "0123456789abcdef", (*got).Payload["commit"])
	assert.Equal(t, "release", (*got).Payload["make_target"])
}

func TestRepoCIHandler_CommitOnlyIsAccepted(t *testing.T) {
	m := new(mockCabinetService)
	got := captureSetx(m)
	w := postRepoCI(t, newTestPipelineServer(t, m), map[string]any{
		"repo_url": "file:///x", "commit": "abc123", "artifact_output": "a.tar",
	})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "abc123", (*got).Payload["commit"])
	assert.Equal(t, "", (*got).Payload["branch"], "branch is always present, empty when only a commit is pinned")
}

func TestRepoCIHandler_RequestOverridesConfig(t *testing.T) {
	m := new(mockCabinetService)
	got := captureSetx(m)
	body := validRepoCIBody()
	body["build_dir"] = "/custom-build"
	body["builder_container"] = "my-builder"
	w := postRepoCI(t, newTestPipelineServer(t, m), body)
	require.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "/custom-build", (*got).Payload["build_dir"])
	assert.Equal(t, "my-builder", (*got).Payload["builder_container"])
}

func TestRepoCIHandler_NoConfigDefaultsNoBuildFields(t *testing.T) {
	m := new(mockCabinetService)
	got := captureSetx(m)
	s := newTestPipelineServer(t, m)
	s.config.SchemaBuildDir = ""
	s.config.SchemaBuilderContainer = ""
	w := postRepoCI(t, s, validRepoCIBody())
	require.Equal(t, http.StatusOK, w.Code)

	for _, k := range []string{"build_dir", "builder_container"} {
		_, present := (*got).Payload[k]
		assert.False(t, present, "%s must not be forwarded when neither request nor config set it", k)
	}
}

// SourceDigest anchors the proof trace: a pinned commit wins, then the relay's
// own SourceTreeHash, then its CommitHash.
func TestRepoCIHandler_SourceDigest(t *testing.T) {
	origTree, origCommit := SourceTreeHash, CommitHash
	t.Cleanup(func() { SourceTreeHash, CommitHash = origTree, origCommit })

	cases := []struct {
		name       string
		reqCommit  string
		tree, comm string
		want       string
	}{
		{"pinned commit wins", "pinned123", "sha256:tree", "relaycommit", "pinned123"},
		{"tree hash when no commit pinned", "", "sha256:tree", "relaycommit", "sha256:tree"},
		{"relay commit when tree hash unknown", "", "unknown", "relaycommit", "relaycommit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SourceTreeHash, CommitHash = tc.tree, tc.comm
			m := new(mockCabinetService)
			got := captureSetx(m)
			body := validRepoCIBody()
			if tc.reqCommit != "" {
				body["commit"] = tc.reqCommit
			}
			w := postRepoCI(t, newTestPipelineServer(t, m), body)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tc.want, (*got).SourceDigest)
		})
	}
}

func TestRepoCIHandler_Timeout(t *testing.T) {
	m := new(mockCabinetService)
	m.On("Setx", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("%w: ci timed out", cabinet.ErrTimeout))
	w := postRepoCI(t, newTestPipelineServer(t, m), validRepoCIBody())
	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
	assertErrorJSON(t, w, "timeout")
}

func TestRepoCIHandler_InternalError(t *testing.T) {
	m := new(mockCabinetService)
	m.On("Setx", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("make: *** [build] Error 2"))
	w := postRepoCI(t, newTestPipelineServer(t, m), validRepoCIBody())
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorJSON(t, w, "internal_error")
}
