package cabinet

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"centralrelay/pkg/canonicaljson"
)

// ProofTraceStep records the cryptographic fingerprint of a single workflow step.
type ProofTraceStep struct {
	Name       string `json:"name"`
	Module     string `json:"module"`
	InputHash  string `json:"input_hash"`
	OutputHash string `json:"output_hash"`
}

// ProofTrace is the immutable audit record of a completed workflow execution.
// Every field is deterministic and canonicalizable; no clock or RNG side-effects
// are introduced here — the Timestamp must be injected by the caller.
//
// SourceDigest anchors the chain to the source revision. It is injected by the caller
// (e.g. git HEAD commit hash or BuildID) and becomes the first input of ChainHash,
// making the hash-chain continuous from source to output.
type ProofTrace struct {
	WorkflowID   string           `json:"workflow_id"`
	SourceDigest string           `json:"source_digest,omitempty"`
	Steps        []ProofTraceStep `json:"steps"`
	ChainHash    string           `json:"chain_hash"`
	Timestamp    string           `json:"timestamp"`
}

// ProofTraceStepFromActuation projects an http-executor actuation's non-secret
// audit facts (feature-009, carried across the Rust↔Go FFI boundary by run_flow)
// into a ProofTraceStep, so a capability actuation folds into the same
// hash-chained ProofTrace as native and WASM steps. The request/response body
// SHA-256 digests become the step's input/output hashes; the step name is the
// caller's traceID, or "<method> <host>" when no trace id was supplied. It
// carries only digests — never the credential or any body contents.
func ProofTraceStepFromActuation(traceID, method, host, requestBodySHA256, responseBodySHA256 string) ProofTraceStep {
	name := traceID
	if name == "" {
		name = method + " " + host
	}
	return ProofTraceStep{
		Name:       name,
		Module:     "http-executor",
		InputHash:  requestBodySHA256,
		OutputHash: responseBodySHA256,
	}
}

// ActuationProof is the durable, recordable proof of one http-executor
// actuation (feature-009): the Go-side single-step ProofTrace (with its
// ChainHash) plus the Rust-side tamper-evident record_hash anchor carried across
// the FFI seam. Handed to an ExecutionRecorder, it is persisted as a
// Vault-signed Git commit (WorkflowRecorder → GitStateRecorder), so an actuation
// becomes durable, cryptographically anchored provenance — not just an in-memory
// fact. Both chains travel together: the Go ProofTrace ChainHash and the Rust
// ChainedEntry record_hash.
type ActuationProof struct {
	ProofTrace *ProofTrace `json:"proof_trace"`
	// RecordHash is the Rust-side ChainedEntry anchor (hex SHA-256), empty if the
	// actuation carried no audit chain.
	RecordHash string `json:"record_hash"`
}

// ProofFromActuation wraps a single actuation step into an ActuationProof: a
// complete, recordable single-step ProofTrace (anchored to sourceDigest, with
// the ChainHash and an injected timestamp) plus the Rust record_hash anchor.
// Build step with ProofTraceStepFromActuation. Hand the result to an
// ExecutionRecorder to persist it durably.
func ProofFromActuation(workflowID, sourceDigest, timestamp, recordHash string, step ProofTraceStep) ActuationProof {
	steps := []ProofTraceStep{step}
	return ActuationProof{
		ProofTrace: &ProofTrace{
			WorkflowID:   workflowID,
			SourceDigest: sourceDigest,
			Steps:        steps,
			ChainHash:    ComputeChainHashV1(workflowID, sourceDigest, steps),
			Timestamp:    timestamp,
		},
		RecordHash: recordHash,
	}
}

// hashValue returns the hex-encoded SHA-256 of the canonical JSON representation of v.
func hashValue(v interface{}) (string, error) {
	b, err := canonicaljson.ToJSON(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ComputeChainHashV1 returns SHA-256 over the full execution identity:
//
//	"CICCHAINv1" \x00 sourceDigest \x00 workflowID \x00 [per step: name \x00 module \x00 inputHash \x00 outputHash \x00]
//
// The "CICCHAINv1" domain prefix:
//   - Distinguishes chain hashes from Merkle-root hashes or leaf hashes produced
//     elsewhere in this system (domain separation).
//   - Makes the format versioned: a future format change uses "CICCHAINv2" and the
//     two are unambiguously different even with identical field contents.
//
// \x00 separators prevent hash collisions between adjacent variable-length fields.
// sourceDigest anchors the chain to the source revision; pass "" when not available.
// workflowID binds the hash to the exact workflow definition that was executed.
// InputHash and step Name are included so that two different workflows producing
// the same outputs cannot share the same ChainHash.
func ComputeChainHashV1(workflowID, sourceDigest string, steps []ProofTraceStep) string {
	var sb strings.Builder
	sb.WriteString("CICCHAINv1") // domain prefix — see comment above
	sb.WriteByte(0)
	sb.WriteString(sourceDigest)
	sb.WriteByte(0)
	sb.WriteString(workflowID)
	sb.WriteByte(0)
	for _, s := range steps {
		sb.WriteString(s.Name)
		sb.WriteByte(0)
		sb.WriteString(s.Module)
		sb.WriteByte(0)
		sb.WriteString(s.InputHash)
		sb.WriteByte(0)
		sb.WriteString(s.OutputHash)
		sb.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
