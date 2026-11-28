package wasmcanon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	header = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	// a custom section named "cic.test" with a one-byte payload, the final O-1 vector's module
	customSection = []byte{0x00, 0x0a, 0x08, 'c', 'i', 'c', '.', 't', 'e', 's', 't', 0xaa}
)

func TestCanonicalizeDropsCustomSections(t *testing.T) {
	raw := append(append([]byte{}, header...), customSection...)
	got, err := Canonicalize(raw)
	require.NoError(t, err)
	assert.Equal(t, header, got)
	assert.NotEqual(t, raw, got, "the raw and the executed bytes are different claims")
}

func TestCanonicalizeLeavesAModuleWithoutCustomSectionsAlone(t *testing.T) {
	got, err := Canonicalize(header)
	require.NoError(t, err)
	assert.Equal(t, header, got)
}

func TestCanonicalizeIsIdempotent(t *testing.T) {
	raw := append(append([]byte{}, header...), customSection...)
	once, err := Canonicalize(raw)
	require.NoError(t, err)
	twice, err := Canonicalize(once)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(once, twice))
}

// A section the pinned library will not decode is refused with the library's own error, wrapped in the relay's text.
func TestCanonicalizeRefusesWhatWabinRefuses(t *testing.T) {
	for name, raw := range map[string][]byte{
		"empty":                {},
		"not wasm":             []byte("not a wasm module"),
		"truncated header":     header[:5],
		"section size too big": append(append([]byte{}, header...), 0x00, 0x05, 0x04, 'n', 'a', 'm', 'e', 0xaa), // declares 5 bytes, carries 6 (the first O-1 vector's defect)
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Canonicalize(raw)
			require.Error(t, err)
			assert.Nil(t, got)
			assert.Contains(t, err.Error(), "failed to decode wasm module with wabin")
		})
	}
}

func TestDigestForm(t *testing.T) {
	assert.Equal(t, "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Digest(nil))
	assert.Equal(t, "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", Digest([]byte("abc")))
}

func TestExecutedDigestIsTheDigestOfTheCanonicalBytes(t *testing.T) {
	raw := append(append([]byte{}, header...), customSection...)
	d, err := ExecutedDigest(raw)
	require.NoError(t, err)
	assert.Equal(t, Digest(header), d)
	assert.NotEqual(t, Digest(raw), d)
	_, err = ExecutedDigest([]byte("x"))
	require.Error(t, err)
}

// The final shared vectors (WP-00): the executed bytes and digest the vectors name are exactly what this package derives
// from the vector's raw module. This is the producer-visible half of the INT-04 compatibility; the relay-side half
// (cmd/relay) reads the same file through the same transformation.
func TestTheSharedVectorsExecutedBytesAreTheTransformation(t *testing.T) {
	b, err := os.ReadFile("../../cmd/relay/testdata/o1-vectors/envelope-vectors.json")
	require.NoError(t, err)
	var v struct {
		Cases []struct {
			ID       string  `json:"id"`
			Expect   *string `json:"expect"`
			Envelope struct {
				Wasm     string `json:"wasm_sha256"`
				Executed string `json:"executed_wasm_sha256"`
			} `json:"envelope"`
			Artifacts struct {
				Raw  string `json:"wasm_raw_b64"`
				Exec string `json:"wasm_executed_b64"`
			} `json:"artifacts"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(b, &v))
	checked := 0
	for _, c := range v.Cases {
		if c.Expect != nil {
			continue // the negative vectors carry deliberately wrong artifacts
		}
		raw, _ := base64.StdEncoding.DecodeString(c.Artifacts.Raw)
		want, _ := base64.StdEncoding.DecodeString(c.Artifacts.Exec)
		got, err := Canonicalize(raw)
		require.NoError(t, err, c.ID)
		assert.Equal(t, want, got, c.ID)
		assert.Equal(t, c.Envelope.Wasm, Digest(raw), c.ID)
		assert.Equal(t, c.Envelope.Executed, Digest(got), c.ID)
		checked++
	}
	require.Equal(t, 2, checked, "one accepted vector per signature algorithm")
}
