// Command statechain-demo is a live, self-contained demonstration of the
// feature-010 signed state chain (§8.1 + §8.5 of the demo plan): it records a
// few infrastructure-state transitions into a git repo, each committed with a
// Vault-Transit signature over a manifest that binds the parent commit, then
// verifies the whole chain — the signing key never leaves Vault. Finally it
// tampers a committed state and re-verifies, showing the chain is tamper-evident.
//
// It expects VAULT_ADDR / VAULT_TOKEN / VAULT_CACERT in the environment (set by
// scripts/statechain-demo.sh, which spins a throwaway dev-tls OpenBao with a
// Transit `ecdsa-p256` key). Run it via `make demo`.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"centralrelay/core/nexus/crypto"
	"centralrelay/core/nexus/recorder"
	"centralrelay/core/nexus/types"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n[!] demo failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	mount := envOr("DEMO_TRANSIT_MOUNT", "cic-transit")
	key := envOr("DEMO_TRANSIT_KEY", "cic-state")

	// The signing key lives in Vault Transit and is never exported.
	svc, err := crypto.NewVaultCryptoService(mount, key)
	if err != nil {
		return fmt.Errorf("vault crypto service: %w", err)
	}

	repo, err := os.MkdirTemp("", "cic-statechain-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(repo)

	rec, err := recorder.NewGitStateRecorder(repo, svc)
	if err != nil {
		return fmt.Errorf("git state recorder: %w", err)
	}

	fmt.Printf("[*] recording infrastructure-state transitions (key stays in Vault: %s/%s)\n\n", mount, key)
	transitions := []struct {
		run    string
		actual map[string]string
	}{
		{"net-v1", map[string]string{"network": "10.0.0.0/24", "vms": "1"}},
		{"net-v1-scaled", map[string]string{"network": "10.0.0.0/24", "vms": "3"}},
		{"net-v2", map[string]string{"network": "10.0.1.0/24", "vms": "3"}},
	}
	for i, tr := range transitions {
		commit, err := rec.RecordState(ctx, types.ExpectedState{RunID: tr.run, Timestamp: time.Now().UTC()}, tr.actual)
		if err != nil {
			return fmt.Errorf("record %s: %w", tr.run, err)
		}
		printCommit(repo, i+1, tr.run, commit)
	}

	fmt.Println("\n[*] VerifyChain (recompute manifest → Vault verify → parent linkage):")
	if err := rec.VerifyChain(ctx); err != nil {
		return fmt.Errorf("a freshly recorded chain must verify, but: %w", err)
	}
	fmt.Print("  ✓ signed state chain verified — signatures valid, chain intact\n\n")

	// Tamper: alter a committed state without re-signing. The Vault signature no
	// longer matches the manifest.
	fmt.Println("[*] tampering the latest state (altering actual.json, signature untouched)...")
	if err := tamper(repo); err != nil {
		return err
	}
	fmt.Println("[*] VerifyChain:")
	if err := rec.VerifyChain(ctx); err != nil {
		fmt.Printf("  ✗ %v\n", err)
		fmt.Println("\n[ok] the chain is tamper-evident: an altered commit is caught, and only")
		fmt.Println("     Vault could have produced a valid signature — trust is proven, not asserted.")
		return nil
	}
	return fmt.Errorf("tampering was NOT detected — this must not happen")
}

// printCommit shows the just-recorded commit's identity, parent linkage, and the
// (truncated) Vault signature, read from the working tree it was written to.
func printCommit(repo string, n int, desired, commit string) {
	var meta struct {
		Parent string `json:"parent"`
	}
	readJSON(filepath.Join(repo, "meta.json"), &meta)
	var sig struct {
		Signature string `json:"signature"`
	}
	readJSON(filepath.Join(repo, "signature.meta"), &sig)

	fmt.Printf("  commit #%d  %s  desired=%-14s parent=%s  sig=%s  ✓\n",
		n, short(commit), desired, short(meta.Parent), short(sig.Signature))
}

// tamper overwrites actual.json with different content and commits it, leaving
// the (now stale) signature.meta in place.
func tamper(repo string) error {
	if err := os.WriteFile(filepath.Join(repo, "actual.json"), []byte(`{"network":"10.0.6.6/24","vms":"666"}`), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"add", "actual.json"}, {"commit", "-m", "tamper"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w (%s)", args, err, out)
		}
	}
	return nil
}

func readJSON(path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, v)
}

func short(s string) string {
	if len(s) <= 12 {
		if s == "" {
			return "<root>"
		}
		return s
	}
	return s[:12] + ".."
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
