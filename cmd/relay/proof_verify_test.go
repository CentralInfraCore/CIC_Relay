package main

import (
	"strings"
	"testing"

	"centralrelay/core/cabinet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testSteps = []cabinet.ProofTraceStep{
	{Name: "step1", Module: "mod@1.0", InputHash: "aaa", OutputHash: "bbb"},
}

func validProofArtifact() *ProofArtifact {
	return &ProofArtifact{
		WorkflowID:   "wf@1.0",
		SourceDigest: "src123",
		ChainHash:    cabinet.ComputeChainHashV1("wf@1.0", "src123", testSteps),
		Steps: []ProofArtifactStep{
			{Name: "step1", Module: "mod@1.0", InputHash: "aaa", OutputHash: "bbb"},
		},
		Timestamp: "2026-01-01T00:00:00Z",
	}
}

func TestVerifyProofArtifact_Valid(t *testing.T) {
	r := VerifyProofArtifact(validProofArtifact())
	assert.True(t, r.Valid)
	assert.Empty(t, r.Errors)
}

func TestVerifyProofArtifact_MissingWorkflowID(t *testing.T) {
	a := validProofArtifact()
	a.WorkflowID = ""
	r := VerifyProofArtifact(a)
	assert.False(t, r.Valid)
	assert.Contains(t, r.Errors[0], "workflow_id")
}

func TestVerifyProofArtifact_MissingChainHash(t *testing.T) {
	a := validProofArtifact()
	a.ChainHash = ""
	r := VerifyProofArtifact(a)
	assert.False(t, r.Valid)
	assert.Contains(t, r.Errors[0], "chain_hash")
}

func TestVerifyProofArtifact_MissingTimestamp(t *testing.T) {
	a := validProofArtifact()
	a.Timestamp = ""
	r := VerifyProofArtifact(a)
	assert.False(t, r.Valid)
	assert.Contains(t, r.Errors[0], "timestamp")
}

func TestVerifyProofArtifact_EmptySourceDigest_Warns(t *testing.T) {
	a := validProofArtifact()
	a.SourceDigest = ""
	a.ChainHash = cabinet.ComputeChainHashV1("wf@1.0", "", testSteps)
	r := VerifyProofArtifact(a)
	assert.True(t, r.Valid)
	require.NotEmpty(t, r.Warnings)
	assert.Contains(t, r.Warnings[0], "source_digest")
}

// "unknown" is the sentinel the relay writes when it has no source revision to
// name. It means exactly what an empty value means, but the emptiness check let
// it through silently: a trace that was in fact unanchored verified with no
// warning at all.
func TestVerifyProofArtifact_UnknownSourceDigest_Warns(t *testing.T) {
	a := validProofArtifact()
	a.SourceDigest = "unknown"
	a.ChainHash = cabinet.ComputeChainHashV1("wf@1.0", "unknown", testSteps)
	r := VerifyProofArtifact(a)
	assert.True(t, r.Valid, "an unanchored chain is still internally consistent")
	require.NotEmpty(t, r.Warnings, `"unknown" must warn just like an empty source_digest`)
	assert.Contains(t, r.Warnings[0], "source_digest")
}

func TestVerifyProofArtifact_ChainHashMismatch(t *testing.T) {
	a := validProofArtifact()
	a.ChainHash = strings.Repeat("d", 64)
	r := VerifyProofArtifact(a)
	assert.False(t, r.Valid)
	assert.Contains(t, r.Errors[0], "chain_hash mismatch")
}

func TestVerifyProofArtifact_StepMissingFields(t *testing.T) {
	a := validProofArtifact()
	empty := cabinet.ProofTraceStep{}
	a.ChainHash = cabinet.ComputeChainHashV1("wf@1.0", "src123", []cabinet.ProofTraceStep{empty})
	a.Steps = []ProofArtifactStep{{}}
	r := VerifyProofArtifact(a)
	assert.False(t, r.Valid)
	joined := strings.Join(r.Errors, " ")
	assert.Contains(t, joined, "steps[0]")
}

func TestVerifyProofArtifact_ValidPoSEResults(t *testing.T) {
	for _, pose := range []string{"VERIFIED", "DRIFT", "SKIPPED"} {
		a := validProofArtifact()
		a.PoSEResult = pose
		r := VerifyProofArtifact(a)
		assert.True(t, r.Valid, "pose_result=%s should be valid", pose)
		assert.Empty(t, r.Errors)
	}
}

func TestVerifyProofArtifact_InvalidPoSEResult(t *testing.T) {
	a := validProofArtifact()
	a.PoSEResult = "BADVALUE"
	r := VerifyProofArtifact(a)
	assert.False(t, r.Valid)
	assert.Contains(t, r.Errors[0], "pose_result")
}

func TestVerifyProofArtifact_CommitRecord_Consistent(t *testing.T) {
	a := validProofArtifact()
	a.PoSEResult = "VERIFIED"
	a.CommitRecord = &ProofArtifactCommit{ID: a.ChainHash, TraceHead: "head123"}
	r := VerifyProofArtifact(a)
	assert.True(t, r.Valid)
	assert.Empty(t, r.Errors)
}

func TestVerifyProofArtifact_CommitRecord_IDMismatch(t *testing.T) {
	a := validProofArtifact()
	a.CommitRecord = &ProofArtifactCommit{ID: "wrong"}
	r := VerifyProofArtifact(a)
	assert.False(t, r.Valid)
	assert.Contains(t, r.Errors[0], "commit_record.id")
}

func TestVerifyProofArtifact_CommitRecord_PoseNotVerified_Warns(t *testing.T) {
	a := validProofArtifact()
	a.CommitRecord = &ProofArtifactCommit{ID: a.ChainHash}
	r := VerifyProofArtifact(a)
	assert.True(t, r.Valid)
	require.NotEmpty(t, r.Warnings)
	assert.Contains(t, r.Warnings[0], "pose_result")
}

func TestProofArtifactFromTrace(t *testing.T) {
	trace := &cabinet.ProofTrace{
		WorkflowID:   "wf@1.0",
		SourceDigest: "src",
		ChainHash:    "chain",
		Timestamp:    "2026-01-01T00:00:00Z",
		Steps: []cabinet.ProofTraceStep{
			{Name: "s1", Module: "m@1.0", InputHash: "ih", OutputHash: "oh"},
		},
	}
	a := ProofArtifactFromTrace(trace)
	assert.Equal(t, "wf@1.0", a.WorkflowID)
	assert.Equal(t, "src", a.SourceDigest)
	assert.Equal(t, "chain", a.ChainHash)
	assert.Equal(t, "2026-01-01T00:00:00Z", a.Timestamp)
	require.Len(t, a.Steps, 1)
	assert.Equal(t, "s1", a.Steps[0].Name)
	assert.Equal(t, "ih", a.Steps[0].InputHash)
}

func TestProofArtifactFromTrace_EmptySteps(t *testing.T) {
	trace := &cabinet.ProofTrace{
		WorkflowID: "wf@1.0",
		ChainHash:  "ch",
		Timestamp:  "ts",
	}
	a := ProofArtifactFromTrace(trace)
	assert.Empty(t, a.Steps)
}

func TestMarshalProofArtifact(t *testing.T) {
	a := validProofArtifact()
	b, err := MarshalProofArtifact(a)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "workflow_id")
	assert.Contains(t, s, "chain_hash")
	assert.Contains(t, s, "wf@1.0")
}
