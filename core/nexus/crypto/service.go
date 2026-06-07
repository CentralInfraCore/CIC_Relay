package crypto

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/hashicorp/vault/api"
)

// CryptoService defines the interface for cryptographic operations like signing and decrypting.
type CryptoService interface {
	// Sign takes a data payload and returns a cryptographic signature.
	Sign(ctx context.Context, data []byte) (string, error)
	// Decrypt takes an encrypted payload and returns the plaintext.
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
	// HealthCheck verifies that the service can communicate with its backend (e.g., Vault).
	HealthCheck(ctx context.Context) error
}

// VaultCryptoService implements CryptoService using HashiCorp Vault's Transit engine.
type VaultCryptoService struct {
	client    *api.Client
	keyName   string
	mountPath string
}

// NewVaultCryptoService creates and configures a new service for signing.
// It reads Vault's address and token from standard environment variables (VAULT_ADDR, VAULT_TOKEN).
func NewVaultCryptoService(mountPath, keyName string) (*VaultCryptoService, error) {
	config := api.DefaultConfig()
	if err := config.ReadEnvironment(); err != nil {
		return nil, fmt.Errorf("failed to read vault config from environment: %w", err)
	}

	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create vault client: %w", err)
	}

	// Verify token is set
	if client.Token() == "" {
		return nil, fmt.Errorf("VAULT_TOKEN environment variable is not set")
	}

	return &VaultCryptoService{
		client:    client,
		keyName:   keyName,
		mountPath: mountPath,
	}, nil
}

// Sign uses the Vault Transit engine to sign the given data.
// It expects the data to be a hash (or raw data depending on Vault config),
// but typically for signing commits we sign the hash.
func (v *VaultCryptoService) Sign(ctx context.Context, data []byte) (string, error) {
	// Vault expects base64 encoded input
	inputBase64 := base64.StdEncoding.EncodeToString(data)

	path := fmt.Sprintf("%s/sign/%s", v.mountPath, v.keyName)
	secret, err := v.client.Logical().WriteWithContext(ctx, path, map[string]interface{}{
		"input": inputBase64,
		// "hash_algorithm": "sha2-256", // Optional, defaults to sha2-256 usually
	})
	if err != nil {
		return "", fmt.Errorf("vault sign request failed: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("vault returned empty response")
	}

	signature, ok := secret.Data["signature"].(string)
	if !ok {
		return "", fmt.Errorf("vault response missing 'signature' field")
	}

	return signature, nil
}

// Decrypt uses the Vault Transit engine to decrypt the given ciphertext.
// The ciphertext must be a valid Vault ciphertext string (e.g., "vault:v1:...").
func (v *VaultCryptoService) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	ciphertextStr := string(ciphertext)

	path := fmt.Sprintf("%s/decrypt/%s", v.mountPath, v.keyName)
	secret, err := v.client.Logical().WriteWithContext(ctx, path, map[string]interface{}{
		"ciphertext": ciphertextStr,
	})
	if err != nil {
		return nil, fmt.Errorf("vault decrypt request failed: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("vault returned empty response")
	}

	plaintextBase64, ok := secret.Data["plaintext"].(string)
	if !ok {
		return nil, fmt.Errorf("vault response missing 'plaintext' field")
	}

	plaintext, err := base64.StdEncoding.DecodeString(plaintextBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode plaintext from base64: %w", err)
	}

	return plaintext, nil
}

// HealthCheck pings the Vault server's health endpoint.
func (v *VaultCryptoService) HealthCheck(ctx context.Context) error {
	_, err := v.client.Sys().Health()
	if err != nil {
		return fmt.Errorf("vault health check failed: %w", err)
	}
	return nil
}
