package main

import (
	"encoding/json"
	"io"
	"net/http"

	"centralrelay/core/cabinet"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// RepoCIRequest is the request body for POST /v1/repo/ci.
//
// The schema pipeline (POST /v1/schemas/pipeline) is CIC-Schemas-shaped: it
// requires schema/version and reads the released artifact from a fixed
// source/<schema>.yaml. This route is the generic counterpart — the caller names
// the make target and the file it produces.
type RepoCIRequest struct {
	RepoURL string `json:"repo_url"`
	// Commit pins an exact revision; Branch builds that branch's tip. One is required.
	Commit string `json:"commit"`
	Branch string `json:"branch"`
	// MakeTarget defaults to "build".
	MakeTarget string `json:"make_target"`
	// ArtifactOutput is the file make produces, relative to the build directory.
	// Its contents are what gets signed. Required — a CI run that names no
	// artifact has nothing to attest.
	ArtifactOutput   string `json:"artifact_output"`
	BuildDir         string `json:"build_dir"`
	BuilderContainer string `json:"builder_container"`
}

// RepoCIResponse is the response body for POST /v1/repo/ci.
type RepoCIResponse struct {
	Artifact   interface{}         `json:"artifact"`
	ProofTrace *cabinet.ProofTrace `json:"proof_trace"`
}

// repoCIHandler handles POST /v1/repo/ci by executing the cic.repo.ci workflow.
func (s *relayServer) repoCIHandler(w http.ResponseWriter, r *http.Request) {
	reqID := uuid.New().String()
	logEntry := s.logger.WithFields(logrus.Fields{
		"req-id": reqID,
		"path":   r.URL.Path,
		"method": r.Method,
	})
	logEntry.Info("Handling repo CI request")

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST method is allowed")
		return
	}

	limitedReader := io.LimitReader(r.Body, s.config.MaxJSONInputSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		logEntry.WithField("outcome", "read_error").WithError(err).Error("Failed to read request body")
		writeError(w, http.StatusInternalServerError, "read_error", "Failed to read request")
		return
	}
	defer r.Body.Close()

	if int64(len(body)) > s.config.MaxJSONInputSize {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Request body too large")
		return
	}

	var req RepoCIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON: "+err.Error())
		return
	}
	if req.RepoURL == "" || req.ArtifactOutput == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "repo_url and artifact_output are required")
		return
	}
	if req.Branch == "" && req.Commit == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "either branch or commit is required")
		return
	}

	payload := map[string]any{
		"repo_url":        req.RepoURL,
		"branch":          req.Branch,
		"artifact_output": req.ArtifactOutput,
	}
	if req.Commit != "" {
		payload["commit"] = req.Commit
	}
	if req.MakeTarget != "" {
		payload["make_target"] = req.MakeTarget
	}
	if req.BuildDir != "" {
		payload["build_dir"] = req.BuildDir
	} else if s.config.SchemaBuildDir != "" {
		payload["build_dir"] = s.config.SchemaBuildDir
	}
	if req.BuilderContainer != "" {
		payload["builder_container"] = req.BuilderContainer
	} else if s.config.SchemaBuilderContainer != "" {
		payload["builder_container"] = s.config.SchemaBuilderContainer
	}

	// Anchor the trace to the revision under build when the caller pinned one.
	// Otherwise fall back to the relay's own build constants — see
	// docs/en/pipeline/source-provenance.md.
	sourceDigest := req.Commit
	if sourceDigest == "" {
		sourceDigest = SourceTreeHash
		if sourceDigest == "unknown" {
			sourceDigest = CommitHash
		}
	}

	sp := &cabinet.SetPayload{
		WorkflowID:   "cic.repo.ci@1.0",
		Payload:      payload,
		SourceDigest: sourceDigest,
	}

	resp, err := s.api.Setx(r.Context(), sp)
	if err != nil {
		if cabinet.IsTimeout(err) {
			logEntry.WithField("outcome", "timeout").WithError(err).Warn("Repo CI workflow timed out")
			writeError(w, http.StatusGatewayTimeout, "timeout", "Workflow timed out: "+err.Error())
			return
		}
		logEntry.WithField("outcome", "internal_error").WithError(err).Error("Repo CI workflow failed")
		writeError(w, http.StatusInternalServerError, "internal_error", "Repo CI failed: "+err.Error())
		return
	}

	logEntry.WithField("outcome", "success").Info("Repo CI completed successfully")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(RepoCIResponse{
		Artifact:   resp.Results["signed_artifact"],
		ProofTrace: resp.Trace,
	})
}
