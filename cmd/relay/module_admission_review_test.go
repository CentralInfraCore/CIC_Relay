package main

// Review round 1 on #233 (F1-F4): the strict wire reading of the envelope and of the authority bindings, the rule
// that a failed authenticated admission is rejected and not downgraded, and the consistency of the executed wasm's
// imports with the manifest declaration.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// F1: the envelope is read strictly. The same bytes must mean the same thing to the producer and to the consumer.
func TestAdmissionReview_F1_StrictEnvelopeReading(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, nil)
	good := string(f.fsys["m"+envelopeSuffix].Data)
	_, err := parseEnvelope([]byte(good))
	require.NoError(t, err, "the unmodified envelope parses")
	first := strings.Index(good, `"kind"`)
	require.GreaterOrEqual(t, first, 0)
	cases := []struct{ name, raw, want string }{
		{"duplicate key", strings.Replace(good, `"kind"`, `"kind":"ModuleReleaseEnvelope","kind"`, 1), "duplicate key"},
		{"case-variant key", strings.Replace(good, `"kind"`, `"Kind"`, 1), "unknown or differently spelled"},
		{"unknown key", strings.Replace(good, `"kind"`, `"extra":1,"kind"`, 1), "unknown or differently spelled"},
		{"missing required key", strings.Replace(good, `"envelope_version"`, `"envelope_versionx"`, 1), "unknown or differently spelled"},
		{"trailing data", good + `{}`, "after the JSON value"},
		{"not an object", `[]`, "an object is required"},
		{"duplicate key in signer", strings.Replace(good, `"alg"`, `"alg":"ed25519","alg"`, 1), "duplicate key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.NotEqual(t, good, c.raw, "the mutation did not apply")
			_, err := parseEnvelope([]byte(c.raw))
			require.Error(t, err)
			assert.Equal(t, codeEnvelopeInvalid, admissionCode(err))
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

// F4: the authority bindings are read with the same strictness, including the end of the input.
func TestAdmissionReview_F4_StrictAuthoritiesReading(t *testing.T) {
	key := "sha256:" + strings.Repeat("ab", 32)
	good := `{"kind":"ModuleSignAuthorities","version":1,"authorities":[{"key_id":"` + key + `","roles":["module:sign"]}]}`
	m, err := parseModuleSignAuthorities([]byte(good))
	require.NoError(t, err)
	assert.True(t, m[key])
	cases := []struct{ name, raw, want string }{
		{"duplicate top-level key", strings.Replace(good, `"version":1`, `"version":1,"version":1`, 1), "duplicate key"},
		{"case-variant key", strings.Replace(good, `"kind"`, `"Kind"`, 1), "unknown or differently spelled"},
		{"unknown field", strings.Replace(good, `"version":1`, `"version":1,"x":1`, 1), "unknown or differently spelled"},
		{"trailing data", good + ` {"kind":"ModuleSignAuthorities"}`, "after the JSON value"},
		{"missing roles", strings.Replace(good, `,"roles":["module:sign"]`, ``, 1), "required key"},
		{"duplicate key_id", strings.Replace(good, `}]}`, `},{"key_id":"`+key+`","roles":["module:sign"]}]}`, 1), "duplicate key_id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.NotEqual(t, good, c.raw, "the mutation did not apply")
			_, err := parseModuleSignAuthorities([]byte(c.raw))
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

// F2: an envelope that verifies but whose signer chain has no CICSourceCA provenance is a FAILED authenticated
// admission: rejected, never quietly downgraded to transitional (D-1). The legacy envelope-less transitional path is
// a different case and is unchanged (see TestAdmission_AC07_MissingSignatureInProd).
func TestAdmissionReview_F2_FailedProvenanceIsRejectedNotTransitional(t *testing.T) {
	for _, mode := range []string{TrustModeProd, TrustModeDev} {
		t.Run(mode, func(t *testing.T) {
			f := newAdmFixture(t, ociFlowManifest, nil)
			f.assets = f.signer.authorize(loadedPKIWithoutSource(t))
			f.assets.TrustMode = mode
			requireRejected(t, f, codeProvenanceUnverified)
		})
	}
	t.Run("a git-only module with a verified envelope but no provenance is rejected too", func(t *testing.T) {
		f := newAdmFixture(t, gitOnlyManifest, nil)
		f.assets = f.signer.authorize(loadedPKIWithoutSource(t))
		f.assets.TrustMode = TrustModeProd
		requireRejected(t, f, codeProvenanceUnverified)
	})
}

// wasmImporting builds a minimal wasm module that imports one function (module/name) of type () -> ().
func wasmImporting(module, name string) []byte {
	imp := []byte{0x01, byte(len(module))}
	imp = append(imp, module...)
	imp = append(imp, byte(len(name)))
	imp = append(imp, name...)
	imp = append(imp, 0x00, 0x00) // kind: function, type index 0
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type section: one func type () -> ()
		0x02, byte(len(imp))} // import section
	return append(out, imp...)
}

const cicFlowSignOnlyManifest = `abi:
  imports:
    - module: cic-flow
      functions: [sign]
capability_manifest:
  egress:
    - host: "*.oraclecloud.com"
  secrets:
    - handle: "oci/*"
      ops: [sign]
`

// F3: what the wasm actually imports must be declared. A declaration alone decided V6 and the capability before.
func TestAdmissionReview_F3_ActualImportsMustBeDeclared(t *testing.T) {
	t.Run("cic-flow imported but not declared", func(t *testing.T) {
		f := newAdmFixture(t, gitOnlyManifest, wasmImporting("cic-flow", "sign"))
		requireRejected(t, f, codeImportUndeclared)
	})
	t.Run("a different cic-flow function than the declared one", func(t *testing.T) {
		f := newAdmFixture(t, cicFlowSignOnlyManifest, wasmImporting("cic-flow", "actuate"))
		requireRejected(t, f, codeImportUndeclared)
	})
	t.Run("git imported but not declared", func(t *testing.T) {
		f := newAdmFixture(t, cicFlowSignOnlyManifest, wasmImporting("git", "status"))
		requireRejected(t, f, codeImportUndeclared)
	})
	t.Run("declared and imported: admitted (positive control)", func(t *testing.T) {
		f := newAdmFixture(t, cicFlowSignOnlyManifest, wasmImporting("cic-flow", "sign"))
		_, err := f.load(t)
		require.NoError(t, err)
		assert.True(t, f.registered())
	})
	t.Run("an undeclared import is rejected without an envelope too", func(t *testing.T) {
		f := newAdmFixture(t, gitOnlyManifest, wasmImporting("cic-flow", "sign"))
		delete(f.fsys, "m"+envelopeSuffix)
		delete(f.fsys, "m"+signatureSuffix)
		requireRejected(t, f, codeImportUndeclared)
	})
}
