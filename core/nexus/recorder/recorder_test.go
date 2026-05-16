package recorder

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"centralrelay/core/nexus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockCryptoService implements CryptoService for testing.
type MockCryptoService struct {
	SignFunc func(ctx context.Context, data []byte) (string, error)
}

func (m *MockCryptoService) Sign(ctx context.Context, data []byte) (string, error) {
	if m.SignFunc != nil {
		return m.SignFunc(ctx, data)
	}
	return "mock-signature-123", nil
}

func (m *MockCryptoService) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	return ciphertext, nil
}

func (m *MockCryptoService) HealthCheck(ctx context.Context) error {
	return nil
}

func TestGitStateRecorder_RecordState_Success(t *testing.T) {
	tmpDir := t.TempDir()
	mockCrypto := &MockCryptoService{}
	recorder, err := NewGitStateRecorder(tmpDir, mockCrypto)
	require.NoError(t, err)

	expectedState := types.ExpectedState{
		RunID:     "test-run-001",
		Timestamp: time.Now(),
	}
	actualState := map[string]string{"status": "success"}

	commitHash, err := recorder.RecordState(context.Background(), expectedState, actualState)
	require.NoError(t, err)
	assert.NotEmpty(t, commitHash)

	// Verify files and signature
	sigPath := filepath.Join(tmpDir, "signature.meta")
	sigContent, err := os.ReadFile(sigPath)
	require.NoError(t, err)
	var sigData map[string]string
	require.NoError(t, json.Unmarshal(sigContent, &sigData))
	assert.Equal(t, "mock-signature-123", sigData["signature"])
}

// --- WorkflowRecorder tests ---

func TestWorkflowRecorder_Record_Success(t *testing.T) {
	tmpDir := t.TempDir()
	mockCrypto := &MockCryptoService{}
	sr, err := NewGitStateRecorder(tmpDir, mockCrypto)
	require.NoError(t, err)

	wr := NewWorkflowRecorder(sr)

	trace := map[string]interface{}{
		"workflow_id": "deploy@1.0",
		"chain_hash":  "abc123",
	}
	err = wr.Record(context.Background(), "deploy@1.0", trace)
	require.NoError(t, err)

	// actual.json should contain the trace
	actual, err := os.ReadFile(filepath.Join(tmpDir, "actual.json"))
	require.NoError(t, err)
	assert.Contains(t, string(actual), "chain_hash")
}

func TestWorkflowRecorder_Record_NilStateRecorder(t *testing.T) {
	wr := NewWorkflowRecorder(nil)
	err := wr.Record(context.Background(), "wf", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "underlying StateRecorder is nil")
}

func TestWorkflowRecorder_Record_VaultUnavailable(t *testing.T) {
	// Vault (CryptoService) fails → recorder returns error → caller should WARN and continue.
	tmpDir := t.TempDir()
	mockCrypto := &MockCryptoService{
		SignFunc: func(ctx context.Context, data []byte) (string, error) {
			return "", errors.New("vault unavailable")
		},
	}
	sr, err := NewGitStateRecorder(tmpDir, mockCrypto)
	require.NoError(t, err)

	wr := NewWorkflowRecorder(sr)
	err = wr.Record(context.Background(), "deploy@1.0", map[string]interface{}{"chain_hash": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vault unavailable")
}

func TestWorkflowRecorder_RunID_Is_WorkflowID(t *testing.T) {
	var capturedExpected types.ExpectedState
	mockSR := &mockStateRecorder{
		fn: func(ctx context.Context, expected types.ExpectedState, actual interface{}) (string, error) {
			capturedExpected = expected
			return "hash", nil
		},
	}

	wr := NewWorkflowRecorder(mockSR)
	err := wr.Record(context.Background(), "myworkflow@2.0", nil)
	require.NoError(t, err)
	assert.Equal(t, "myworkflow@2.0", capturedExpected.RunID)
}

// mockStateRecorder is a test double for StateRecorder.
type mockStateRecorder struct {
	fn func(ctx context.Context, expected types.ExpectedState, actual interface{}) (string, error)
}

func (m *mockStateRecorder) RecordState(ctx context.Context, expected types.ExpectedState, actual interface{}) (string, error) {
	return m.fn(ctx, expected, actual)
}

// --- GitStateRecorder error tests ---

func TestGitStateRecorder_RecordState_Errors(t *testing.T) {
	t.Run("init fails on bad path", func(t *testing.T) {
		// Use a path that cannot be created
		_, err := NewGitStateRecorder("/proc/invalid-path", &MockCryptoService{})
		assert.Error(t, err)
	})

	t.Run("crypto service not configured", func(t *testing.T) {
		tmpDir := t.TempDir()
		recorder, err := NewGitStateRecorder(tmpDir, nil) // Pass nil crypto service
		require.NoError(t, err)
		_, err = recorder.RecordState(context.Background(), types.ExpectedState{}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cryptoservice is not configured")
	})

	t.Run("signing fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		mockCrypto := &MockCryptoService{
			SignFunc: func(ctx context.Context, data []byte) (string, error) {
				return "", assert.AnError
			},
		}
		recorder, err := NewGitStateRecorder(tmpDir, mockCrypto)
		require.NoError(t, err)
		_, err = recorder.RecordState(context.Background(), types.ExpectedState{}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to sign manifest")
	})

	t.Run("git command fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		recorder, err := NewGitStateRecorder(tmpDir, &MockCryptoService{})
		require.NoError(t, err)

		// Make git fail by removing the .git directory
		err = os.RemoveAll(filepath.Join(tmpDir, ".git"))
		require.NoError(t, err)

		_, err = recorder.RecordState(context.Background(), types.ExpectedState{}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "git add . failed")
	})

	t.Run("json marshal fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		recorder, err := NewGitStateRecorder(tmpDir, &MockCryptoService{})
		require.NoError(t, err)

		// Pass a value that cannot be marshaled (e.g., channel or function)
		// But interface{} can hold anything.
		// Let's try passing a float infinity which JSON doesn't support
		actual := map[string]interface{}{
			"bad": math.Inf(1),
		}
		_, err = recorder.RecordState(context.Background(), types.ExpectedState{}, actual)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to marshal actual state")
	})
}

func TestGitStateRecorder_GetHeadHash_NoCommits(t *testing.T) {
	// Create a bare git repo with no commits → getHeadHash fails.
	tmpDir := t.TempDir()
	cmd := exec.Command("git", "init", tmpDir)
	require.NoError(t, cmd.Run())

	r := &GitStateRecorder{RepoPath: tmpDir}
	_, err := r.getHeadHash()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get HEAD hash")
}

func TestGitStateRecorder_Init_AlreadyInitialized(t *testing.T) {
	// Second call to init() when .git already exists → early return (no error).
	tmpDir := t.TempDir()
	r := &GitStateRecorder{RepoPath: tmpDir, CryptoService: &MockCryptoService{}}
	// First call: creates .git, configures user, creates initial commit.
	require.NoError(t, r.init())
	// Second call: .git exists → skip initialization.
	require.NoError(t, r.init())
}
