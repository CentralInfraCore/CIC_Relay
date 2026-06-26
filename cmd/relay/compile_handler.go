package main

import (
	"encoding/json"
	"io"
	"net/http"

	"centralrelay/core/cabinet"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// CompileRequest is the request body for POST /v1/schema/compile.
type CompileRequest struct {
	Schema          string `json:"schema"`
	Version         string `json:"version"`
	SourceAssertion string `json:"source_assertion,omitempty"` // developer-signed artifact YAML from CIC-Schemas
}

// CompileResponse is the response body for POST /v1/schema/compile.
type CompileResponse struct {
	Artifact   interface{}         `json:"artifact"`
	ProofTrace *cabinet.ProofTrace `json:"proof_trace"`
}

// compileHandler handles POST /v1/schema/compile requests by executing the cic.schema.compile workflow.
func (s *relayServer) compileHandler(w http.ResponseWriter, r *http.Request) {
	reqID := uuid.New().String()
	logEntry := s.logger.WithFields(logrus.Fields{
		"req-id": reqID,
		"path":   r.URL.Path,
		"method": r.Method,
	})
	logEntry.Info("Handling compile request")

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

	var req CompileRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON: "+err.Error())
		return
	}
	if req.Schema == "" || req.Version == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "schema and version are required")
		return
	}

	sourceDigest := SourceTreeHash
	if sourceDigest == "unknown" {
		sourceDigest = CommitHash
	}

	sp := &cabinet.SetPayload{
		WorkflowID:   "cic.schema.compile@1.0",
		Payload:      map[string]any{"schema": req.Schema, "version": req.Version, "source_assertion": req.SourceAssertion},
		SourceDigest: sourceDigest,
	}

	resp, err := s.api.Setx(r.Context(), sp)
	if err != nil {
		if cabinet.IsTimeout(err) {
			logEntry.WithField("outcome", "timeout").WithError(err).Warn("Compile workflow timed out")
			writeError(w, http.StatusGatewayTimeout, "timeout", "Workflow timed out: "+err.Error())
			return
		}
		logEntry.WithField("outcome", "internal_error").WithError(err).Error("Compile workflow failed")
		writeError(w, http.StatusInternalServerError, "internal_error", "Compile failed: "+err.Error())
		return
	}

	logEntry.WithField("outcome", "success").Info("Compile operation successful")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(CompileResponse{
		Artifact:   resp.Results["signed_artifact"],
		ProofTrace: resp.Trace,
	})
}
