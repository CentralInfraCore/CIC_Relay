package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero/api"

	"centralrelay/pkg/hostsign"
	"centralrelay/pkg/hostsign/ociprofile"
)

// cic-flow.send (O-1 WP-04, contract 03-sign-send-contract.md). The guest describes the request
// structurally; everything security-relevant happens here, on the host side, in the fixed order
// implemented by pkg/hostsign.Engine. This file is the composition root: it is the only place
// that joins the generic seam, the provider profile (ociprofile), the Vault signer and the
// per-module manifest.

// sendConfig is the OPERATOR configuration of cic-flow.send. It is not part of any module artifact
// and not guest-supplied; without it a module that imports cic-flow.send is rejected at load.
type sendConfig struct {
	policy   hostsign.Policy
	profiles []hostsign.Profile
	// keyID is the provider key identifier the signing profile puts into the Authorization header
	// (OCI: <tenancy OCID>/<user OCID>/<fingerprint>). The guest names neither key nor identifier.
	keyID string
}

// loadSendConfig reads the operator deployment policy (JSON, hostsign.Policy) and validates it.
func loadSendConfig(policyPath, keyID string) (*sendConfig, error) {
	raw, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, fmt.Errorf("cic-flow.send policy: %w", err)
	}
	var p hostsign.Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("cic-flow.send policy: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("cic-flow.send policy: %w", err)
	}
	if keyID == "" {
		return nil, fmt.Errorf("cic-flow.send policy: CIC_OCI_SIGN_KEY_ID is required with CIC_HOSTSIGN_POLICY")
	}
	return &sendConfig{policy: p, profiles: []hostsign.Profile{ociprofile.New()}, keyID: keyID}, nil
}

// sendConfigFromEnv returns nil (send not configured) when CIC_HOSTSIGN_POLICY is unset.
func sendConfigFromEnv() (*sendConfig, error) {
	path := os.Getenv("CIC_HOSTSIGN_POLICY")
	if path == "" {
		return nil, nil
	}
	return loadSendConfig(path, os.Getenv("CIC_OCI_SIGN_KEY_ID"))
}

// configureSend installs the operator configuration; call once at startup.
func (r *cicFlowRegistry) configureSend(cfg *sendConfig) {
	r.mu.Lock()
	r.send = cfg
	r.mu.Unlock()
}

// handleMatches: a manifest secret handle matches a key_ref exactly or as "<prefix>/*".
func handleMatches(handle, ref string) bool {
	if handle == ref {
		return true
	}
	if prefix, ok := strings.CutSuffix(handle, "/*"); ok {
		rest, found := strings.CutPrefix(ref, prefix+"/")
		return found && rest != "" && !strings.Contains(rest, "/")
	}
	return false
}

// bindSend builds the per-module send engine from the operator config and the module's verified
// manifest. The module's manifest decides WHICH key refs it may use and which egress it has; the
// operator policy decides what may be sent where.
func (r *cicFlowRegistry) bindSend(m capabilityManifest, projectYAML []byte) (*hostsign.Engine, error) {
	r.mu.RLock()
	cfg := r.send
	r.mu.RUnlock()
	if cfg == nil {
		return nil, fmt.Errorf("module imports cic-flow.send but the operator configured no cic-flow.send policy (CIC_HOSTSIGN_POLICY)")
	}
	var mu sync.Mutex
	cache := map[string]hostsign.Signer{}
	keys := func(ref string) (hostsign.Key, bool) {
		allowed := false
		for _, s := range m.Secrets {
			if containsFold(s.Ops, "sign") && handleMatches(s.Handle, ref) {
				allowed = true
			}
		}
		if !allowed || r.resolve == nil {
			return hostsign.Key{}, false
		}
		mu.Lock()
		defer mu.Unlock()
		sg, ok := cache[ref]
		if !ok {
			s, err := r.resolve(ref)
			if err != nil {
				return hostsign.Key{}, false
			}
			sg, cache[ref] = s, s
		}
		return hostsign.Key{Signer: sg, KeyID: cfg.keyID}, true
	}
	sum := sha256.Sum256(projectYAML)
	return hostsign.NewEngine(hostsign.Config{
		Policy:   cfg.policy,
		Profiles: cfg.profiles,
		Keys:     keys,
		Egress: func(method, host string, port int, path string) bool {
			return m.Egress.allows(method, host, port, path)
		},
		Doer: r.doer,
		// INTERIM: the digest of the verified project.yaml stands in for the ABI profile digest until the
		// WP-03 profile artifact exists (recorded as profile_digest).
		ProfileDigest: "sha256:" + hex.EncodeToString(sum[:]),
	})
}

// makeSendFn is the cic-flow.send host function: the guest's structured JSON request is handled by
// the module's engine; the answer is the guest-visible response (never a signature or Authorization).
func makeSendFn(deps cicFlowDeps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		if deps.send == nil {
			return gitWriteError(mod, outPtr, outLen, "cic-flow.send: not granted to this module")
		}
		view, ok := mod.Memory().Read(reqPtr, reqLen)
		if !ok {
			return gitWriteError(mod, outPtr, outLen, "bad request memory")
		}
		raw := append([]byte(nil), view...)
		out := deps.send.Handle(ctx, raw)
		return gitWriteJSON(mod, outPtr, outLen, json.RawMessage(out))
	}
}
