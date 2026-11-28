package main

// INT-04: the REAL release producer (cic-module-oracle-cloud tools/releaselib, with the executed bytes from the
// wasmcanon command) against the REAL admission. Not a vector file written by hand: tests/int04/make_bundle.py in the
// module repository produces the bundle; this test runs every case through verifyEnvelope. It runs only when
// CIC_INT04_BUNDLE names a bundle, because the producer is another repository's code; the evidence run is
// documented in the O-1 integration package.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInt04_ProducedReleasesThroughAdmission(t *testing.T) {
	path := os.Getenv("CIC_INT04_BUNDLE")
	if path == "" {
		t.Skip("CIC_INT04_BUNDLE is not set: the producer-made bundle is an external input")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc struct {
		Cases []struct {
			ID          string          `json:"id"`
			Expect      *string         `json:"expect"`
			Envelope    json.RawMessage `json:"envelope"`
			Signature   string          `json:"signature_file"`
			SPKI        string          `json:"signer_spki_b64"`
			Raw         string          `json:"raw_b64"`
			Executed    string          `json:"executed_b64"`
			ManifestB64 string          `json:"manifest_b64"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	require.NotEmpty(t, doc.Cases)
	accepted, refused := 0, 0
	for _, c := range doc.Cases {
		t.Run(c.ID, func(t *testing.T) {
			_, err := verifyEnvelope(envelopeInput{
				EnvelopeJSON: c.Envelope, SignatureFile: []byte(c.Signature), SignerSPKI: b64(t, c.SPKI),
				RawWasm: b64(t, c.Raw), ExecutedWasm: b64(t, c.Executed), Manifest: b64(t, c.ManifestB64),
			})
			if c.Expect == nil {
				require.NoError(t, err)
				accepted++
				return
			}
			require.Error(t, err)
			assert.Equal(t, *c.Expect, admissionCode(err), "%v", err)
			refused++
		})
	}
	assert.Equal(t, 2, accepted, "one produced release per signature algorithm")
	assert.Equal(t, 10, refused)
}
