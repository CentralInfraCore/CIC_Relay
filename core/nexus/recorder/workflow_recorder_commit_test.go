package recorder

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"centralrelay/core/cabinet"
	"centralrelay/core/nexus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type commitStateRecorder struct {
	commit string
	err    error
}

func (c *commitStateRecorder) RecordState(context.Context, types.ExpectedState, interface{}) (string, error) {
	return c.commit, c.err
}

// RecordCommit must hand the persisted record's commit hash to the caller.
func TestWorkflowRecorder_RecordCommit_ReturnsCommit(t *testing.T) {
	w := NewWorkflowRecorder(&commitStateRecorder{commit: "abc123"})
	commit, err := w.RecordCommit(context.Background(), "wf@1.0", map[string]string{"k": "v"})
	require.NoError(t, err)
	assert.Equal(t, "abc123", commit)
}

func TestWorkflowRecorder_RecordCommit_Error(t *testing.T) {
	w := NewWorkflowRecorder(&commitStateRecorder{err: errors.New("boom")})
	commit, err := w.RecordCommit(context.Background(), "wf@1.0", nil)
	require.Error(t, err)
	assert.Empty(t, commit)

	nilSR := NewWorkflowRecorder(nil)
	_, err = nilSR.RecordCommit(context.Background(), "wf@1.0", nil)
	require.Error(t, err)
}

// WorkflowRecorder must stay a cabinet.CommitRecorder: Setx only returns the commit
// hash for recorders that implement it. Removing RecordCommit fails the build here.
var (
	_ cabinet.ExecutionRecorder = (*WorkflowRecorder)(nil)
	_ cabinet.CommitRecorder    = (*WorkflowRecorder)(nil)
)

// With the real git recorder the returned commit is the actual new HEAD.
func TestWorkflowRecorder_RecordCommit_RealGitRecorder_ReturnsHead(t *testing.T) {
	repo := t.TempDir()
	sr, err := NewGitStateRecorder(repo, signingMock())
	require.NoError(t, err)
	w := NewWorkflowRecorder(sr)

	commit, err := w.RecordCommit(context.Background(), "wf@1.0", map[string]string{"k": "v"})
	require.NoError(t, err)

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(string(out)), commit)
	assert.Len(t, commit, 40)
}
