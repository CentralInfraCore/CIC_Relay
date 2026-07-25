package main

import (
	"encoding/json"
	"io"
	"net/http"

	"centralrelay/core/cabinet"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// PipelineRequest is the request body for POST /v1/schemas/pipeline.
type PipelineRequest struct {
	Schema  string `json:"schema"`  // e.g. "postgresql"
	Version string `json:"version"` // e.g. "v0.18.0"
	Branch  string `json:"branch"`  // e.g. "postgres/dev"
	// Commit pins the build to an exact revision. When set, the pipeline takes a
	// full clone and checks this commit out, so the resulting build_hash attests
	// that revision rather than whatever the branch tip happened to be. Either
	// Commit or Branch must be present.
	Commit           string `json:"commit"`
	RepoURL          string `json:"repo_url"`          // e.g. "file:///src/cic-schemas"
	BuildDir         string `json:"build_dir"`         // override config default
	BuilderContainer string `json:"builder_container"` // override config default
}

// PipelineResponse is the response body for POST /v1/schemas/pipeline.
type PipelineResponse struct {
	Artifact   interface{}         `json:"artifact"`
	ProofTrace *cabinet.ProofTrace `json:"proof_trace"`
}

// pipelineHandler handles POST /v1/schemas/pipeline requests by executing the cic.schemas.pipeline workflow.
func (s *relayServer) pipelineHandler(w http.ResponseWriter, r *http.Request) {
	reqID := uuid.New().String()
	logEntry := s.logger.WithFields(logrus.Fields{
		"req-id": reqID,
		"path":   r.URL.Path,
		"method": r.Method,
	})
	logEntry.Info("Handling pipeline request")

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

	var req PipelineRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON: "+err.Error())
		return
	}
	if req.Schema == "" || req.Version == "" || req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "schema, version and repo_url are required")
		return
	}
	if req.Branch == "" && req.Commit == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "either branch or commit is required")
		return
	}

	// Build the workflow input, merging config defaults with per-request overrides.
	payload := map[string]any{
		"schema":   req.Schema,
		"version":  req.Version,
		"branch":   req.Branch,
		"repo_url": req.RepoURL,
	}
	if req.Commit != "" {
		payload["commit"] = req.Commit
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

	// Anchor the trace to the revision under build, not to the relay binary.
	//
	// SourceTreeHash/CommitHash describe the relay's own source, which is the
	// right answer for a request that carries no repository but the wrong one
	// here: it made every pipeline run report the same source_digest regardless
	// of what was actually built. When the caller pinned a commit we can anchor
	// to it up front; otherwise the pipeline resolves the cloned HEAD and the
	// signed artifact carries source_ref/source_tree_digest.
	sourceDigest := req.Commit
	if sourceDigest == "" {
		sourceDigest = SourceTreeHash
		if sourceDigest == "unknown" {
			sourceDigest = CommitHash
		}
	}

	sp := &cabinet.SetPayload{
		WorkflowID:   "cic.schemas.pipeline@1.0",
		Payload:      payload,
		SourceDigest: sourceDigest,
	}

	resp, err := s.api.Setx(r.Context(), sp)
	if err != nil {
		if cabinet.IsTimeout(err) {
			logEntry.WithField("outcome", "timeout").WithError(err).Warn("Pipeline workflow timed out")
			writeError(w, http.StatusGatewayTimeout, "timeout", "Workflow timed out: "+err.Error())
			return
		}
		logEntry.WithField("outcome", "internal_error").WithError(err).Error("Pipeline workflow failed")
		writeError(w, http.StatusInternalServerError, "internal_error", "Pipeline failed: "+err.Error())
		return
	}

	logEntry.WithField("outcome", "success").Info("Pipeline completed successfully")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(PipelineResponse{
		Artifact:   resp.Results["signed_artifact"],
		ProofTrace: resp.Trace,
	})
}
