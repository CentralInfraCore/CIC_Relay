package cabinet

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeChainHash(t *testing.T) {
	t.Run("empty sourceDigest and no steps produces hash of empty string", func(t *testing.T) {
		sum := sha256.Sum256([]byte(""))
		want := hex.EncodeToString(sum[:])
		assert.Equal(t, want, computeChainHash("", nil))
	})

	t.Run("sourceDigest only anchors the chain", func(t *testing.T) {
		digest := "abc123"
		sum := sha256.Sum256([]byte(digest))
		want := hex.EncodeToString(sum[:])
		assert.Equal(t, want, computeChainHash(digest, nil))
	})

	t.Run("steps without sourceDigest concatenates output hashes", func(t *testing.T) {
		steps := []ProofTraceStep{
			{OutputHash: "hash1"},
			{OutputHash: "hash2"},
		}
		sum := sha256.Sum256([]byte("hash1hash2"))
		want := hex.EncodeToString(sum[:])
		assert.Equal(t, want, computeChainHash("", steps))
	})

	t.Run("sourceDigest + steps are concatenated in order", func(t *testing.T) {
		steps := []ProofTraceStep{
			{OutputHash: "aaa"},
			{OutputHash: "bbb"},
		}
		var sb strings.Builder
		sb.WriteString("src")
		sb.WriteString("aaa")
		sb.WriteString("bbb")
		sum := sha256.Sum256([]byte(sb.String()))
		want := hex.EncodeToString(sum[:])
		assert.Equal(t, want, computeChainHash("src", steps))
	})

	t.Run("different sourceDigest produces different hash", func(t *testing.T) {
		h1 := computeChainHash("rev1", nil)
		h2 := computeChainHash("rev2", nil)
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