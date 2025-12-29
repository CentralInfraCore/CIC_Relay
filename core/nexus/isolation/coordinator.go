package isolation

import (
	"context"
	"fmt"
)

// DecryptRequest represents a request to decrypt a payload.
type DecryptRequest struct {
	Payload []byte
	KeyID   string
}

// DecryptResponse represents the result of a decryption operation.
type DecryptResponse struct {
	Payload []byte
	Error   error
}

// Worker defines the interface for an isolated decryption component.
type Worker interface {
	Decrypt(ctx context.Context, req DecryptRequest) (*DecryptResponse, error)
}

// Coordinator manages the 3-step decryption process.
type Coordinator struct {
	HostWorker   Worker
	RelayWorker  Worker
	ModuleWorker Worker
}

// NewCoordinator creates a new Coordinator with the given workers.
func NewCoordinator(host, relay, module Worker) *Coordinator {
	return &Coordinator{
		HostWorker:   host,
		RelayWorker:  relay,
		ModuleWorker: module,
	}
}

// Unwrap performs the layered decryption: Relay -> Module -> Host.
// Note: The order depends on how the data was wrapped. Assuming standard onion routing:
// Outer layer = Relay, Middle = Module, Inner = Host.
func (c *Coordinator) Unwrap(ctx context.Context, encryptedData []byte) ([]byte, error) {
	// 1. Relay Unwrap
	relayResp, err := c.RelayWorker.Decrypt(ctx, DecryptRequest{Payload: encryptedData})
	if err != nil {
		return nil, fmt.Errorf("relay decryption failed: %w", err)
	}
	if relayResp.Error != nil {
		return nil, fmt.Errorf("relay worker error: %w", relayResp.Error)
	}

	// 2. Module Unwrap
	moduleResp, err := c.ModuleWorker.Decrypt(ctx, DecryptRequest{Payload: relayResp.Payload})
	if err != nil {
		return nil, fmt.Errorf("module decryption failed: %w", err)
	}
	if moduleResp.Error != nil {
		return nil, fmt.Errorf("module worker error: %w", moduleResp.Error)
	}

	// 3. Host Unwrap
	hostResp, err := c.HostWorker.Decrypt(ctx, DecryptRequest{Payload: moduleResp.Payload})
	if err != nil {
		return nil, fmt.Errorf("host decryption failed: %w", err)
	}
	if hostResp.Error != nil {
		return nil, fmt.Errorf("host worker error: %w", hostResp.Error)
	}

	return hostResp.Payload, nil
}
