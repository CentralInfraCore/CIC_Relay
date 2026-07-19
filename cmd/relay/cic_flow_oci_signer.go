package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"centralrelay/core/nexus/crypto"
)

// OCI-key wiring for cic-flow.sign (feature-011, r3-plan.md decision 3). The OCI
// cloud key is relay-managed, not shipped with the module: it is obtained and
// stored in Vault out-of-band, and a module's `oci/...` manifest handle is a
// logical reference the relay maps to that provisioned Vault key. The module
// never names key material — only the handle.

// ociVaultSigner adapts VaultCryptoService.SignOCI (pkcs1v15 / sha2-256, raw
// base64) to the cic-flow signer interface. The OCI API key never leaves Vault.
type ociVaultSigner struct{ svc *crypto.VaultCryptoService }

func (s ociVaultSigner) Sign(ctx context.Context, data []byte) (string, error) {
	return s.svc.SignOCI(ctx, data)
}

// buildOCIKeyResolver returns a keyResolver that maps a module's `oci/...` secret
// handle to a SignOCI signer over the relay-provisioned Vault Transit key, when
// the OCI signing key is configured (CIC_OCI_SIGN_MOUNT + CIC_OCI_SIGN_KEY).
// Returns nil when unconfigured, leaving cic-flow.sign inert (fail-closed) until
// the key is provisioned.
func buildOCIKeyResolver() keyResolver {
	mount := os.Getenv("CIC_OCI_SIGN_MOUNT")
	keyName := os.Getenv("CIC_OCI_SIGN_KEY")
	if mount == "" || keyName == "" {
		return nil
	}
	return func(handle string) (signer, error) {
		if !strings.HasPrefix(handle, "oci/") {
			return nil, fmt.Errorf("cic-flow: unsupported secret handle %q (expected oci/...)", handle)
		}
		svc, err := crypto.NewVaultCryptoService(mount, keyName)
		if err != nil {
			return nil, fmt.Errorf("cic-flow: build OCI signer: %w", err)
		}
		return ociVaultSigner{svc: svc}, nil
	}
}
