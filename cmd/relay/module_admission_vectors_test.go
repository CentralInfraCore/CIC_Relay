package main

// The O-1 envelope conformance vectors, run through the relay's verifyEnvelope.
//
// PROVISIONAL: testdata/o1-vectors/envelope-vectors.json is a byte-identical copy of the O-1 preparation bundle's
// vectors/envelope-vectors.json (sha256 91f64b74b4e68853be2f0cad2ecf742b975913709ae4d438d95c89b7f19cab51), produced
// by the reference implementation only; WP-00 finalises them. The keys are public test constants and sign nothing
// real. The vectors' wasm_executed_b64 is the reference byte-level custom-section removal, NOT the relay's wabin
// re-encode: TestO1EnvelopeVectors_WabinExecutedRepresentation measures whether the two agree.

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
func TestO1EnvelopeVectors_Provisional(t *testing.T) {
	cases := loadO1Vectors(t)
	require.Len(t, cases, 26, "the provisional vector set has 26 cases")
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

// Measures the reference executed representation against the relay's own transformation (wasm-canon/v1, wabin
// re-encode). Not an assertion about the vectors' correctness: it records what the relay derives from wasm_raw.
func TestO1EnvelopeVectors_WabinExecutedRepresentation(t *testing.T) {
	c := loadO1Vectors(t)[0]
	raw, want := b64(t, c.Artifacts.WasmRaw), b64(t, c.Artifacts.WasmExecuted)
	measure := func(label string, raw []byte) {
		d, err := cabinet.NewModuleDescriptor(context.Background(), cabinet.BaseBlock{Name: "v", Version: "1"}, "", nil, nil, raw)
		if err != nil {
			t.Logf("MEASURED %s: raw %x: the relay cannot build a descriptor: %v", label, raw, err)
			return
		}
		t.Logf("MEASURED %s: raw %x -> relay executed %s (%x); reference executed %s; equal=%v",
			label, raw, sha256Ref(d.WasmCode), d.WasmCode, sha256Ref(want), sha256Ref(d.WasmCode) == sha256Ref(want))
	}
	measure("vector wasm_raw", raw)
	// The vector's custom section declares size 5 but carries 6 bytes (name length 1 + "name" + payload 1): the same
	// section with the size corrected, to separate a malformed vector from a transformation difference.
	fixed := append([]byte(nil), raw...)
	if len(fixed) == 16 && fixed[8] == 0x00 && fixed[9] == 0x05 {
		fixed[9] = 0x06
		measure("vector wasm_raw, section size corrected to 6", fixed)
	}
	measure("custom section \"x\" (size 3)", wasmWithCustom)
}
