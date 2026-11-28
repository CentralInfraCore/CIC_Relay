package main

// The O-1 envelope conformance vectors, run through the relay's verifyEnvelope.
//
// testdata/o1-vectors/envelope-vectors.json is a byte-identical copy of the O-1 preparation bundle's
// vectors/envelope-vectors.wasm-canon-v1.json (sha256 d8a2e058aecb5ed46fbc10185c7e9a130d9e2eef03176fd57df824dfae8887a4), the vector set the WP-00 job (CIC_Relay #231)
// produced and verified against the pinned wasm-canon/v1 transformation (wabin v0.0.0-20230304001439-f6f874872834,
// CoreFeaturesV2). It replaces the earlier provisional set whose wasm_raw was not a valid module. The keys are public
// test constants and sign nothing real. The transformation is normative: if a vector and the relay differ, the vector
// is corrected, the relay's transformation is not bent to it (TestO1EnvelopeVectors_ExecutedIsTheRelaysTransformation).

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/core/cabinet"
	"context"
)

// o1VectorCase is one envelope vector.
type o1VectorCase struct {
	ID            string          `json:"id"`
	Description   string          `json:"description"`
	Envelope      json.RawMessage `json:"envelope"`
	SignatureFile string          `json:"signature_file"`
	SignerSPKIB64 string          `json:"signer_spki_b64"`
	Artifacts     struct {
		WasmRaw      string `json:"wasm_raw_b64"`
		WasmExecuted string `json:"wasm_executed_b64"`
		ManifestRaw  string `json:"manifest_raw_b64"`
	} `json:"artifacts"`
	Expect *string `json:"expect"`
}

// loadO1Vectors reads the provisional vector file.
func loadO1Vectors(t *testing.T) []o1VectorCase {
	t.Helper()
	data, err := os.ReadFile("testdata/o1-vectors/envelope-vectors.json")
	require.NoError(t, err)
	var doc struct {
		Cases []o1VectorCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc.Cases
}

// b64 decodes a vector field.
func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	require.NoError(t, err)
	return b
}

// Every vector gives its expected result code (nil = accepted), with the vectors' own executed representation.
func TestO1EnvelopeVectors_WasmCanonV1(t *testing.T) {
	cases := loadO1Vectors(t)
	require.Len(t, cases, 26, "the vector set has 26 cases")
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			_, err := verifyEnvelope(envelopeInput{
				EnvelopeJSON: c.Envelope, SignatureFile: []byte(c.SignatureFile), SignerSPKI: b64(t, c.SignerSPKIB64),
				RawWasm: b64(t, c.Artifacts.WasmRaw), ExecutedWasm: b64(t, c.Artifacts.WasmExecuted), Manifest: b64(t, c.Artifacts.ManifestRaw),
			})
			if c.Expect == nil {
				require.NoError(t, err, c.Description)
				return
			}
			require.Error(t, err, c.Description)
			assert.Equal(t, *c.Expect, admissionCode(err), "%s: %v", c.Description, err)
		})
	}
}

// The pinned transformation is an assertion, not a measurement: for every vector that is meant to be accepted, the
// executed bytes the vector names are exactly what the relay's own wasm-canon/v1 derives from the vector's raw
// module, and the executed digest in the envelope is that value's digest.
func TestO1EnvelopeVectors_ExecutedIsTheRelaysTransformation(t *testing.T) {
	checked := 0
	for _, c := range loadO1Vectors(t) {
		if c.Expect != nil {
			continue
		}
		raw, want := b64(t, c.Artifacts.WasmRaw), b64(t, c.Artifacts.WasmExecuted)
		d, err := cabinet.NewModuleDescriptor(context.Background(), cabinet.BaseBlock{Name: "v", Version: "1"}, "", nil, nil, raw)
		require.NoError(t, err, "%s: the relay must be able to build a descriptor from the vector's raw module", c.ID)
		assert.Equal(t, sha256Ref(want), sha256Ref(d.WasmCode), "%s: the relay's executed bytes are the vector's", c.ID)
		assert.Equal(t, want, d.WasmCode, "%s", c.ID)
		checked++
	}
	require.Equal(t, 2, checked, "one accepted vector per signature algorithm")
}
