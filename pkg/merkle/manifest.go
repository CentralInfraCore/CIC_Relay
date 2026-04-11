package merkle

import "encoding/hex"

// VerificationManifest holds all provenance inputs for a CIC build artifact.
// Empty string fields are automatically substituted with NullMarker ("∅")
// so the tree structure remains stable across partial builds.
//
// Subtree order is fixed and must never change:
//
//	0 = SOURCE
//	1 = BUILDENV
//	2 = INPUTS
//	3 = ARTIFACT
//	4 = VALIDATION
//	5 = AUDIT
type VerificationManifest struct {
	Version    int
	Source     SourceFields
	BuildEnv   BuildEnvFields
	Inputs     InputsFields
	Artifact   ArtifactFields
	Validation ValidationFields
	Audit      AuditFields
}

// SourceFields — subtree 0.
type SourceFields struct {
	// SourceRef is the commit SHA or tag that identifies the source revision.
	SourceRef string
	// SourceTreeDigest is the deterministic digest of the compiled source tree
	// (e.g. pkg/sourcedigest output injected via ldflags).
	SourceTreeDigest string
	// CommitSignature is the normalized fingerprint of the developer commit signature.
	CommitSignature string
}

// BuildEnvFields — subtree 1.
type BuildEnvFields struct {
	// SchemaDigest is the digest of the schema artifact used during compilation.
	SchemaDigest string
	// DockerImageChecksum is the pinned builder image digest (tag@sha256:...).
	DockerImageChecksum string
	// ToolchainDigest identifies the compiler/toolchain version (e.g. sha256 of go binary).
	ToolchainDigest string
	// DependencyLockDigest is the digest of the dependency lockfile (e.g. sha256(go.sum)).
	DependencyLockDigest string
	// PipelinePolicyDigest is the digest of the CI pipeline policy config.
	PipelinePolicyDigest string
}

// InputsFields — subtree 2.
type InputsFields struct {
	WorkflowDescriptorDigest string
	ModuleDescriptorDigest   string
	SchemaDescriptorDigest   string
	// RuntimeInputDigest covers dynamic inputs passed at execution time.
	// Use NullMarker if there are no runtime inputs.
	RuntimeInputDigest string
}

// ArtifactFields — subtree 3.
// For WASM artifacts, ArtifactDigest must be:
//
//	sha256(strip_custom_sections(wasm_bytes))
//
// because the canonical checksum excludes non-deterministic CustomSection data.
type ArtifactFields struct {
	// ArtifactDigest is the canonical content hash of the output artifact.
	ArtifactDigest string
	// ArtifactSize is encoded as a decimal string (no leading zeros).
	ArtifactSize int64
	// EmbeddedBuildMetaDigest is the digest of build metadata injected into the artifact.
	EmbeddedBuildMetaDigest string
	// ExternalBuildMetaDigest is the digest of the external YAML build metadata file.
	ExternalBuildMetaDigest string
}

// ValidationFields — subtree 4.
// Quorum fields are absent in v0.1.
type ValidationFields struct {
	// BuildSignatureDigest is the digest of the build signature object.
	BuildSignatureDigest string
	// CAFingerprint is the fingerprint of the signing CA certificate.
	CAFingerprint string
	// CertChainHash is the hash of the full certificate chain used for verification.
	CertChainHash string
}

// AuditFields — subtree 5.
// SigningPolicyDigest is the digest of the policy that governs who may sign and
// under what conditions. The actual signature lives externally and references the
// final root hash — it is never included in the tree itself (avoids circularity).
type AuditFields struct {
	// PipelineLogDigest is the digest of the signed pipeline execution log.
	PipelineLogDigest string
	// ReleasePolicyDigest is the digest of the release policy document.
	ReleasePolicyDigest string
	// Timestamp is the build timestamp in UTC ISO-8601 Z format.
	Timestamp string
	// SigningPolicyDigest is the digest of the signing policy config.
	SigningPolicyDigest string
}

// BuildVerificationRoot computes the single final verification hash for m.
// Returns a lowercase 64-char hex-encoded SHA-256 string.
//
// The signing workflow:
//  1. Call BuildVerificationRoot → root
//  2. Sign(root) externally → signature
//  3. Publish { final_verification_hash: root, signature: ... } as separate object
func BuildVerificationRoot(m *VerificationManifest) string {
	hSource := MerkleRoot([][]byte{
		LeafHash("source_ref", m.Source.SourceRef),
		LeafHash("source_tree_digest", m.Source.SourceTreeDigest),
		LeafHash("commit_signature", m.Source.CommitSignature),
	})

	hBuildEnv := MerkleRoot([][]byte{
		LeafHash("schema_digest", m.BuildEnv.SchemaDigest),
		LeafHash("docker_image_checksum", m.BuildEnv.DockerImageChecksum),
		LeafHash("toolchain_digest", m.BuildEnv.ToolchainDigest),
		LeafHash("dependency_lock_digest", m.BuildEnv.DependencyLockDigest),
		LeafHash("pipeline_policy_digest", m.BuildEnv.PipelinePolicyDigest),
	})

	hInputs := MerkleRoot([][]byte{
		LeafHash("workflow_descriptor_digest", m.Inputs.WorkflowDescriptorDigest),
		LeafHash("module_descriptor_digest", m.Inputs.ModuleDescriptorDigest),
		LeafHash("schema_descriptor_digest", m.Inputs.SchemaDescriptorDigest),
		LeafHash("runtime_input_digest", m.Inputs.RuntimeInputDigest),
	})

	hArtifact := MerkleRoot([][]byte{
		LeafHash("artifact_digest", m.Artifact.ArtifactDigest),
		SizeLeaf("artifact_size", m.Artifact.ArtifactSize),
		LeafHash("embedded_build_meta_digest", m.Artifact.EmbeddedBuildMetaDigest),
		LeafHash("external_build_meta_digest", m.Artifact.ExternalBuildMetaDigest),
	})

	hValidation := MerkleRoot([][]byte{
		LeafHash("build_signature_digest", m.Validation.BuildSignatureDigest),
		LeafHash("ca_fingerprint", m.Validation.CAFingerprint),
		LeafHash("cert_chain_hash", m.Validation.CertChainHash),
	})

	hAudit := MerkleRoot([][]byte{
		LeafHash("pipeline_log_digest", m.Audit.PipelineLogDigest),
		LeafHash("release_policy_digest", m.Audit.ReleasePolicyDigest),
		LeafHash("timestamp", m.Audit.Timestamp),
		LeafHash("signing_policy_digest", m.Audit.SigningPolicyDigest),
	})

	root := MerkleRoot([][]byte{
		hSource,
		hBuildEnv,
		hInputs,
		hArtifact,
		hValidation,
		hAudit,
	})

	return hex.EncodeToString(root)
}
