package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProofTraceStepFromActuation(t *testing.T) {
	t.Run("maps audit facts into a step and folds into the chain hash", func(t *testing.T) {
		step := ProofTraceStepFromActuation("trace-abc-123", "GET", "127.0.0.1", "reqhash", "resphash")
		assert.Equal(t, "trace-abc-123", step.Name)
		assert.Equal(t, "http-executor", step.Module)
		assert.Equal(t, "reqhash", step.InputHash)
		assert.Equal(t, "resphash", step.OutputHash)

		// The actuation step folds into the same ProofTrace chain hash as any
		// native/WASM step — the bridge lands in the existing audit vocabulary.
		h := ComputeChainHashV1("wf", "src", []ProofTraceStep{step})
		assert.NotEmpty(t, h)
		assert.Len(t, h, 64)
	})

	t.Run("falls back to method and host when no trace id", func(t *testing.T) {
		step := ProofTraceStepFromActuation("", "POST", "api.example", "i", "o")
		assert.Equal(t, "POST api.example", step.Name)
	})

	t.Run("different body digests yield different chain hashes", func(t *testing.T) {
		s1 := ProofTraceStepFromActuation("t", "GET", "h", "in1", "out")
		s2 := ProofTraceStepFromActuation("t", "GET", "h", "in2", "out")
		assert.NotEqual(t,
			ComputeChainHashV1("wf", "src", []ProofTraceStep{s1}),
			ComputeChainHashV1("wf", "src", []ProofTraceStep{s2}),
		)
	})
}

func TestProofFromActuation(t *testing.T) {
	step := ProofTraceStepFromActuation("trace-1", "GET", "127.0.0.1", "reqhash", "resphash")
	proof := ProofFromActuation("cap-flow", "src-rev", "2026-07-13T00:00:00Z", "abc123recordhash", step)

	require.NotNil(t, proof.ProofTrace)
	assert.Equal(t, "cap-flow", proof.ProofTrace.WorkflowID)
	assert.Equal(t, "src-rev", proof.ProofTrace.SourceDigest)
	assert.Equal(t, "2026-07-13T00:00:00Z", proof.ProofTrace.Timestamp)
	assert.Equal(t, "abc123recordhash", proof.RecordHash)
	require.Len(t, proof.ProofTrace.Steps, 1)
	assert.Equal(t, step, proof.ProofTrace.Steps[0])

	// The ChainHash is the canonical hash over exactly this single-step trace.
	want := ComputeChainHashV1("cap-flow", "src-rev", []ProofTraceStep{step})
	assert.Equal(t, want, proof.ProofTrace.ChainHash)
	assert.Len(t, proof.ProofTrace.ChainHash, 64)

	t.Run("records through an ExecutionRecorder", func(t *testing.T) {
		var got interface{}
		var gotID string
		rec := recorderFunc(func(_ context.Context, id string, trace interface{}) error {
			gotID = id
			got = trace
			return nil
		})
		require.NoError(t, rec.Record(context.Background(), proof.ProofTrace.WorkflowID, proof))
		assert.Equal(t, "cap-flow", gotID)
		assert.Equal(t, proof, got)
	})
}

// recorderFunc adapts a func to the ExecutionRecorder interface for testing.
type recorderFunc func(ctx context.Context, workflowID string, trace interface{}) error

func (f recorderFunc) Record(ctx context.Context, workflowID string, trace interface{}) error {
	return f(ctx, workflowID, trace)
}

func TestComputeChainHash(t *testing.T) {
	t.Run("all empty inputs produces deterministic hash", func(t *testing.T) {
		h := ComputeChainHashV1("", "", nil)
		assert.Len(t, h, 64)
	})

	t.Run("workflowID is included in hash", func(t *testing.T) {
		h1 := ComputeChainHashV1("wf-a", "src", nil)
		h2 := ComputeChainHashV1("wf-b", "src", nil)
		assert.NotEqual(t, h1, h2)
	})

	t.Run("sourceDigest anchors the chain", func(t *testing.T) {
		h1 := ComputeChainHashV1("wf", "rev1", nil)
		h2 := ComputeChainHashV1("wf", "rev2", nil)
		assert.NotEqual(t, h1, h2)
	})

	t.Run("step name is included in hash", func(t *testing.T) {
		s1 := []ProofTraceStep{{Name: "step-a", Module: "m", InputHash: "i", OutputHash: "o"}}
		s2 := []ProofTraceStep{{Name: "step-b", Module: "m", InputHash: "i", OutputHash: "o"}}
		assert.NotEqual(t, ComputeChainHashV1("wf", "src", s1), ComputeChainHashV1("wf", "src", s2))
	})

	t.Run("inputHash is included in hash", func(t *testing.T) {
		s1 := []ProofTraceStep{{Name: "s", Module: "m", InputHash: "in1", OutputHash: "out"}}
		s2 := []ProofTraceStep{{Name: "s", Module: "m", InputHash: "in2", OutputHash: "out"}}
		assert.NotEqual(t, ComputeChainHashV1("wf", "src", s1), ComputeChainHashV1("wf", "src", s2))
	})

	t.Run("outputHash is included in hash", func(t *testing.T) {
		s1 := []ProofTraceStep{{Name: "s", Module: "m", InputHash: "in", OutputHash: "out1"}}
		s2 := []ProofTraceStep{{Name: "s", Module: "m", InputHash: "in", OutputHash: "out2"}}
		assert.NotEqual(t, ComputeChainHashV1("wf", "src", s1), ComputeChainHashV1("wf", "src", s2))
	})

	t.Run("same inputs produce same hash (determinism)", func(t *testing.T) {
		steps := []ProofTraceStep{{Name: "s", Module: "m", InputHash: "i", OutputHash: "o"}}
		assert.Equal(t, ComputeChainHashV1("wf", "src", steps), ComputeChainHashV1("wf", "src", steps))
	})

	t.Run("different sourceDigest produces different hash", func(t *testing.T) {
		h1 := ComputeChainHashV1("wf", "rev1", nil)
		h2 := ComputeChainHashV1("wf", "rev2", nil)
		assert.NotEqual(t, h1, h2)
	})
}

func TestHashValue(t *testing.T) {
	t.Run("simple map produces deterministic hex hash", func(t *testing.T) {
		v := map[string]interface{}{"key": "value"}
		h1, err := hashValue(v)
		require.NoError(t, err)
		h2, err := hashValue(v)
		require.NoError(t, err)
		assert.Equal(t, h1, h2)
		assert.Len(t, h1, 64) // SHA-256 hex
	})

	t.Run("different values produce different hashes", func(t *testing.T) {
		h1, err := hashValue(map[string]interface{}{"a": 1})
		require.NoError(t, err)
		h2, err := hashValue(map[string]interface{}{"a": 2})
		require.NoError(t, err)
		assert.NotEqual(t, h1, h2)
	})

	t.Run("nil input hashes successfully", func(t *testing.T) {
		h, err := hashValue(nil)
		require.NoError(t, err)
		assert.Len(t, h, 64)
	})
}
