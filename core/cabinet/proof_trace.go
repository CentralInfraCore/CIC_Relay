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

// hashValue returns the hex-encoded SHA-256 of the canonical JSON representation of v.
func hashValue(v interface{}) (string, error) {
	b, err := canonicaljson.ToJSON(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// computeChainHash returns SHA-256 of: sourceDigest + concatenated output hashes of all steps.
// sourceDigest anchors the chain to the source revision; pass "" when not available.
// An empty sourceDigest with an empty steps slice produces the hash of the empty string.
func computeChainHash(sourceDigest string, steps []ProofTraceStep) string {
	var sb strings.Builder
	sb.WriteString(sourceDigest)
	for _, s := range steps {
		sb.WriteString(s.OutputHash)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}