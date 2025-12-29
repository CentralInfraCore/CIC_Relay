package isolation

import (
	"context"
	"fmt"

	"centralrelay/core/nexus/crypto"
)

// LocalWorker is a simple in-process implementation of the Worker interface.
// It uses a CryptoService to perform the actual decryption.
type LocalWorker struct {
	CryptoService crypto.CryptoService
}

// NewLocalWorker creates a new worker that uses the given CryptoService.
func NewLocalWorker(svc crypto.CryptoService) *LocalWorker {
	return &LocalWorker{
		CryptoService: svc,
	}
}

// Decrypt calls the underlying CryptoService's Decrypt method.
// In a real implementation, this would run in a separate process with its own identity.
func (w *LocalWorker) Decrypt(ctx context.Context, req DecryptRequest) (*DecryptResponse, error) {
	if w.CryptoService == nil {
		return &DecryptResponse{Error: fmt.Errorf("cryptoservice not configured for this worker")}, nil
	}

	// In a real scenario, the CryptoService would be configured with a specific key
	// for this worker (host, relay, or module).
	decryptedPayload, err := w.CryptoService.Decrypt(ctx, req.Payload)
	if err != nil {
		// Wrap the error in a DecryptResponse so the coordinator can handle it.
		return &DecryptResponse{Error: fmt.Errorf("decryption failed: %w", err)}, nil
	}

	return &DecryptResponse{
		Payload: decryptedPayload,
		Error:   nil,
	}, nil
}
