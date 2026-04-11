package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime"
)

// BuildContext carries build-time and runtime-discovered metadata that populates
// the SOURCE, BUILDENV, VALIDATION, and AUDIT subtrees of a VerificationManifest.
//
// Fields are typically sourced from ldflags variables (CommitHash, SourceTreeHash)
// or discovered at startup (go.sum digest, CA fingerprint).
// Empty fields are replaced with NullMarker ("∅") by LeafHash automatically.
type BuildContext struct {
	// SOURCE
	SourceRef        string // commit SHA — from -X main.CommitHash
	SourceTreeDigest string // source tree hash — from -X main.SourceTreeHash

	// BUILDENV
	DockerImageChecksum  string // pinned builder image digest
	ToolchainDigest      string // compiler/toolchain version digest
	DependencyLockDigest string // sha256(go.sum) — use GoSumDigest()
	PipelinePolicyDigest string // CI pipeline policy digest

	// VALIDATION
	CAFingerprint string // sha256 of the trust anchor CA cert
	CertChainHash string // hash of the full certificate chain

	// AUDIT
	SigningPolicyDigest string // digest of the signing policy config
}

// GoRuntimeDigest returns sha256(runtime.Version()) as "sha256:<hex>".
// This gives a stable, zero-dependency toolchain fingerprint.
func GoRuntimeDigest() string {
	sum := sha256.Sum256([]byte(runtime.Version()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// GoSumDigest reads the go.sum file at path and returns its lowercase SHA-256
// hex digest prefixed with "sha256:".
// Returns NullMarker if path is empty or the file cannot be read.
func GoSumDigest(path string) string {
	if path == "" {
		return NullMarker
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return NullMarker
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
