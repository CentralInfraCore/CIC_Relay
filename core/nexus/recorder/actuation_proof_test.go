package recorder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"centralrelay/core/cabinet"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestActuationProof_PersistsDurably proves the recorder span of the feature-009
// audit bridge: an http-executor actuation, projected into a cabinet.ActuationProof
// (Go ProofTrace + Rust record_hash anchor), is persisted through the real
// WorkflowRecorder → GitStateRecorder as a Vault-signed Git commit.
func TestActuationProof_PersistsDurably(t *testing.T) {
	tmpDir := t.TempDir()
	mockCrypto := &MockCryptoService{}
	sr, err := NewGitStateRecorder(tmpDir, mockCrypto)
	require.NoError(t, err)
	wr := NewWorkflowRecorder(sr)

	// An actuation's audit crosses the FFI seam and becomes a durable proof.
	step := cabinet.ProofTraceStepFromActuation(
		"trace-persist-1", "GET", "127.0.0.1",
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	)
	proof := cabinet.ProofFromActuation(
		"cap-http-flow", "src-rev-abc", "2026-07-13T00:00:00Z",
		"3b1f9c0a7e5d2b48f6a1c3e59d7042b8c1f6a9d3e0b4278c5a1f9e3d6b0c2748a",
		step,
	)

	require.NoError(t, wr.Record(context.Background(), proof.ProofTrace.WorkflowID, proof))

	// The persisted "actual" state carries both chains: the Go ProofTrace ChainHash
	// and the Rust record_hash anchor.
	actual, err := os.ReadFile(filepath.Join(tmpDir, "actual.json"))
	require.NoError(t, err)
	var persisted cabinet.ActuationProof
	require.NoError(t, json.Unmarshal(actual, &persisted))
	require.NotNil(t, persisted.ProofTrace)
	assert.Equal(t, proof.ProofTrace.ChainHash, persisted.ProofTrace.ChainHash)
	assert.Equal(t, proof.RecordHash, persisted.RecordHash)
	require.Len(t, persisted.ProofTrace.Steps, 1)
	assert.Equal(t, "http-executor", persisted.ProofTrace.Steps[0].Module)
	assert.Equal(t, "trace-persist-1", persisted.ProofTrace.Steps[0].Name)

	// The manifest hash was signed (Vault-signed provenance).
	sig, err := os.ReadFile(filepath.Join(tmpDir, "signature.meta"))
	require.NoError(t, err)
	var sigData map[string]string
	require.NoError(t, json.Unmarshal(sig, &sigData))
	assert.Equal(t, "mock-signature-123", sigData["signature"])
}
