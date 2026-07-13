// This file has no build tag: the result types and parser are plain Go, usable
// (and testable) without the Rust staticlib. Only the FFI calls in bridge.go
// need cgo; interpreting their JSON output does not.

package cicffi

import (
	"encoding/json"
	"fmt"
)

// ActuationAudit is the non-secret audit record the Rust run_flow emits for an
// actuation (feature-009). It never carries the credential, the Authorization
// header, a Vault token, or any body contents — only digests and facts, so it is
// safe to log or fold into a ProofTrace. It mirrors http-executor's
// ChainedEntry(AuditRecord): RecordHash is the tamper-evident SHA-256 that binds
// this record to its position in the audit chain.
type ActuationAudit struct {
	// TraceID is the caller's correlation id (echoed from the run_flow request),
	// empty if none was supplied.
	TraceID string `json:"trace_id"`
	// Method is the HTTP method ("GET", "POST", ...).
	Method string `json:"method"`
	// Host is the target host ("unknown" if the target did not parse).
	Host string `json:"host"`
	// Decision is "allow" or "deny:<reason>".
	Decision string `json:"decision"`
	// Status is the endpoint status, nil if none was reached.
	Status *int `json:"status"`
	// DurationMs is the wall-clock actuation duration in milliseconds.
	DurationMs uint64 `json:"duration_ms"`
	// RequestBodySHA256 is the hex SHA-256 of the request body.
	RequestBodySHA256 string `json:"request_body_sha256"`
	// ResponseBodySHA256 is the hex SHA-256 of the response body, empty if none.
	ResponseBodySHA256 string `json:"response_body_sha256"`
	// Seq is the zero-based position in the audit chain.
	Seq uint64 `json:"seq"`
	// RecordHash is the hex SHA-256 binding the record to its chain position.
	RecordHash string `json:"record_hash"`
}

// RunFlowResult is the typed form of the run_flow JSON output. On success OK is
// true and Status/BodyBase64/Scope/Audit are populated; on a flow failure OK is
// false and Error carries the non-secret reason.
type RunFlowResult struct {
	OK         bool            `json:"ok"`
	Status     int             `json:"status"`
	BodyBase64 string          `json:"body_base64"`
	Scope      []string        `json:"scope"`
	Audit      *ActuationAudit `json:"audit"`
	Error      string          `json:"error"`
}

// ParseRunFlowResult decodes the JSON returned by RunFlow into a RunFlowResult.
func ParseRunFlowResult(raw []byte) (RunFlowResult, error) {
	var res RunFlowResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return RunFlowResult{}, fmt.Errorf("cicffi: parse run_flow result: %w", err)
	}
	return res, nil
}
