// Package schemacompile implements the native modules for the schema compilation workflow.
//
// Three modules, three stubs — each will be filled in as the trust layers land:
//
//	cic.source.assert@1.0  — validates the source assertion (Layer 1)
//	cic.schema.build@1.0   — invokes the CIC-Schemas compiler (Layer 0.2)
//	cic.artifact.sign@1.0  — computes buildHash, attaches cicSign/cicSignedCA (Layer 2)
package schemacompile

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"

	"centralrelay/pkg/canonicaljson"
	"centralrelay/pkg/merkle"
	"gopkg.in/yaml.v3"
)

// ArtifactSigner signs raw bytes and returns a signature string.
// Implementations must be context-aware and respect cancellation.
// Pass nil to NewSignArtifactFunc to run without signing (cic_sign will be "unavailable").
type ArtifactSigner interface {
	Sign(ctx context.Context, data []byte) (string, error)
}

// CertVerifyFunc is called with the parsed developer certificate from source_assertion.
// Return a non-nil error to reject the assertion.
// If nil is passed as the verifier, certificate chain verification is skipped (stub mode).
type CertVerifyFunc func(cert *x509.Certificate) error

// cicSchemasArtifact mirrors the structure produced by CIC-Schemas generate_signed_artifact.
// Used for parsing the source_assertion YAML in AssertSource.
type cicSchemasArtifact struct {
	Metadata map[string]interface{} `yaml:"metadata"`
	Spec     interface{}            `yaml:"spec"`
}

// NewAssertSourceFunc returns a native function for cic.source.assert@1.0 wired with
// the provided certificate verifier. Pass nil to run in stub mode (no chain verification).
//
// The returned function:
//   - passes through inputs without source_assertion (stub mode)
//   - validates required metadata fields when source_assertion is present
//   - parses and verifies the PEM certificate in metadata.createdBy.certificate via verify
func NewAssertSourceFunc(verify CertVerifyFunc) func(context.Context, interface{}) (interface{}, error) {
	return func(_ context.Context, input interface{}) (interface{}, error) {
		return assertSourceImpl(input, verify)
	}
}

// AssertSource is the legacy no-verifier entry point (stub mode, Layer 1 not yet wired).
// Use NewAssertSourceFunc to inject a real PKI verifier.
func AssertSource(_ context.Context, input interface{}) (interface{}, error) {
	return assertSourceImpl(input, nil)
}

func assertSourceImpl(input interface{}, verify CertVerifyFunc) (interface{}, error) {
	m, ok := input.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cic.source.assert: expected map, got %T", input)
	}

	assertionYAML, _ := m["source_assertion"].(string)
	if assertionYAML == "" {
		// No assertion provided — pass through in stub mode.
		out := make(map[string]interface{}, len(m)+2)
		for k, v := range m {
			out[k] = v
		}
		out["asserted"] = true
		out["policy_version"] = "1.0"
		return out, nil
	}

	var artifact cicSchemasArtifact
	if err := yaml.Unmarshal([]byte(assertionYAML), &artifact); err != nil {
		return nil, fmt.Errorf("cic.source.assert: failed to parse source_assertion YAML: %w", err)
	}
	if artifact.Metadata == nil {
		return nil, fmt.Errorf("cic.source.assert: source_assertion missing metadata block")
	}

	// Validate required identity fields.
	for _, field := range []string{"name", "version", "checksum", "sign"} {
		if v, _ := artifact.Metadata[field].(string); v == "" {
			return nil, fmt.Errorf("cic.source.assert: source_assertion metadata.%s is required", field)
		}
	}
	var certPEM string
	if cb, _ := artifact.Metadata["createdBy"].(map[string]interface{}); cb == nil {
		return nil, fmt.Errorf("cic.source.assert: source_assertion metadata.createdBy is required")
	} else if certPEM, _ = cb["certificate"].(string); certPEM == "" {
		return nil, fmt.Errorf("cic.source.assert: source_assertion metadata.createdBy.certificate is required")
	}

	// L0.4 — spec checksum cross-validation.
	// metadata.checksum must equal sha256(canonical_json(spec)) — 64-char lowercase hex.
	// This mirrors CIC-Schemas artifact.py:compute_spec_checksum (json.dumps sort_keys=True).
	if artifact.Spec != nil {
		specJSON, err := canonicaljson.ToJSON(artifact.Spec)
		if err != nil {
			return nil, fmt.Errorf("cic.source.assert: failed to canonicalize spec for checksum: %w", err)
		}
		sum := sha256.Sum256(specJSON)
		computed := hex.EncodeToString(sum[:])
		declared, _ := artifact.Metadata["checksum"].(string)
		if declared != computed {
			return nil, fmt.Errorf(
				"cic.source.assert: metadata.checksum mismatch: declared=%q computed=%q",
				declared, computed,
			)
		}
	}

	// L0.3 — X.509 certificate chain verification.
	if verify != nil {
		block, _ := pem.Decode([]byte(certPEM))
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("cic.source.assert: createdBy.certificate is not a valid PEM CERTIFICATE block")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("cic.source.assert: failed to parse createdBy.certificate: %w", err)
		}
		if err := verify(cert); err != nil {
			return nil, fmt.Errorf("cic.source.assert: certificate verification failed: %w", err)
		}
	}

	// Cross-check name/version from the request against the assertion.
	assertedName, _ := artifact.Metadata["name"].(string)
	assertedVersion, _ := artifact.Metadata["version"].(string)
	requestedSchema, _ := m["schema"].(string)
	requestedVersion, _ := m["version"].(string)
	if requestedSchema != "" && assertedName != requestedSchema {
		return nil, fmt.Errorf("cic.source.assert: schema mismatch: request=%q assertion=%q", requestedSchema, assertedName)
	}
	if requestedVersion != "" && assertedVersion != requestedVersion {
		return nil, fmt.Errorf("cic.source.assert: version mismatch: request=%q assertion=%q", requestedVersion, assertedVersion)
	}

	out := map[string]interface{}{
		"asserted":       true,
		"policy_version": "1.0",
		"schema":         assertedName,
		"version":        assertedVersion,
		"metadata":       artifact.Metadata,
		"spec":           artifact.Spec,
		"raw_yaml":       assertionYAML,
	}

	carryProvenance(out, m)
	return out, nil
}

// provenanceKeys are the fields that identify the source revision under build.
//
// cic.pipeline.start resolves the cloned repository's HEAD commit and computes a
// tree digest over it; the pipeline steps propagate them via copyMap. The steps
// in this package, however, build their results from scratch, so without an
// explicit hand-off the provenance is dropped and cic.artifact.sign falls back
// to the relay's own build-time constants. The observable effect was that
// build_hash attested WHAT was compiled but not WHERE it came from: two
// different commits of the same repository produced a bit-identical build_hash
// whenever the compiled artifact was unchanged.
var provenanceKeys = []string{"source_ref", "source_tree_digest", "builder_image_digest", "branch"}

// carryProvenance copies the provenance fields from src to dst when present.
// Absent keys are left absent rather than written as empty strings, so the
// signing step can tell "no repository in this run" from "empty value".
func carryProvenance(dst, src map[string]interface{}) {
	for _, k := range provenanceKeys {
		if v, ok := src[k]; ok {
			dst[k] = v
		}
	}
}

// BuildSchema is the native function for cic.schema.build@1.0.
//
// If the input contains a parsed artifact (from AssertSource), it returns it
// as the artifact to be signed. Otherwise it generates a stub artifact for testing.
func BuildSchema(_ context.Context, input interface{}) (interface{}, error) {
	m, ok := input.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cic.schema.build: expected map, got %T", input)
	}

	// If AssertSource already parsed a real artifact, use it.
	if metadata, ok := m["metadata"].(map[string]interface{}); ok && metadata != nil {
		rawYAML, _ := m["raw_yaml"].(string)
		out := map[string]interface{}{
			"schema":       m["schema"],
			"version":      m["version"],
			"raw_artifact": rawYAML,
			"metadata":     metadata,
			"spec":         m["spec"],
		}
		carryProvenance(out, m)
		return out, nil
	}

	// Stub mode: generate synthetic artifact.
	schema, _ := m["schema"].(string)
	version, _ := m["version"].(string)
	if schema == "" {
		return nil, fmt.Errorf("cic.schema.build: schema is required")
	}
	if version == "" {
		return nil, fmt.Errorf("cic.schema.build: version is required")
	}

	rawArtifact := fmt.Sprintf("apiVersion: cic.com/v1\nkind: SchemaArtifact\nmetadata:\n  name: %s\n  version: %s\nspec: {}\n", schema, version)

	out := map[string]interface{}{
		"schema":       schema,
		"version":      version,
		"raw_artifact": rawArtifact,
	}
	carryProvenance(out, m)
	return out, nil
}

// SignArtifact is the legacy no-signer entry point (stub mode, no Vault signing).
// Use NewSignArtifactFunc to inject a real signer.
func SignArtifact(_ context.Context, input interface{}) (interface{}, error) {
	return newSignArtifactResult(context.Background(), input, merkle.BuildContext{}, nil)
}

// NewSignArtifactFunc returns a cic.artifact.sign@1.0 native function wired with
// build-time context and an optional signer.
//
// Despite the module name "sign", this function performs two distinct roles:
//  1. Verification manifest assembly: always computes verification_root — the
//     Merkle-root hash of the full provenance manifest — regardless of whether
//     Vault signing is available.
//  2. Optional Vault signing: if signer is non-nil, build_hash is signed and
//     cic_sign is set to the resulting signature.
//
// When signer is nil, cic_sign is "unavailable" (non-fatal: the provenance record
// is still complete, just without a Vault signature).
// cic_signed_ca is set to bctx.CAFingerprint when the PKI trust store is loaded (Layer 2).
// Falls back to "stub:pending" in dev/test mode when no CA is available.
// The verification_root hash is always valid regardless of signing availability.
func NewSignArtifactFunc(bctx merkle.BuildContext, signer ArtifactSigner) func(context.Context, interface{}) (interface{}, error) {
	return func(ctx context.Context, input interface{}) (interface{}, error) {
		return newSignArtifactResult(ctx, input, bctx, signer)
	}
}

// newSignArtifactResult is the shared implementation for both SignArtifact and
// NewSignArtifactFunc. It computes build_hash, calls signer if available, and
// computes verification_root.
func newSignArtifactResult(ctx context.Context, input interface{}, bctx merkle.BuildContext, signer ArtifactSigner) (interface{}, error) {
	m, ok := input.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cic.artifact.sign: expected map, got %T", input)
	}
	rawArtifact, _ := m["raw_artifact"].(string)
	if rawArtifact == "" {
		return nil, fmt.Errorf("cic.artifact.sign: raw_artifact is required")
	}

	sum := sha256.Sum256([]byte(rawArtifact))
	buildHash := "sha256:" + hex.EncodeToString(sum[:])

	schema, _ := m["schema"].(string)
	version, _ := m["version"].(string)

	// Source provenance describes the repository under build, NOT the relay.
	//
	// When the pipeline cloned a repository, cic.pipeline.start put the resolved
	// commit and tree digest into the flow; prefer those. Only fall back to bctx
	// (the relay's own build-time constants, fixed at server start) when the run
	// carries no repository — e.g. a direct /v1/schema/compile call.
	//
	// The BuildEnv/Validation fields below deliberately keep using bctx: those
	// correctly describe the environment that performed the build.
	sourceRef, _ := m["source_ref"].(string)
	sourceTreeDigest, _ := m["source_tree_digest"].(string)
	if sourceRef == "" {
		sourceRef = bctx.SourceRef
	}
	if sourceTreeDigest == "" {
		sourceTreeDigest = bctx.SourceTreeDigest
	}

	// M33: compute Merkle-root verification hash from all available provenance inputs.
	manifest := &merkle.VerificationManifest{
		Version: 1,
		Source: merkle.SourceFields{
			SourceRef:        sourceRef,
			SourceTreeDigest: sourceTreeDigest,
		},
		BuildEnv: merkle.BuildEnvFields{
			DockerImageChecksum:  bctx.DockerImageChecksum,
			ToolchainDigest:      bctx.ToolchainDigest,
			DependencyLockDigest: bctx.DependencyLockDigest,
			PipelinePolicyDigest: bctx.PipelinePolicyDigest,
		},
		Inputs: merkle.InputsFields{
			// schema@version is the stable descriptor of the compiled schema input.
			SchemaDescriptorDigest: schema + "@" + version,
		},
		Artifact: merkle.ArtifactFields{
			ArtifactDigest: buildHash,
			ArtifactSize:   int64(len(rawArtifact)),
		},
		Validation: merkle.ValidationFields{
			CAFingerprint: bctx.CAFingerprint,
			CertChainHash: bctx.CertChainHash,
		},
		Audit: merkle.AuditFields{
			SigningPolicyDigest: bctx.SigningPolicyDigest,
		},
	}

	cicSign := "unavailable"
	if signer != nil {
		sig, err := signer.Sign(ctx, []byte(buildHash))
		if err != nil {
			return nil, fmt.Errorf("cic.artifact.sign: vault signing failed: %w", err)
		}
		cicSign = sig
	}

	// Layer 2 (M36): use the loaded CA fingerprint when available; fall back to
	// "stub:pending" in dev/test mode where the trust store is not bootstrapped.
	cicSignedCA := "stub:pending"
	if bctx.CAFingerprint != "" {
		cicSignedCA = bctx.CAFingerprint
	}

	// source_ref / source_tree_digest are echoed so a caller can see which
	// revision the build_hash actually attests, without re-deriving it.
	return map[string]interface{}{
		"artifact":           rawArtifact,
		"build_hash":         buildHash,
		"cic_sign":           cicSign,
		"cic_signed_ca":      cicSignedCA,
		"verification_root":  merkle.BuildVerificationRoot(manifest),
		"source_ref":         sourceRef,
		"source_tree_digest": sourceTreeDigest,
	}, nil
}
