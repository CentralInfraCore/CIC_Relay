package recorder

import (
	"context"
	"fmt"
	"time"

	"centralrelay/core/nexus/types"
)

// WorkflowRecorder adapts StateRecorder to the cabinet.ExecutionRecorder interface.
// It wraps each ProofTrace as the "actual" state and builds a minimal ExpectedState
// from the workflowID. The Graph field is nil for workflow executions (not IaC).
//
// It satisfies cabinet.ExecutionRecorder structurally:
//
//	func Record(ctx context.Context, workflowID string, trace interface{}) error
type WorkflowRecorder struct {
	sr StateRecorder
}

// NewWorkflowRecorder creates a WorkflowRecorder wrapping the given StateRecorder.
func NewWorkflowRecorder(sr StateRecorder) *WorkflowRecorder {
	return &WorkflowRecorder{sr: sr}
}

// Record persists the ProofTrace via the underlying StateRecorder.
// workflowID is used as the RunID; trace is stored as the actual state.
func (w *WorkflowRecorder) Record(ctx context.Context, workflowID string, trace interface{}) error {
	if w.sr == nil {
		return fmt.Errorf("WorkflowRecorder: underlying StateRecorder is nil")
	}
	expected := types.ExpectedState{
		RunID:     workflowID,
		Timestamp: time.Now().UTC(),
	}
	_, err := w.sr.RecordState(ctx, expected, trace)
	return err
}