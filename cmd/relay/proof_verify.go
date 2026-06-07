package main

import (
	"encoding/json"
	"fmt"

	"centralrelay/core/cabinet"
)

// ProofArtifact is the unified proof record produced by every CIC-Relay execution.
// It implements the proof_artifact@1.0 schema contract.
type ProofArtifact struct {
	WorkflowID   string               `json:"workflow_id"`
	SourceDigest string               `json:"source_digest"`
	ChainHash    string               `json:"chain_hash"`
	Steps        []ProofArtifactStep  `json:"steps"`
	Timestamp    string               `json:"timestamp"`
	PoSEResult   string               `json:"pose_result,omitempty"`
	CommitRecord *ProofArtifactCommit `json:"commit_record,omitempty"`
}

// ProofArtifactStep mirrors cabinet.ProofTraceStep for the verifier.
type ProofArtifactStep struct {
	Name       string `json:"name"`
	Module     string `json:"module"`
	InputHash  string `json:"input_hash"`
	OutputHash string `json:"output_hash"`
}

// ProofArtifactCommit holds the state commit record appended after PoSE verification.
type ProofArtifactCommit struct {
	ID        string `json:"id"`
	TraceHead string `json:"trace_head"`
	PoSERoot  string `json:"pose_root"`
	Actor     string `json:"actor,omitempty"`
}

// VerifyResult is the output of VerifyProofArtifact.
type VerifyResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// VerifyProofArtifact validates a ProofArtifact against the proof_artifact@1.0 schema contract.
//
// Checks performed:
//  1. Required fields present (workflow_id, source_digest, chain_hash, steps, timestamp).
//  2. chain_hash recomputed and verified against the artifact's declared value.
//  3. Per-step: name, module, input_hash, output_hash all non-empty.
//  4. pose_result value is one of: VERIFIED | DRIFT | SKIPPED (when present).
//  5. commit_record.id == chain_hash when commit_record is present.
func VerifyProofArtifact(a *ProofArtifact) VerifyResult {
	var errs, warns []string

	// 1. Required fields
	if a.WorkflowID == "" {
		errs = append(errs, "workflow_id is required")
	}
	if a.ChainHash == "" {
		errs = append(errs, "chain_hash is required")
	}
	if a.Timestamp == "" {
		errs = append(errs, "timestamp is required")
	}
	if a.SourceDigest == "" {
		warns = append(warns, "source_digest is empty — chain is not anchored to a source revision")
	}

	// 2. chain_hash recomputation
	if a.WorkflowID != "" && a.ChainHash != "" {
		steps := make([]cabinet.ProofTraceStep, len(a.Steps))
		for i, s := range a.Steps {
			steps[i] = cabinet.ProofTraceStep{
				Name:       s.Name,
				Module:     s.Module,
				InputHash:  s.InputHash,
				OutputHash: s.OutputHash,
			}
		}
		expected := cabinet.ComputeChainHashV1(a.WorkflowID, a.SourceDigest, steps)
		if expected != a.ChainHash {
			errs = append(errs, fmt.Sprintf(
				"chain_hash mismatch: declared=%s recomputed=%s", a.ChainHash, expected,
			))
		}
	}

	// 3. Per-step validation
	for i, s := range a.Steps {
		prefix := fmt.Sprintf("steps[%d]", i)
		if s.Name == "" {
			errs = append(errs, prefix+".name is required")
		}
		if s.Module == "" {
			errs = append(errs, prefix+".module is required")
		}
		if s.InputHash == "" {
			errs = append(errs, prefix+".input_hash is required")
		}
		if s.OutputHash == "" {
			errs = append(errs, prefix+".output_hash is required")
		}
	}

	// 4. pose_result enum
	if a.PoSEResult != "" {
		switch a.PoSEResult {
		case "VERIFIED", "DRIFT", "SKIPPED":
		default:
			errs = append(errs, fmt.Sprintf(
				"pose_result must be VERIFIED | DRIFT | SKIPPED, got %q", a.PoSEResult,
			))
		}
	}

	// 5. commit_record consistency
	if a.CommitRecord != nil {
		if a.CommitRecord.ID != a.ChainHash {
			errs = append(errs, fmt.Sprintf(
				"commit_record.id must equal chain_hash: got %q, want %q",
				a.CommitRecord.ID, a.ChainHash,
			))
		}
		if a.PoSEResult != "VERIFIED" {
			warns = append(warns, "commit_record present but pose_result is not VERIFIED")
		}
	}

	return VerifyResult{
		Valid:    len(errs) == 0,
		Errors:   errs,
		Warnings: warns,
	}
}

// ProofArtifactFromTrace converts a cabinet.ProofTrace to a ProofArtifact.
func ProofArtifactFromTrace(t *cabinet.ProofTrace) *ProofArtifact {
	steps := make([]ProofArtifactStep, len(t.Steps))
	for i, s := range t.Steps {
		steps[i] = ProofArtifactStep{
			Name:       s.Name,
			Module:     s.Module,
			InputHash:  s.InputHash,
			OutputHash: s.OutputHash,
		}
	}
	return &ProofArtifact{
		WorkflowID:   t.WorkflowID,
		SourceDigest: t.SourceDigest,
		ChainHash:    t.ChainHash,
		Steps:        steps,
		Timestamp:    t.Timestamp,
	}
}

// MarshalProofArtifact serializes a ProofArtifact to canonical JSON.
func MarshalProofArtifact(a *ProofArtifact) ([]byte, error) {
	return json.Marshal(a)
}
