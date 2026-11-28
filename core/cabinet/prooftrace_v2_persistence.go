package cabinet

import "fmt"

// The persistence object of ProofTrace V2 (the third result dimension, "evidence"). It lives OUTSIDE the artifact and
// outside its hash domain: an artifact can not assert about itself that it was durably recorded, and the outcome of
// the recording is not known when the artifact is created. It names the artifact it is about (ArtifactChainHash), and
// a persisted record carries an attestation. The schema-level checks here are NOT the proof: that the attestation is
// authentic and binds this artifact is established by the verifier against pinned recorder keys (pkg/o1verify).

// Attestation kinds of a persisted record.
const (
	// AttestationGitSignedCommit is a kind of persistence attestation.
	AttestationGitSignedCommit = "git-signed-commit"
	// AttestationRecorderSignature is a kind of persistence attestation.
	AttestationRecorderSignature = "recorder-signature"
)

// Persistence error codes.
const (
	// CodePersistenceNotBound: the record names no artifact (no chain hash), or a malformed one.
	CodePersistenceNotBound = "V2_PERSISTENCE_NOT_BOUND"
	// CodePersistenceRecorderRequired: the record names no recorder.
	CodePersistenceRecorderRequired = "V2_PERSISTENCE_RECORDER_REQUIRED"
	// CodePersistedWithoutAttestation: persisted=true without a commit or without an attestation.
	CodePersistedWithoutAttestation = "V2_PERSISTED_WITHOUT_ATTESTATION"
	// CodePersistedWithError: persisted=true but an error code is present.
	CodePersistedWithError = "V2_PERSISTED_WITH_ERROR"
	// CodeNotPersistedWithoutError: persisted=false without an error code.
	CodeNotPersistedWithoutError = "V2_NOT_PERSISTED_WITHOUT_ERROR"
	// CodeNotPersistedWithEvidence: persisted=false but a commit or an attestation is present.
	CodeNotPersistedWithEvidence = "V2_NOT_PERSISTED_WITH_EVIDENCE"
	// CodeAttestationInvalid: an unknown attestation kind or a malformed digest.
	CodeAttestationInvalid = "V2_ATTESTATION_INVALID"
)

// AttestationV2 is the attestation of a persisted record.
type AttestationV2 struct {
	Kind            string `json:"kind"`
	SignerKeyID     string `json:"signer_key_id"`
	SignatureDigest string `json:"signature_digest"`
}

// PersistenceStatusV2 is the persistence object (prooftrace-v2-persistence.schema.json).
type PersistenceStatusV2 struct {
	ArtifactChainHash string         `json:"artifact_chain_hash"`
	Persisted         bool           `json:"persisted"`
	Recorder          string         `json:"recorder"`
	Commit            string         `json:"commit,omitempty"`
	ErrorCode         string         `json:"error_code,omitempty"`
	Attestation       *AttestationV2 `json:"attestation,omitempty"`
}

// Validate checks the shape rules of the record. It does not (and can not) prove the attestation.
func (p *PersistenceStatusV2) Validate() error {
	if p == nil || !digestPattern.MatchString(p.ArtifactChainHash) {
		return &ArtifactV2Error{Code: CodePersistenceNotBound, Field: "artifact_chain_hash"}
	}
	if p.Recorder == "" {
		return &ArtifactV2Error{Code: CodePersistenceRecorderRequired, Field: "recorder"}
	}
	if p.Persisted {
		if p.ErrorCode != "" {
			return &ArtifactV2Error{Code: CodePersistedWithError, Field: "error_code"}
		}
		if p.Commit == "" || p.Attestation == nil {
			return &ArtifactV2Error{Code: CodePersistedWithoutAttestation, Field: "attestation"}
		}
		switch p.Attestation.Kind {
		case AttestationGitSignedCommit, AttestationRecorderSignature:
		default:
			return &ArtifactV2Error{Code: CodeAttestationInvalid, Field: "attestation.kind", Detail: p.Attestation.Kind}
		}
		if !digestPattern.MatchString(p.Attestation.SignerKeyID) || !digestPattern.MatchString(p.Attestation.SignatureDigest) {
			return &ArtifactV2Error{Code: CodeAttestationInvalid, Field: "attestation", Detail: "a digest is malformed"}
		}
		return nil
	}
	if p.ErrorCode == "" {
		return &ArtifactV2Error{Code: CodeNotPersistedWithoutError, Field: "error_code"}
	}
	if p.Commit != "" || p.Attestation != nil {
		return &ArtifactV2Error{Code: CodeNotPersistedWithEvidence, Field: "commit/attestation"}
	}
	return nil
}

// NewPersistenceFailureV2 builds the record of a failed recording. The operation itself is not claimed to have not
// happened: this object says nothing about the execution dimension (#186).
func NewPersistenceFailureV2(chainHash, recorder, errorCode string) (*PersistenceStatusV2, error) {
	p := &PersistenceStatusV2{ArtifactChainHash: chainHash, Persisted: false, Recorder: recorder, ErrorCode: errorCode}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("persistence failure record: %w", err)
	}
	return p, nil
}
