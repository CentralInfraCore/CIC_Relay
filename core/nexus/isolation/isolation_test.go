package isolation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockWorker is a mock implementation of the Worker interface.
type MockWorker struct {
	mock.Mock
}

func (m *MockWorker) Decrypt(ctx context.Context, req DecryptRequest) (*DecryptResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*DecryptResponse), args.Error(1)
}

// MockCryptoService for LocalWorker testing
type MockCryptoService struct {
	mock.Mock
}

func (m *MockCryptoService) Sign(ctx context.Context, data []byte) (string, error) {
	args := m.Called(ctx, data)
	return args.String(0), args.Error(1)
}

func (m *MockCryptoService) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	args := m.Called(ctx, ciphertext)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockCryptoService) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestCoordinator_Unwrap_Success(t *testing.T) {
	hostWorker := new(MockWorker)
	relayWorker := new(MockWorker)
	moduleWorker := new(MockWorker)

	coordinator := NewCoordinator(hostWorker, relayWorker, moduleWorker)

	input := []byte("encrypted")
	relayOutput := []byte("relay_decrypted")
	moduleOutput := []byte("module_decrypted")
	hostOutput := []byte("host_decrypted")

	// Expectation: Relay -> Module -> Host
	relayWorker.On("Decrypt", mock.Anything, DecryptRequest{Payload: input}).Return(&DecryptResponse{Payload: relayOutput}, nil)
	moduleWorker.On("Decrypt", mock.Anything, DecryptRequest{Payload: relayOutput}).Return(&DecryptResponse{Payload: moduleOutput}, nil)
	hostWorker.On("Decrypt", mock.Anything, DecryptRequest{Payload: moduleOutput}).Return(&DecryptResponse{Payload: hostOutput}, nil)

	result, err := coordinator.Unwrap(context.Background(), input)
	assert.NoError(t, err)
	assert.Equal(t, hostOutput, result)

	hostWorker.AssertExpectations(t)
	relayWorker.AssertExpectations(t)
	moduleWorker.AssertExpectations(t)
}

func TestCoordinator_Unwrap_Errors(t *testing.T) {
	t.Run("relay error", func(t *testing.T) {
		hostWorker := new(MockWorker)
		relayWorker := new(MockWorker)
		moduleWorker := new(MockWorker)
		coordinator := NewCoordinator(hostWorker, relayWorker, moduleWorker)

		relayWorker.On("Decrypt", mock.Anything, mock.Anything).Return(nil, errors.New("relay fail"))

		_, err := coordinator.Unwrap(context.Background(), []byte("data"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "relay decryption failed")
	})

	t.Run("module error", func(t *testing.T) {
		hostWorker := new(MockWorker)
		relayWorker := new(MockWorker)
		moduleWorker := new(MockWorker)
		coordinator := NewCoordinator(hostWorker, relayWorker, moduleWorker)

		relayWorker.On("Decrypt", mock.Anything, mock.Anything).Return(&DecryptResponse{Payload: []byte("ok")}, nil)
		moduleWorker.On("Decrypt", mock.Anything, mock.Anything).Return(nil, errors.New("module fail"))

		_, err := coordinator.Unwrap(context.Background(), []byte("data"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "module decryption failed")
	})
}

func TestCoordinator_Unwrap_WorkerResponseErrors(t *testing.T) {
	t.Run("relay response error", func(t *testing.T) {
		hostWorker := new(MockWorker)
		relayWorker := new(MockWorker)
		moduleWorker := new(MockWorker)
		coordinator := NewCoordinator(hostWorker, relayWorker, moduleWorker)

		relayWorker.On("Decrypt", mock.Anything, mock.Anything).
			Return(&DecryptResponse{Error: errors.New("relay payload corrupted")}, nil)

		_, err := coordinator.Unwrap(context.Background(), []byte("data"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "relay worker error")
	})

	t.Run("module response error", func(t *testing.T) {
		hostWorker := new(MockWorker)
		relayWorker := new(MockWorker)
		moduleWorker := new(MockWorker)
		coordinator := NewCoordinator(hostWorker, relayWorker, moduleWorker)

		relayWorker.On("Decrypt", mock.Anything, mock.Anything).
			Return(&DecryptResponse{Payload: []byte("ok")}, nil)
		moduleWorker.On("Decrypt", mock.Anything, mock.Anything).
			Return(&DecryptResponse{Error: errors.New("module payload corrupted")}, nil)

		_, err := coordinator.Unwrap(context.Background(), []byte("data"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "module worker error")
	})

	t.Run("host decryption transport error", func(t *testing.T) {
		hostWorker := new(MockWorker)
		relayWorker := new(MockWorker)
		moduleWorker := new(MockWorker)
		coordinator := NewCoordinator(hostWorker, relayWorker, moduleWorker)

		relayWorker.On("Decrypt", mock.Anything, mock.Anything).
			Return(&DecryptResponse{Payload: []byte("ok")}, nil)
		moduleWorker.On("Decrypt", mock.Anything, mock.Anything).
			Return(&DecryptResponse{Payload: []byte("ok")}, nil)
		hostWorker.On("Decrypt", mock.Anything, mock.Anything).
			Return(nil, errors.New("host fail"))

		_, err := coordinator.Unwrap(context.Background(), []byte("data"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "host decryption failed")
	})

	t.Run("host response error", func(t *testing.T) {
		hostWorker := new(MockWorker)
		relayWorker := new(MockWorker)
		moduleWorker := new(MockWorker)
		coordinator := NewCoordinator(hostWorker, relayWorker, moduleWorker)

		relayWorker.On("Decrypt", mock.Anything, mock.Anything).
			Return(&DecryptResponse{Payload: []byte("ok")}, nil)
		moduleWorker.On("Decrypt", mock.Anything, mock.Anything).
			Return(&DecryptResponse{Payload: []byte("ok")}, nil)
		hostWorker.On("Decrypt", mock.Anything, mock.Anything).
			Return(&DecryptResponse{Error: errors.New("host payload corrupted")}, nil)

		_, err := coordinator.Unwrap(context.Background(), []byte("data"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "host worker error")
	})
}

func TestLocalWorker_Decrypt(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockCrypto := new(MockCryptoService)
		worker := NewLocalWorker(mockCrypto)

		input := []byte("ciphertext")
		output := []byte("plaintext")

		mockCrypto.On("Decrypt", mock.Anything, input).Return(output, nil)

		resp, err := worker.Decrypt(context.Background(), DecryptRequest{Payload: input})
		assert.NoError(t, err)
		assert.Nil(t, resp.Error)
		assert.Equal(t, output, resp.Payload)
	})

	t.Run("crypto error", func(t *testing.T) {
		mockCrypto := new(MockCryptoService)
		worker := NewLocalWorker(mockCrypto)

		mockCrypto.On("Decrypt", mock.Anything, mock.Anything).Return(nil, errors.New("decrypt fail"))

		resp, err := worker.Decrypt(context.Background(), DecryptRequest{Payload: []byte("data")})
		assert.NoError(t, err) // Worker itself didn't fail, but returned an error response
		assert.Error(t, resp.Error)
		assert.Contains(t, resp.Error.Error(), "decrypt fail")
	})

	t.Run("missing crypto service", func(t *testing.T) {
		worker := NewLocalWorker(nil)
		resp, err := worker.Decrypt(context.Background(), DecryptRequest{})
		assert.NoError(t, err)
		assert.Error(t, resp.Error)
		assert.Contains(t, resp.Error.Error(), "cryptoservice not configured")
	})
}

func TestNewSubprocessWorker_Success(t *testing.T) {
	w, err := NewSubprocessWorker()
	require.NoError(t, err)
	require.NotNil(t, w)
}

func TestSubprocessWorker_Decrypt_BadExecutable(t *testing.T) {
	w := newSubprocessWorkerWithExe("/nonexistent-binary-xyz", nil, nil)
	_, err := w.Decrypt(context.Background(), DecryptRequest{Payload: []byte("data")})
	require.Error(t, err)
}

func TestSubprocessWorker_Decrypt_NoResponse(t *testing.T) {
	w := newSubprocessWorkerWithExe("true", nil, nil)
	_, err := w.Decrypt(context.Background(), DecryptRequest{Payload: []byte("data")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no response")
}

func TestSubprocessWorker_Decrypt_BadJSON(t *testing.T) {
	// read drains the JSON request from stdin before responding, avoiding broken-pipe race.
	w := newSubprocessWorkerWithExe("sh", []string{"-c", `read line; echo "not-json"`}, nil)
	_, err := w.Decrypt(context.Background(), DecryptRequest{Payload: []byte("data")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse subprocess response")
}

func TestSubprocessWorker_Decrypt_WorkerError(t *testing.T) {
	// read drains the JSON request from stdin before responding, avoiding broken-pipe race.
	script := `read line; echo '{"error":"worker failed"}'`
	w := newSubprocessWorkerWithExe("sh", []string{"-c", script}, nil)
	resp, err := w.Decrypt(context.Background(), DecryptRequest{Payload: []byte("data")})
	require.NoError(t, err)
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Error(), "worker failed")
}

func TestSubprocessWorker_Decrypt_WithEnv(t *testing.T) {
	// Covers the w.env != nil branch: the env slice is passed to the subprocess.
	// read drains stdin before responding to avoid broken-pipe race under -race.
	script := `read line; echo '{"result":"dGVzdA=="}'`
	w := newSubprocessWorkerWithExe("sh", []string{"-c", script}, []string{"PATH=/usr/bin:/bin"})
	resp, err := w.Decrypt(context.Background(), DecryptRequest{Payload: []byte("data")})
	require.NoError(t, err)
	assert.Nil(t, resp.Error)
}
