package hostsign

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type vectorCase struct {
	ID               string   `json:"id"`
	Records          []Record `json:"records"`
	ExpectDigest     string   `json:"expect_digest"`
	ExpectError      string   `json:"expect_error"`
	ExpectRecordB64s []string `json:"expect_record_bytes_b64"`
}

func loadVectors(t *testing.T) []vectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/chain-and-interaction-vectors.json")
	require.NoError(t, err)
	var f struct {
		Cases []vectorCase `json:"interaction_cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.Cases)
	return f.Cases
}

// AC-SS-09: the list digest on the shared (PROVISIONAL, test-key) vectors is identical to the
// reference value, record by record and as a list; a seq gap or a swap is rejected with the named error.
func TestInteractionVectors(t *testing.T) {
	for _, c := range loadVectors(t) {
		t.Run(c.ID, func(t *testing.T) {
			got, err := InteractionsDigest(c.Records)
			if c.ExpectError != "" {
				require.Error(t, err)
				assert.Equal(t, c.ExpectError, err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.ExpectDigest, got)
			for i, r := range c.Records {
				b, err := RecordBytes(r)
				require.NoError(t, err)
				assert.Equal(t, c.ExpectRecordB64s[i], base64.StdEncoding.EncodeToString(b), "record bytes %d", i)
			}
		})
	}
}

func TestInteractionsDigestRefusals(t *testing.T) {
	c := loadVectors(t)[0]
	r0, r1 := c.Records[0], c.Records[1]
	d, err := InteractionsDigest([]Record{r0, r1})
	require.NoError(t, err)

	// a denied record counts: dropping it changes the digest
	d1, err := InteractionsDigest([]Record{r0})
	require.NoError(t, err)
	assert.NotEqual(t, d, d1)

	bad := r0
	bad.Host = "a\nb"
	_, err = InteractionsDigest([]Record{bad})
	assert.ErrorIs(t, err, ErrBadValue)
	bad.Host = "hé"
	_, err = InteractionsDigest([]Record{bad})
	assert.ErrorIs(t, err, ErrBadValue)

	swapped := r1
	swapped.Seq = 0
	other := r0
	other.Seq = 1
	_, err = InteractionsDigest([]Record{swapped, other})
	assert.NoError(t, err, "seq values are what is checked, so a consistently renumbered list is a different list")
	_, err = InteractionsDigest([]Record{r1, r0})
	assert.ErrorIs(t, err, ErrSeqGapOrOrder)
}
