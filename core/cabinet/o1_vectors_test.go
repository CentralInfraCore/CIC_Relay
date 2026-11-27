package cabinet

// O-1 WP-00: second (Go) implementation of the shared O-1 conformance vectors.
//
// The vectors under testdata/o1-vectors are TEST-KEY, PROVISIONAL vectors produced by the
// Python reference (make_o1_vectors.py / build_o1_schemas.py). This file re-implements the
// reference verifier steps independently and checks every case against the expected error
// code. It also measures the pinned wasm-canon/v1 transformation (stripWasmModuleFunc, the
// production wabin decode -> drop custom sections -> encode) against the vectors'
// wasm_executed_b64. Nothing here is a release signature and nothing here is wired into
// the production admission path (that is WP-02).

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pinned digests of the vendored corpus. A change of the corpus must change these constants.
const (
	o1EnvelopeVectorsFile    = "envelope-vectors.json"
	o1EnvelopeVectorsSHA256  = "91f64b74b4e68853be2f0cad2ecf742b975913709ae4d438d95c89b7f19cab51"
	o1CanonVectorsFile       = "envelope-vectors.wasm-canon-v1.json"
	o1CanonVectorsSHA256     = "d8a2e058aecb5ed46fbc10185c7e9a130d9e2eef03176fd57df824dfae8887a4"
	o1ChainVectorsFile       = "chain-and-interaction-vectors.json"
	o1ChainVectorsSHA256     = "a74e7ef447c09ed25f7c0659456d0a22d17e09395a0f0566c9eef7081ca2d7ed"
	o1EnvelopeExpectedCases  = 26
	o1EnvelopeSignedDomain   = "CICMODENVv1"
	o1ProfileSignedDomain    = "CICABIPROFv1"
	o1ChainDomain            = "CICCHAINv2"
	o1HostInteractionsDomain = "CICHOSTINTv1"
)

// Error codes of the reference verifier (reference_verify in make_o1_vectors.py).
const (
	codeEnvelopeVersion        = "ENVELOPE_VERSION"
	codeEnvelopeInvalid        = "ENVELOPE_INVALID"
	codeAlgMismatch            = "ALG_MISMATCH"
	codeKeyMismatch            = "KEY_MISMATCH"
	codeSigEncoding            = "SIG_ENCODING"
	codeSignatureInvalid       = "SIGNATURE_INVALID"
	codeWasmDigestMismatch     = "WASM_DIGEST_MISMATCH"
	codeExecutedDigestMismatch = "EXECUTED_DIGEST_MISMATCH"
	codeManifestDigestMismatch = "MANIFEST_DIGEST_MISMATCH"
	// codeKeyUnparsable is NOT a reference code: the reference raises an uncaught exception
	// on an unparsable SPKI. No vector exercises it.
	codeKeyUnparsable = "KEY_UNPARSABLE"
)

const (
	algECDSAP256 = "ecdsa-p256-sha256"
	algEd25519   = "ed25519"
)

// ---------------------------------------------------------------- corpus loading

type o1EnvelopeCase struct {
	ID            string            `json:"id"`
	Description   string            `json:"description"`
	Envelope      map[string]any    `json:"envelope"`
	SignatureFile string            `json:"signature_file"`
	SignerSPKIB64 string            `json:"signer_spki_b64"`
	Artifacts     map[string]string `json:"artifacts"`
	Expect        *string           `json:"expect"`
}

type o1EnvelopeVectors struct {
	Kind          string            `json:"kind"`
	Version       int               `json:"version"`
	SignedDomain  string            `json:"signed_domain"`
	KeyIDs        map[string]string `json:"key_ids"`
	Cases         []o1EnvelopeCase  `json:"cases"`
	WasmCanon     string            `json:"wasm_canon"`
	CorrectionOf  string            `json:"correction_of"`
	WasmRawSource string            `json:"wasm_raw_note"`
}

func o1ReadPinned(t *testing.T, name, wantSHA256 string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "o1-vectors", name))
	require.NoError(t, err)
	sum := sha256.Sum256(b)
	require.Equal(t, wantSHA256, hex.EncodeToString(sum[:]), "vendored corpus %s changed: the pinned digest must be updated deliberately", name)
	return b
}

func o1DecodeJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	require.NoError(t, dec.Decode(v))
}

func o1LoadEnvelopeVectors(t *testing.T, name, sum string) o1EnvelopeVectors {
	t.Helper()
	var v o1EnvelopeVectors
	o1DecodeJSON(t, o1ReadPinned(t, name, sum), &v)
	require.Equal(t, "O1ConformanceVectors/envelope", v.Kind)
	require.Equal(t, o1EnvelopeSignedDomain, v.SignedDomain)
	return v
}

// ---------------------------------------------------------------- CICMODENVv1 (Go port of envelope_signed_bytes / parse_signed_bytes)

var (
	o1EnvFields = []string{"module_id", "wasm_sha256", "executed_wasm_sha256", "manifest_digest", "signer_key_id", "signer_alg"}
	o1DigestRe  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	o1ModuleRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*@v?[0-9]+\.[0-9]+\.[0-9]+$`)
)

func o1Str(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("missing field %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q is not a string", key)
	}
	return s, nil
}

// o1EnvelopeSignedBytes builds the CICMODENVv1 signed domain of an envelope.
func o1EnvelopeSignedBytes(env map[string]any) ([]byte, error) {
	signer, ok := env["signer"].(map[string]any)
	if !ok {
		return nil, errors.New("missing signer")
	}
	vals := make([]string, 0, 6)
	for _, k := range []string{"module_id", "wasm_sha256", "executed_wasm_sha256", "manifest_digest"} {
		s, err := o1Str(env, k)
		if err != nil {
			return nil, err
		}
		vals = append(vals, s)
	}
	for _, k := range []string{"key_id", "alg"} {
		s, err := o1Str(signer, k)
		if err != nil {
			return nil, err
		}
		vals = append(vals, s)
	}
	var b strings.Builder
	b.WriteString(o1EnvelopeSignedDomain + "\n")
	for i, k := range o1EnvFields {
		b.WriteString(k + "=" + vals[i] + "\n")
	}
	return []byte(b.String()), nil
}

// o1ParseSignedBytes is the strict parser of the CICMODENVv1 domain; the error string is the
// reference reason (NON_ASCII, CR_PRESENT, ...).
func o1ParseSignedBytes(b []byte) (map[string]string, error) {
	for _, c := range b {
		if c > 0x7f {
			return nil, errors.New("NON_ASCII")
		}
	}
	t := string(b)
	if strings.Contains(t, "\r") {
		return nil, errors.New("CR_PRESENT")
	}
	if !strings.HasSuffix(t, "\n") {
		return nil, errors.New("NO_FINAL_LF")
	}
	lines := strings.Split(t[:len(t)-1], "\n")
	if lines[0] != o1EnvelopeSignedDomain {
		return nil, errors.New("DOMAIN")
	}
	body := lines[1:]
	keys := make([]string, len(body))
	seen := map[string]bool{}
	for i, l := range body {
		keys[i], _, _ = strings.Cut(l, "=")
		if seen[keys[i]] {
			return nil, errors.New("DUPLICATE")
		}
		seen[keys[i]] = true
	}
	allowed := map[string]bool{}
	for _, f := range o1EnvFields {
		allowed[f] = true
	}
	for _, k := range keys {
		if !allowed[k] {
			return nil, errors.New("UNKNOWN_FIELD")
		}
	}
	if strings.Join(keys, "\x00") != strings.Join(o1EnvFields, "\x00") {
		sorted := append([]string(nil), keys...)
		sort.Strings(sorted)
		want := append([]string(nil), o1EnvFields...)
		sort.Strings(want)
		if strings.Join(sorted, "\x00") == strings.Join(want, "\x00") {
			return nil, errors.New("ORDER")
		}
		return nil, errors.New("MISSING_OR_ORDER")
	}
	out := map[string]string{}
	for _, l := range body {
		k, v, _ := strings.Cut(l, "=")
		if v == "" || strings.ContainsAny(v, " \t") {
			return nil, errors.New("VALUE_FORM")
		}
		out[k] = v
	}
	for _, k := range []string{"wasm_sha256", "executed_wasm_sha256", "manifest_digest", "signer_key_id"} {
		if !o1DigestRe.MatchString(out[k]) {
			return nil, errors.New("DIGEST_FORM")
		}
	}
	if out["signer_alg"] != algECDSAP256 && out["signer_alg"] != algEd25519 {
		return nil, errors.New("ALG")
	}
	if !o1ModuleRe.MatchString(out["module_id"]) {
		return nil, errors.New("MODULE_ID_FORM")
	}
	return out, nil
}

func o1SHA(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// o1VerifyEnvelopeCase runs the reference verifier steps, in the reference order:
// version -> form -> alg<->key type -> key_id -> signature encoding -> signature ->
// raw digest -> executed digest -> manifest digest. It returns "" on accept or the error code.
func o1VerifyEnvelopeCase(c o1EnvelopeCase) string {
	env := c.Envelope
	if v, ok := env["envelope_version"].(json.Number); !ok || v.String() != "1" {
		return codeEnvelopeVersion
	}
	signed, err := o1EnvelopeSignedBytes(env)
	if err != nil {
		return codeEnvelopeInvalid
	}
	if _, err := o1ParseSignedBytes(signed); err != nil {
		return codeEnvelopeInvalid
	}
	alg := env["signer"].(map[string]any)["alg"].(string)
	keyID := env["signer"].(map[string]any)["key_id"].(string)

	spki, err := base64.StdEncoding.DecodeString(c.SignerSPKIB64)
	if err != nil {
		return codeKeyUnparsable
	}
	pk, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		return codeKeyUnparsable
	}
	ecKey, isEC := pk.(*ecdsa.PublicKey)
	edKey, isEd := pk.(ed25519.PublicKey)
	// Stricter than the reference: the reference accepts any EC curve for ecdsa-p256-sha256
	// (isinstance EllipticCurvePublicKey); ENV-02 names P-256 only (reported finding).
	isP256 := isEC && ecKey.Curve == elliptic.P256()
	if (alg == algECDSAP256) != isP256 || (alg == algEd25519) != isEd {
		return codeAlgMismatch
	}
	if keyID != o1SHA(spki) {
		return codeKeyMismatch
	}

	sf := c.SignatureFile
	if !strings.HasSuffix(sf, "\n") || strings.Contains(sf[:len(sf)-1], "\n") || strings.Contains(sf, " ") || strings.Contains(sf, "\r") {
		return codeSigEncoding
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(sf[:len(sf)-1])
	if err != nil {
		return codeSigEncoding
	}
	if base64.StdEncoding.EncodeToString(sig) != sf[:len(sf)-1] {
		return codeSigEncoding
	}

	switch alg {
	case algECDSAP256:
		d := sha256.Sum256(signed)
		if !ecdsa.VerifyASN1(ecKey, d[:], sig) {
			return codeSignatureInvalid
		}
	case algEd25519:
		if !ed25519.Verify(edKey, signed, sig) {
			return codeSignatureInvalid
		}
	}

	raw, err1 := base64.StdEncoding.DecodeString(c.Artifacts["wasm_raw_b64"])
	exe, err2 := base64.StdEncoding.DecodeString(c.Artifacts["wasm_executed_b64"])
	man, err3 := base64.StdEncoding.DecodeString(c.Artifacts["manifest_raw_b64"])
	if err1 != nil || err2 != nil || err3 != nil {
		return codeEnvelopeInvalid
	}
	if o1SHA(raw) != env["wasm_sha256"] {
		return codeWasmDigestMismatch
	}
	if o1SHA(exe) != env["executed_wasm_sha256"] {
		return codeExecutedDigestMismatch
	}
	if o1SHA(man) != env["manifest_digest"] {
		return codeManifestDigestMismatch
	}
	return ""
}

func o1Expect(c o1EnvelopeCase) string {
	if c.Expect == nil {
		return ""
	}
	return *c.Expect
}

// ---------------------------------------------------------------- test keys (public test constants, worthless by construction)

func o1TestEd25519Key() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func o1TestECDSAKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	h := sha256.Sum256([]byte("CIC O-1 TEST KEY ecdsa-p256 (public test constant)"))
	d := new(big.Int).SetBytes(h[:])
	d.Mod(d, new(big.Int).Lsh(big.NewInt(1), 255))
	d.Add(d, big.NewInt(1))
	raw := make([]byte, 32)
	d.FillBytes(raw)
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	require.NoError(t, err)
	return k
}

func o1SPKI(t *testing.T, pub any) []byte {
	t.Helper()
	b, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	return b
}

// ---------------------------------------------------------------- AC-00-1: envelope vectors, every case with its expected code

func o1RunEnvelopeVectors(t *testing.T, v o1EnvelopeVectors) {
	require.Len(t, v.Cases, o1EnvelopeExpectedCases)
	ids := map[string]bool{}
	matched := 0
	for _, c := range v.Cases {
		require.False(t, ids[c.ID], "duplicate case id %s", c.ID)
		ids[c.ID] = true
		got, want := o1VerifyEnvelopeCase(c), o1Expect(c)
		if assert.Equal(t, want, got, "case %s (%s): got %q want %q", c.ID, c.Description, got, want) {
			matched++
		}
	}
	t.Logf("envelope vectors matched with the expected code: %d/%d", matched, len(v.Cases))
}

func TestO1EnvelopeVectors(t *testing.T) {
	o1RunEnvelopeVectors(t, o1LoadEnvelopeVectors(t, o1EnvelopeVectorsFile, o1EnvelopeVectorsSHA256))
}

func TestO1EnvelopeVectorsWasmCanonV1(t *testing.T) {
	v := o1LoadEnvelopeVectors(t, o1CanonVectorsFile, o1CanonVectorsSHA256)
	require.Equal(t, "wasm-canon/v1", v.WasmCanon)
	o1RunEnvelopeVectors(t, v)
	// The corrected corpus binds the executed representation to the pinned transformation:
	// for every case that is not deliberately tampered, executed == stripWasmModuleFunc(raw).
	for _, c := range v.Cases {
		if strings.HasSuffix(c.ID, "-wasm-raw-modified") || strings.HasSuffix(c.ID, "-executed-representation-differs") {
			continue
		}
		raw, _ := base64.StdEncoding.DecodeString(c.Artifacts["wasm_raw_b64"])
		exe, _ := base64.StdEncoding.DecodeString(c.Artifacts["wasm_executed_b64"])
		got, err := stripWasmModuleFunc(raw)
		require.NoError(t, err, "case %s", c.ID)
		assert.Equal(t, exe, got, "case %s: executed bytes are not the wasm-canon/v1 output of the raw bytes", c.ID)
	}
}

// The key ids and SPKIs of the vectors derive from the public test constants.
func TestO1TestKeysDeriveTheVectorKeyIDs(t *testing.T) {
	v := o1LoadEnvelopeVectors(t, o1EnvelopeVectorsFile, o1EnvelopeVectorsSHA256)
	ec := o1TestECDSAKey(t)
	ed := o1TestEd25519Key()
	assert.Equal(t, v.KeyIDs[algECDSAP256], o1SHA(o1SPKI(t, &ec.PublicKey)))
	assert.Equal(t, v.KeyIDs[algEd25519], o1SHA(o1SPKI(t, ed.Public())))
}

// Ed25519 is deterministic: the stored signatures are reproduced byte for byte. The four
// signature-encoding/bit-flip cases are checked as "canonical signature + the documented mutation".
func TestO1Ed25519SignaturesReproduceByteForByte(t *testing.T) {
	t.Run("original", func(t *testing.T) {
		o1CheckEd25519Reproduction(t, o1LoadEnvelopeVectors(t, o1EnvelopeVectorsFile, o1EnvelopeVectorsSHA256))
	})
	t.Run("wasm-canon-v1", func(t *testing.T) {
		o1CheckEd25519Reproduction(t, o1LoadEnvelopeVectors(t, o1CanonVectorsFile, o1CanonVectorsSHA256))
	})
}

func o1CheckEd25519Reproduction(t *testing.T, v o1EnvelopeVectors) {
	ed := o1TestEd25519Key()
	byID := map[string]o1EnvelopeCase{}
	for _, c := range v.Cases {
		byID[c.ID] = c
	}
	signedByEd := []string{"alg-declares-ed25519-but-key-is-ecdsa", "alg-not-in-allowlist", "malformed-wasm-digest-form"}
	for _, c := range v.Cases {
		if strings.HasPrefix(c.ID, "ed25519-") {
			signedByEd = append(signedByEd, c.ID)
		}
	}
	require.Len(t, signedByEd, 14)
	for _, id := range signedByEd {
		c := byID[id]
		env := c.Envelope
		if id == "ed25519-envelope-field-tampered" {
			env = byID["ed25519-valid"].Envelope // the signature covers the original envelope
		}
		msg, err := o1EnvelopeSignedBytes(env)
		require.NoError(t, err)
		canon := base64.StdEncoding.EncodeToString(ed25519.Sign(ed, msg)) + "\n"
		var want string
		switch id {
		case "ed25519-signature-bit-flipped":
			want = canon[:3] + map[bool]string{true: "B", false: "A"}[canon[3] == 'A'] + canon[4:]
		case "ed25519-signature-with-whitespace":
			want = canon[:10] + " " + canon[10:]
		case "ed25519-signature-without-final-lf":
			want = canon[:len(canon)-1]
		case "ed25519-signature-two-lines":
			want = canon[:10] + "\n" + canon[10:]
		default:
			want = canon
		}
		assert.Equal(t, want, c.SignatureFile, "case %s", id)
	}
}

// ---------------------------------------------------------------- AC-00-4: ECDSA, two separate requirements

// (a) the fixed (stored) ECDSA signature verifies, deterministically, on every run.
func TestO1ECDSAFixedSignatureVerifies(t *testing.T) {
	t.Run("original", func(t *testing.T) {
		o1CheckECDSAFixed(t, o1LoadEnvelopeVectors(t, o1EnvelopeVectorsFile, o1EnvelopeVectorsSHA256))
	})
	t.Run("wasm-canon-v1", func(t *testing.T) {
		o1CheckECDSAFixed(t, o1LoadEnvelopeVectors(t, o1CanonVectorsFile, o1CanonVectorsSHA256))
	})
}

func o1CheckECDSAFixed(t *testing.T, v o1EnvelopeVectors) {
	var c o1EnvelopeCase
	for _, x := range v.Cases {
		if x.ID == "ecdsa-valid" {
			c = x
		}
	}
	require.Equal(t, "ecdsa-valid", c.ID)
	spki, err := base64.StdEncoding.DecodeString(c.SignerSPKIB64)
	require.NoError(t, err)
	pk, err := x509.ParsePKIXPublicKey(spki)
	require.NoError(t, err)
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(c.SignatureFile, "\n"))
	require.NoError(t, err)
	msg, err := o1EnvelopeSignedBytes(c.Envelope)
	require.NoError(t, err)
	d := sha256.Sum256(msg)
	assert.True(t, ecdsa.VerifyASN1(pk.(*ecdsa.PublicKey), d[:], sig), "stored ECDSA signature must verify")
	assert.Equal(t, "", o1VerifyEnvelopeCase(c))
}

// (b) a fresh ECDSA signature made with the test key verifies; it need not equal the stored one.
func TestO1ECDSAFreshSignatureVerifies(t *testing.T) {
	v := o1LoadEnvelopeVectors(t, o1EnvelopeVectorsFile, o1EnvelopeVectorsSHA256)
	var c o1EnvelopeCase
	for _, x := range v.Cases {
		if x.ID == "ecdsa-valid" {
			c = x
		}
	}
	require.Equal(t, "ecdsa-valid", c.ID)
	key := o1TestECDSAKey(t)
	msg, err := o1EnvelopeSignedBytes(c.Envelope)
	require.NoError(t, err)
	d := sha256.Sum256(msg)
	sig, err := ecdsa.SignASN1(rand.Reader, key, d[:])
	require.NoError(t, err)

	fresh := c
	fresh.SignatureFile = base64.StdEncoding.EncodeToString(sig) + "\n"
	assert.Equal(t, "", o1VerifyEnvelopeCase(fresh), "a fresh signature by the test key must be accepted")
	t.Logf("fresh signature equals the stored one: %v (not required)", fresh.SignatureFile == c.SignatureFile)

	// The fresh signature is bound to the domain: over one changed byte it is SIGNATURE_INVALID.
	other := append([]byte(nil), msg...)
	other[len(other)-2] ^= 0x01
	od := sha256.Sum256(other)
	osig, err := ecdsa.SignASN1(rand.Reader, key, od[:])
	require.NoError(t, err)
	wrong := c
	wrong.SignatureFile = base64.StdEncoding.EncodeToString(osig) + "\n"
	assert.Equal(t, codeSignatureInvalid, o1VerifyEnvelopeCase(wrong))
}

// Stricter than the reference: an EC key on another curve presented for ecdsa-p256-sha256.
func TestO1ECDSAOtherCurveIsAlgMismatch(t *testing.T) {
	v := o1LoadEnvelopeVectors(t, o1EnvelopeVectorsFile, o1EnvelopeVectorsSHA256)
	var c o1EnvelopeCase
	for _, x := range v.Cases {
		if x.ID == "ecdsa-valid" {
			c = x
		}
	}
	k384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	spki := o1SPKI(t, &k384.PublicKey)
	env := map[string]any{}
	for k, val := range c.Envelope {
		env[k] = val
	}
	env["signer"] = map[string]any{"alg": algECDSAP256, "key_id": o1SHA(spki)}
	msg, err := o1EnvelopeSignedBytes(env)
	require.NoError(t, err)
	d := sha256.Sum256(msg)
	sig, err := ecdsa.SignASN1(rand.Reader, k384, d[:])
	require.NoError(t, err)
	c.Envelope = env
	c.SignerSPKIB64 = base64.StdEncoding.EncodeToString(spki)
	c.SignatureFile = base64.StdEncoding.EncodeToString(sig) + "\n"
	assert.Equal(t, codeAlgMismatch, o1VerifyEnvelopeCase(c))
}

// ---------------------------------------------------------------- AC-00-2/3: wasm-canon/v1 measurement (production stripWasmModuleFunc)

var (
	o1WasmHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	// (module (func (export "add") (param i32 i32) (result i32) local.get 0 local.get 1 i32.add))
	o1WasmAddBody = []byte{
		0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f, // type
		0x03, 0x02, 0x01, 0x00, // function
		0x07, 0x07, 0x01, 0x03, 0x61, 0x64, 0x64, 0x00, 0x00, // export "add"
		0x0a, 0x09, 0x01, 0x07, 0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b, // code
	}
)

func o1Custom(name string, payload ...byte) []byte {
	body := append([]byte{byte(len(name))}, []byte(name)...)
	body = append(body, payload...)
	return append([]byte{0x00, byte(len(body))}, body...)
}

func o1Cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// o1ByteLevelStrip is the reference transformation of make_o1_vectors.py: byte-level
// removal of every custom section (id 0), all other bytes kept as they are.
func o1ByteLevelStrip(b []byte) ([]byte, error) {
	if len(b) < 8 {
		return nil, errors.New("short")
	}
	out := append([]byte(nil), b[:8]...)
	i := 8
	for i < len(b) {
		start := i
		id := b[i]
		i++
		var size, shift uint
		for {
			if i >= len(b) {
				return nil, errors.New("truncated size")
			}
			c := b[i]
			i++
			size |= uint(c&0x7f) << shift
			shift += 7
			if c&0x80 == 0 {
				break
			}
		}
		if i+int(size) > len(b) {
			return nil, errors.New("truncated section")
		}
		i += int(size)
		if id != 0 {
			out = append(out, b[start:i]...)
		}
	}
	return out, nil
}

type o1WasmMeasurement struct {
	name string
	raw  []byte
	// pinned measurement of stripWasmModuleFunc on raw ("" = decode error expected)
	wantCanonSHA256 string
	wantErrContains string
	// whether the measured wasm-canon/v1 output equals the reference byte-level strip
	wantEqualsByteLevel bool
	// whether the measured wasm-canon/v1 output is the raw input unchanged
	wantCanonEqualsRaw bool
}

func o1WasmMeasurements(t *testing.T) []o1WasmMeasurement {
	v := o1LoadEnvelopeVectors(t, o1EnvelopeVectorsFile, o1EnvelopeVectorsSHA256)
	vectorRaw, err := base64.StdEncoding.DecodeString(v.Cases[0].Artifacts["wasm_raw_b64"])
	require.NoError(t, err)
	require.Equal(t, o1Cat(o1WasmHeader, []byte{0x00, 0x05, 0x04, 'n', 'a', 'm', 'e', 0xaa}), vectorRaw)
	return []o1WasmMeasurement{
		{name: "M0 vector minimal: header + custom \"name\" (payload 0xaa)", raw: vectorRaw,
			wantErrContains: "failed to decode wasm module with wabin: get size of section unknown: EOF"},
		{name: "M1 empty module: header only", raw: o1WasmHeader,
			wantCanonSHA256: "sha256:93a44bbb96c751218e4c00d479e4c14358122a389acca16205b1e4d0dc5f9476", wantEqualsByteLevel: true, wantCanonEqualsRaw: true},
		{name: "M2 header + three non-name custom sections", raw: o1Cat(o1WasmHeader, o1Custom("producers", 0x00), o1Custom("cic.test", 0xaa), o1Custom("x", 0x01)),
			wantCanonSHA256: "sha256:93a44bbb96c751218e4c00d479e4c14358122a389acca16205b1e4d0dc5f9476", wantEqualsByteLevel: true},
		{name: "M2b header + custom section with an empty payload (valid per the core spec)", raw: o1Cat(o1WasmHeader, o1Custom("x")),
			wantErrContains: "failed to read custom section name[x]: EOF"},
		{name: "M3 add module, custom sections before and after the code", raw: o1Cat(o1WasmHeader, o1Custom("a", 1), o1WasmAddBody, o1Custom("producers", 1, 1, 'A')),
			wantCanonSHA256: "sha256:f61fd62f57c41269c3c23f360eeaf1090b1db9c38651106674d48bc65dba88ba", wantEqualsByteLevel: true},
		{name: "M4 add module + valid name section (module name \"m\")", raw: o1Cat(o1WasmHeader, o1WasmAddBody, o1Custom("name", 0x00, 0x02, 0x01, 'm')),
			wantCanonSHA256: "sha256:ea9c97adf2dca378288f124ac6803c7790e10e38a31a89f36cc2237a92a9540d", wantEqualsByteLevel: false, wantCanonEqualsRaw: true},
		{name: "M5 add module, type section size as non-minimal LEB128 (0x87 0x00)", raw: o1Cat(o1WasmHeader, []byte{0x01, 0x87, 0x00}, o1WasmAddBody[2:9], o1WasmAddBody[9:]),
			wantCanonSHA256: "sha256:f61fd62f57c41269c3c23f360eeaf1090b1db9c38651106674d48bc65dba88ba", wantEqualsByteLevel: false},
		{name: "M6 header + custom \"cic.test\" (payload 0xaa): corrected-vector candidate", raw: o1Cat(o1WasmHeader, o1Custom("cic.test", 0xaa)),
			wantCanonSHA256: "sha256:93a44bbb96c751218e4c00d479e4c14358122a389acca16205b1e4d0dc5f9476", wantEqualsByteLevel: true},
	}
}

// TestO1WasmCanonV1Measurement measures the production stripWasmModuleFunc (pinned wabin,
// CoreFeaturesV2) on the vector module and six further inputs and pins the result.
func TestO1WasmCanonV1Measurement(t *testing.T) {
	for _, m := range o1WasmMeasurements(t) {
		t.Run(m.name, func(t *testing.T) {
			canon, err := stripWasmModuleFunc(m.raw)
			byteLevel, blErr := o1ByteLevelStrip(m.raw)
			t.Logf("raw        %s %x", o1SHA(m.raw), m.raw)
			if blErr != nil {
				t.Logf("byte-level error: %v", blErr)
			} else {
				t.Logf("byte-level %s %x", o1SHA(byteLevel), byteLevel)
			}
			if err != nil {
				t.Logf("wasm-canon/v1 error: %v", err)
				require.NotEmpty(t, m.wantErrContains, "unexpected decode error")
				assert.Contains(t, err.Error(), m.wantErrContains)
				return
			}
			t.Logf("canon      %s %x", o1SHA(canon), canon)
			require.Empty(t, m.wantErrContains, "expected a decode error")
			assert.Equal(t, m.wantCanonSHA256, o1SHA(canon))
			require.NoError(t, blErr)
			assert.Equal(t, m.wantEqualsByteLevel, bytes.Equal(canon, byteLevel), "equality with the reference byte-level strip")
			assert.Equal(t, m.wantCanonEqualsRaw, bytes.Equal(canon, m.raw), "canon output equals the raw input")
		})
	}
}

// The original vectors' wasm_executed_b64 is not the wasm-canon/v1 output of their raw module.
func TestO1OriginalVectorExecutedIsNotWasmCanonV1(t *testing.T) {
	v := o1LoadEnvelopeVectors(t, o1EnvelopeVectorsFile, o1EnvelopeVectorsSHA256)
	raw, _ := base64.StdEncoding.DecodeString(v.Cases[0].Artifacts["wasm_raw_b64"])
	_, err := stripWasmModuleFunc(raw)
	require.Error(t, err, "the vector raw module is expected to be rejected by the pinned wabin decoder")
	assert.Contains(t, err.Error(), "get size of section unknown: EOF")
	// the declared custom section size (5) covers only the name; 0xaa is read as the next section id
	// and its size is missing (EOF)
	_, err = o1ByteLevelStrip(raw)
	assert.EqualError(t, err, "truncated size")
}

// ---------------------------------------------------------------- AC-00-5: CICCHAINv2, CICHOSTINTv1, CICABIPROFv1

var (
	o1StepCommon = []string{"index", "requested.module_id", "requested.operation", "requested.presented_artifact_digest", "outcome.execution", "outcome.authorization",
		"input_digest", "output_digest", "references.execution_context.digest", "references.authorization_decision.digest",
		"references.host_interactions.digest", "references.host_interactions.count"}
	o1ExecFields = []string{"artifact_digest", "executed_digest", "manifest_digest", "envelope_digest", "profile.id", "profile.digest", "dispatched", "trust_state"}
	o1RecFields  = []string{"interaction_id", "seq", "decision", "code", "method", "host", "path_sha256", "request_body_sha256", "status", "response_body_sha256", "policy_digest", "profile_digest", "signing_profile_digest"}
	errBadValue  = errors.New("BAD_VALUE")
	errSeqGap    = errors.New("SEQ_GAP_OR_ORDER")
	errBadType   = errors.New("UNSUPPORTED_VALUE_TYPE")
)

func o1Get(o any, path string) any {
	for _, p := range strings.Split(path, ".") {
		m, ok := o.(map[string]any)
		if !ok {
			return nil
		}
		if o, ok = m[p]; !ok {
			return nil
		}
	}
	return o
}

// o1Value renders a scalar like the reference (_cv / _val): null literal, decimal integers,
// strings as-is; a value with CR/LF or a non-ASCII byte is rejected. Booleans and containers are
// not defined by the normative text and are rejected (the reference would render Python's str()).
func o1Value(v any) (string, error) {
	var s string
	switch x := v.(type) {
	case nil:
		return "null", nil
	case string:
		s = x
	case json.Number:
		if _, err := x.Int64(); err != nil {
			return "", errBadType
		}
		s = x.String()
	default:
		return "", errBadType
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", errBadValue
	}
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return "", errBadValue
		}
	}
	return s, nil
}

func o1Required(m map[string]any, path string) (any, error) {
	cur := any(m)
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("missing %s", path)
		}
		if cur, ok = mm[p]; !ok {
			return nil, fmt.Errorf("missing %s", path)
		}
	}
	return cur, nil
}

func o1ChainBytes(a map[string]any) ([]byte, error) {
	var L []string
	add := func(k string, v any) error {
		s, err := o1Value(v)
		if err != nil {
			return err
		}
		L = append(L, k+"="+s)
		return nil
	}
	L = append(L, o1ChainDomain)
	for _, p := range []string{"version", "workflow.id", "workflow.source_digest", "outcome.execution", "outcome.authorization"} {
		v, err := o1Required(a, p)
		if err != nil {
			return nil, err
		}
		if err := add(p, v); err != nil {
			return nil, err
		}
	}
	steps, ok := a["steps"].([]any)
	if !ok {
		return nil, errors.New("missing steps")
	}
	L = append(L, fmt.Sprintf("steps=%d", len(steps)))
	for _, st := range steps {
		s := st.(map[string]any)
		idx, err := o1Value(s["index"])
		if err != nil {
			return nil, err
		}
		name, ok := s["name"].(string)
		if !ok {
			return nil, errors.New("missing step name")
		}
		L = append(L, "step."+idx+".name_sha256="+o1SHA([]byte(name)))
		for _, f := range o1StepCommon {
			if err := add("step."+idx+"."+f, o1Get(s, f)); err != nil {
				return nil, err
			}
		}
		if rej, present := s["rejection"]; present {
			L = append(L, "step."+idx+".rejection=present")
			r, _ := rej.(map[string]any)
			for _, f := range []string{"phase", "code"} {
				v, ok := r[f]
				if !ok {
					return nil, fmt.Errorf("missing rejection.%s", f)
				}
				if err := add("step."+idx+".rejection."+f, v); err != nil {
					return nil, err
				}
			}
		} else {
			L = append(L, "step."+idx+".rejection=absent")
		}
		if exe, present := s["executed"]; present {
			L = append(L, "step."+idx+".executed=present")
			for _, f := range o1ExecFields {
				if err := add("step."+idx+".executed."+f, o1Get(exe, f)); err != nil {
					return nil, err
				}
			}
		} else {
			L = append(L, "step."+idx+".executed=absent")
		}
	}
	return []byte(strings.Join(L, "\n") + "\n"), nil
}

func o1RecordBytes(r map[string]any) ([]byte, error) {
	var L []string
	for _, k := range o1RecFields {
		v, ok := r[k]
		if !ok {
			return nil, fmt.Errorf("missing %s", k)
		}
		s, err := o1Value(v)
		if err != nil {
			return nil, err
		}
		L = append(L, k+"="+s)
	}
	intent, ok := r["intent"].(map[string]any)
	if !ok {
		return nil, errors.New("missing intent")
	}
	for _, k := range []string{"operation", "resource_id", "binding_digest"} {
		v, ok := intent[k]
		if !ok {
			return nil, fmt.Errorf("missing intent.%s", k)
		}
		s, err := o1Value(v)
		if err != nil {
			return nil, err
		}
		L = append(L, "intent."+k+"="+s)
	}
	return []byte(strings.Join(L, "\n") + "\n"), nil
}

func o1InteractionsDigest(records []map[string]any) (string, error) {
	for i, r := range records {
		n, ok := r["seq"].(json.Number)
		if !ok {
			return "", errSeqGap
		}
		if s, err := n.Int64(); err != nil || s != int64(i) {
			return "", errSeqGap
		}
	}
	L := []string{o1HostInteractionsDomain, fmt.Sprintf("count=%d", len(records))}
	for i, r := range records {
		rb, err := o1RecordBytes(r)
		if err != nil {
			return "", err
		}
		L = append(L, fmt.Sprintf("%d:%s", i, o1SHA(rb)))
	}
	return o1SHA([]byte(strings.Join(L, "\n") + "\n")), nil
}

func o1ProfileSignedBytes(raw []byte, keyID, alg string) []byte {
	return []byte(o1ProfileSignedDomain + "\nprofile_sha256=" + o1SHA(raw) + "\nsigner_key_id=" + keyID + "\nsigner_alg=" + alg + "\n")
}

type o1ChainVectors struct {
	Kind       string `json:"kind"`
	ChainCases []struct {
		ID                 string         `json:"id"`
		Artifact           map[string]any `json:"artifact"`
		ExpectChainHash    string         `json:"expect_chain_hash"`
		ExpectHashInputB64 string         `json:"expect_hash_input_b64"`
		MustDifferFrom     string         `json:"must_differ_from"`
	} `json:"chain_cases"`
	InteractionCases []struct {
		ID                   string           `json:"id"`
		Records              []map[string]any `json:"records"`
		ExpectDigest         string           `json:"expect_digest"`
		ExpectRecordBytesB64 []string         `json:"expect_record_bytes_b64"`
		ExpectError          string           `json:"expect_error"`
	} `json:"interaction_cases"`
	ProfileCases []struct {
		ID                   string `json:"id"`
		ProfileRawB64        string `json:"profile_raw_b64"`
		SignerKeyID          string `json:"signer_key_id"`
		SignerAlg            string `json:"signer_alg"`
		ExpectSignedBytesB64 string `json:"expect_signed_bytes_b64"`
	} `json:"profile_cases"`
}

func o1LoadChainVectors(t *testing.T) o1ChainVectors {
	t.Helper()
	var v o1ChainVectors
	o1DecodeJSON(t, o1ReadPinned(t, o1ChainVectorsFile, o1ChainVectorsSHA256), &v)
	require.Equal(t, "O1ConformanceVectors/chain-interactions-profile", v.Kind)
	return v
}

func TestO1ChainVectors(t *testing.T) {
	v := o1LoadChainVectors(t)
	require.Len(t, v.ChainCases, 3)
	got := map[string]string{}
	for _, c := range v.ChainCases {
		b, err := o1ChainBytes(c.Artifact)
		require.NoError(t, err, c.ID)
		want, err := base64.StdEncoding.DecodeString(c.ExpectHashInputB64)
		require.NoError(t, err)
		assert.Equal(t, string(want), string(b), "case %s: hash input", c.ID)
		assert.Equal(t, c.ExpectChainHash, o1SHA(b), "case %s: chain hash", c.ID)
		got[c.ID] = o1SHA(b)
	}
	for _, c := range v.ChainCases {
		if c.MustDifferFrom != "" {
			assert.NotEqual(t, got[c.MustDifferFrom], got[c.ID], "case %s must differ from %s", c.ID, c.MustDifferFrom)
		}
	}
}

// A value with a line break or a non-ASCII byte is rejected (V2-03), with the named reason.
func TestO1ChainRejectsBadValues(t *testing.T) {
	v := o1LoadChainVectors(t)
	for _, bad := range []string{"poc\nobserve", "poc\robserve", "poc.obsérve"} {
		var a map[string]any
		raw, _ := json.Marshal(v.ChainCases[0].Artifact)
		o1DecodeJSON(t, raw, &a)
		a["workflow"].(map[string]any)["id"] = bad
		_, err := o1ChainBytes(a)
		assert.ErrorIs(t, err, errBadValue, "value %q", bad)
	}
	// free text (step.name) enters only as a digest: UTF-8 is accepted there
	var a map[string]any
	raw, _ := json.Marshal(v.ChainCases[0].Artifact)
	o1DecodeJSON(t, raw, &a)
	a["steps"].([]any)[0].(map[string]any)["name"] = "név\n"
	_, err := o1ChainBytes(a)
	assert.NoError(t, err)
}

func TestO1HostInteractionVectors(t *testing.T) {
	v := o1LoadChainVectors(t)
	require.Len(t, v.InteractionCases, 3)
	for _, c := range v.InteractionCases {
		d, err := o1InteractionsDigest(c.Records)
		if c.ExpectError != "" {
			if assert.Error(t, err, c.ID) {
				assert.Equal(t, c.ExpectError, err.Error(), "case %s: error code", c.ID)
			}
			continue
		}
		require.NoError(t, err, c.ID)
		assert.Equal(t, c.ExpectDigest, d, "case %s: digest", c.ID)
		require.Len(t, c.ExpectRecordBytesB64, len(c.Records))
		for i, r := range c.Records {
			rb, err := o1RecordBytes(r)
			require.NoError(t, err)
			want, _ := base64.StdEncoding.DecodeString(c.ExpectRecordBytesB64[i])
			assert.Equal(t, string(want), string(rb), "case %s record %d", c.ID, i)
		}
	}
	// a record value with a line break is rejected with the named reason
	var r map[string]any
	raw, _ := json.Marshal(v.InteractionCases[0].Records[0])
	o1DecodeJSON(t, raw, &r)
	r["host"] = "a\nb"
	_, err := o1RecordBytes(r)
	assert.ErrorIs(t, err, errBadValue)
}

func TestO1ProfileDomainVector(t *testing.T) {
	v := o1LoadChainVectors(t)
	require.Len(t, v.ProfileCases, 1)
	for _, c := range v.ProfileCases {
		raw, err := base64.StdEncoding.DecodeString(c.ProfileRawB64)
		require.NoError(t, err)
		want, err := base64.StdEncoding.DecodeString(c.ExpectSignedBytesB64)
		require.NoError(t, err)
		assert.Equal(t, string(want), string(o1ProfileSignedBytes(raw, c.SignerKeyID, c.SignerAlg)), c.ID)
	}
}
