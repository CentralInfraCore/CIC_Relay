package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
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

// GoRuntimeDigest returns a "sha256:<hex>" digest of the Go toolchain identity.
//
// Included fields (separated by \x00):
//   - runtime.Version()  — e.g. "go1.22.2"
//   - runtime.GOOS       — e.g. "linux"
//   - runtime.GOARCH     — e.g. "amd64"
//   - CGO_ENABLED        — "0" or "1" (from debug.ReadBuildInfo settings)
//   - -tags              — build tags at compile time, if any
//   - GOEXPERIMENT       — active Go experiments, if any
//
// Using only runtime.Version() would miss CGO mode and build-tag variation that
// can materially change binary behavior while keeping the same version string.
func GoRuntimeDigest() string {
	var sb strings.Builder
	sb.WriteString(runtime.Version())
	sb.WriteByte(0)
	sb.WriteString(runtime.GOOS)
	sb.WriteByte(0)
	sb.WriteString(runtime.GOARCH)
	sb.WriteByte(0)
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "CGO_ENABLED", "-tags", "GOEXPERIMENT":
				sb.WriteString(s.Key)
				sb.WriteByte(0x1f) // unit separator between key and value
				sb.WriteString(s.Value)
				sb.WriteByte(0)
			}
		}
	}
	sum := sha256.Sum256([]byte(sb.String()))
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
