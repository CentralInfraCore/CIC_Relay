package cabinet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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