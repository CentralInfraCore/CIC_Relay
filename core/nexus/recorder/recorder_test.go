package recorder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"centralrelay/core/nexus/drift"
	"centralrelay/core/nexus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockCryptoService implements CryptoService for testing.
type MockCryptoService struct {
	SignFunc   func(ctx context.Context, data []byte) (string, error)
	VerifyFunc func(ctx context.Context, data []byte, signature string) (bool, error)
}

func (m *MockCryptoService) Sign(ctx context.Context, data []byte) (string, error) {
	if m.SignFunc != nil {
		return m.SignFunc(ctx, data)
	}
	return "mock-signature-123", nil
}

func (m *MockCryptoService) Verify(ctx context.Context, data []byte, signature string) (bool, error) {
	if m.VerifyFunc != nil {
		return m.VerifyFunc(ctx, data, signature)
	}
	return signature == "mock-signature-123", nil
}

func (m *MockCryptoService) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	return ciphertext, nil
}

func (m *MockCryptoService) HealthCheck(ctx context.Context) error {
	return nil
}

// signingMock returns a mock whose signature deterministically binds the signed
// data, so a real content tamper (which changes the manifest hash) makes Verify
// fail — the property VerifyChain relies on.
func signingMock() *MockCryptoService {
	return &MockCryptoService{
		SignFunc: func(_ context.Context, data []byte) (string, error) {
			return "sig:" + hex.EncodeToString(data), nil
		},
		VerifyFunc: func(_ context.Context, data []byte, signature string) (bool, error) {
			return signature == "sig:"+hex.EncodeToString(data), nil
		},
	}
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

func TestGitStateRecorder_RecordState_WriteFileFails(t *testing.T) {
	tmpDir := t.TempDir()
	recorder, err := NewGitStateRecorder(tmpDir, &MockCryptoService{})
	require.NoError(t, err)

	// Create a DIRECTORY named "expected.json" so os.WriteFile into it fails.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "expected.json"), 0o755))

	_, err = recorder.RecordState(context.Background(), types.ExpectedState{RunID: "r1"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write")
}

// craftCommit hand-builds a signed state commit with a controllable parent, to
// exercise VerifyChain's chain-linkage check independently of RecordState.
func craftCommit(t *testing.T, r *GitStateRecorder, m *MockCryptoService, parent string) {
	t.Helper()
	files := map[string][]byte{
		"expected.json": []byte(`{"RunID":"crafted"}`),
		"actual.json":   []byte(`{"ok":true}`),
	}
	metaBytes, err := json.MarshalIndent(map[string]string{"run_id": "crafted", "parent": parent}, "", "  ")
	require.NoError(t, err)
	files["meta.json"] = metaBytes

	manifest, err := r.createManifest(files)
	require.NoError(t, err)
	mb, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)
	h := sha256.Sum256(mb)
	sig, err := m.Sign(context.Background(), h[:])
	require.NoError(t, err)
	sigBytes, err := json.MarshalIndent(map[string]string{"signature": sig}, "", "  ")
	require.NoError(t, err)
	files["signature.meta"] = sigBytes

	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(r.RepoPath, name), content, 0644))
	}
	require.NoError(t, r.runGit("add", "."))
	require.NoError(t, r.runGit("commit", "-m", "crafted"))
}

func TestVerifyChain_ValidChainVerifies(t *testing.T) {
	ctx := context.Background()
	r, err := NewGitStateRecorder(t.TempDir(), signingMock())
	require.NoError(t, err)

	// A chain of three recorded state transitions.
	for i, run := range []string{"run-1", "run-2", "run-3"} {
		_, err := r.RecordState(ctx, types.ExpectedState{RunID: run}, map[string]int{"step": i})
		require.NoError(t, err)
	}

	require.NoError(t, r.VerifyChain(ctx), "a well-formed signed chain must verify")
}

func TestVerifyChain_DetectsContentTampering(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	r, err := NewGitStateRecorder(tmp, signingMock())
	require.NoError(t, err)

	_, err = r.RecordState(ctx, types.ExpectedState{RunID: "run-1"}, map[string]string{"a": "1"})
	require.NoError(t, err)

	// Tamper: commit an altered expected.json while the (now stale) signature.meta
	// stays. The Vault signature no longer matches the manifest.
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "expected.json"), []byte(`{"RunID":"TAMPERED"}`), 0644))
	require.NoError(t, r.runGit("add", "expected.json"))
	require.NoError(t, r.runGit("commit", "-m", "tamper"))

	err = r.VerifyChain(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifyChain_DetectsBrokenChain(t *testing.T) {
	ctx := context.Background()
	m := signingMock()
	r, err := NewGitStateRecorder(t.TempDir(), m)
	require.NoError(t, err)

	_, err = r.RecordState(ctx, types.ExpectedState{RunID: "run-1"}, nil)
	require.NoError(t, err)

	// A validly-signed commit whose signed parent points at the wrong commit.
	craftCommit(t, r, m, "0000000000000000000000000000000000000000")

	err = r.VerifyChain(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chain broken")
}

func TestVerifyChain_DetectsInvalidSignature(t *testing.T) {
	ctx := context.Background()
	// Signer that always reports invalid on verify.
	m := &MockCryptoService{
		SignFunc:   func(_ context.Context, _ []byte) (string, error) { return "whatever", nil },
		VerifyFunc: func(_ context.Context, _ []byte, _ string) (bool, error) { return false, nil },
	}
	r, err := NewGitStateRecorder(t.TempDir(), m)
	require.NoError(t, err)
	_, err = r.RecordState(ctx, types.ExpectedState{RunID: "run-1"}, nil)
	require.NoError(t, err)

	err = r.VerifyChain(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifyChain_NoSignedCommits(t *testing.T) {
	// A fresh repo has only the .gitignore init commit — nothing signed to verify.
	r, err := NewGitStateRecorder(t.TempDir(), signingMock())
	require.NoError(t, err)
	err = r.VerifyChain(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no signed state commits")
}

func TestVerifyChain_NilCryptoService(t *testing.T) {
	r := &GitStateRecorder{RepoPath: t.TempDir()}
	err := r.VerifyChain(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cryptoservice is not configured")
}

func TestVerifyChain_VerifyBackendError(t *testing.T) {
	ctx := context.Background()
	m := &MockCryptoService{
		SignFunc:   func(_ context.Context, _ []byte) (string, error) { return "s", nil },
		VerifyFunc: func(_ context.Context, _ []byte, _ string) (bool, error) { return false, errors.New("vault down") },
	}
	r, err := NewGitStateRecorder(t.TempDir(), m)
	require.NoError(t, err)
	_, err = r.RecordState(ctx, types.ExpectedState{RunID: "r1"}, nil)
	require.NoError(t, err)

	err = r.VerifyChain(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verify")
}

func TestVerifyChain_CorruptSignatureMeta(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	r, err := NewGitStateRecorder(tmp, signingMock())
	require.NoError(t, err)
	_, err = r.RecordState(ctx, types.ExpectedState{RunID: "r1"}, nil)
	require.NoError(t, err)

	// Commit a signature.meta that is not valid JSON.
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "signature.meta"), []byte("not json"), 0644))
	require.NoError(t, r.runGit("add", "signature.meta"))
	require.NoError(t, r.runGit("commit", "-m", "corrupt"))

	err = r.VerifyChain(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature.meta")
}

func TestRecordDrift_ClassifiesSignsAndVerifies(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	r, err := NewGitStateRecorder(tmp, signingMock())
	require.NoError(t, err)

	desired := map[string]interface{}{"network": "10.0.0.0/24", "vms": "3"}

	// In sync → NONE.
	_, rep, err := r.RecordDrift(ctx, "sync", desired, desired)
	require.NoError(t, err)
	assert.Equal(t, drift.None, rep.Type)

	// A changed value → RECONCILIABLE_DRIFT, reported precisely.
	drifted := map[string]interface{}{"network": "10.0.9.9/24", "vms": "3"}
	_, rep, err = r.RecordDrift(ctx, "drifted", desired, drifted)
	require.NoError(t, err)
	assert.Equal(t, drift.ReconciliableDrift, rep.Type)
	assert.Equal(t, []string{"network"}, rep.Changed)

	// Deleted infra → HARD_DRIFT, and the verdict is persisted in the signed meta.
	_, rep, err = r.RecordDrift(ctx, "deleted", desired, map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, drift.HardDrift, rep.Type)

	metaRaw, err := os.ReadFile(filepath.Join(tmp, "meta.json"))
	require.NoError(t, err)
	assert.Contains(t, string(metaRaw), "HARD_DRIFT")

	// The whole chain — including the drift commits — still verifies (same layout).
	require.NoError(t, r.VerifyChain(ctx))
}

func TestRecordDrift_MixedWithRecordStateVerifies(t *testing.T) {
	ctx := context.Background()
	r, err := NewGitStateRecorder(t.TempDir(), signingMock())
	require.NoError(t, err)

	_, err = r.RecordState(ctx, types.ExpectedState{RunID: "boot"}, map[string]string{"ok": "1"})
	require.NoError(t, err)
	_, _, err = r.RecordDrift(ctx, "observe", map[string]interface{}{"a": "1"}, map[string]interface{}{"a": "1", "b": "2"})
	require.NoError(t, err)

	require.NoError(t, r.VerifyChain(ctx), "a chain mixing RecordState and RecordDrift must verify")
}
