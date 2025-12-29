package recorder

import (
	"context"
	"encoding/json"
	"math"
	"os"
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
