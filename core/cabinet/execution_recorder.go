package cabinet

import "context"

// ExecutionRecorder is an optional sink for ProofTrace results produced by Setx.
// If nil, recording is silently skipped. Errors from recording are non-fatal:
// the caller logs a WARN and continues.
type ExecutionRecorder interface {
	Record(ctx context.Context, workflowID string, trace interface{}) error
}