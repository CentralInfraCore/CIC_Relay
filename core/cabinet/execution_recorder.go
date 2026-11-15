package cabinet

import "context"

// ExecutionRecorder is an optional sink for ProofTrace results produced by Setx.
// If nil, recording is skipped. A recording error does not fail the call unless
// the service requires evidence (WithEvidenceRequired); either way the outcome is
// reported in SetResponse.Proof.
type ExecutionRecorder interface {
	Record(ctx context.Context, workflowID string, trace interface{}) error
}

// CommitRecorder is an optional extension of ExecutionRecorder for recorders that
// can name the persisted record (for the git recorder: the commit hash). Setx
// prefers it, so the commit can be returned in SetResponse.Proof.Commit.
type CommitRecorder interface {
	RecordCommit(ctx context.Context, workflowID string, trace interface{}) (commit string, err error)
}
