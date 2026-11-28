package attest

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/o1verify"
)

const chain = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

type edKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	spki []byte
	id   string
}

func newEd(t *testing.T) edKey {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	spki, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	return edKey{pub, priv, spki, sha(spki)}
}

func recorderAttestation(t *testing.T, k edKey, chainHash string) (o1verify.Attestation, Material) {
	sig := ed25519.Sign(k.priv, RecorderSignedBytes(chainHash, k.id, AlgEd25519))
	return o1verify.Attestation{Kind: "recorder-signature", SignerKeyID: k.id, SignatureDigest: sha(sig)}, Material{Signature: sig, SPKI: k.spki}
}

func recVerifier(k edKey, m Material, att o1verify.Attestation, authorised bool) RecorderVerifier {
	return RecorderVerifier{
		Authorities: map[string]bool{k.id: authorised},
		Lookup: func(d string) (Material, bool) {
			if d == att.SignatureDigest {
				return m, true
			}
			return Material{}, false
		},
	}
}

func TestRecorderAttestationPositiveAndEveryNegative(t *testing.T) {
	k := newEd(t)
	att, m := recorderAttestation(t, k, chain)
	require.NoError(t, recVerifier(k, m, att, true).VerifyAttestation(att, chain), "positive control")

	other := newEd(t)
	otherSig := ed25519.Sign(other.priv, RecorderSignedBytes(chain, k.id, AlgEd25519))
	cases := map[string]struct {
		v    RecorderVerifier
		att  o1verify.Attestation
		hash string
		want string
	}{
		"another chain hash":                     {recVerifier(k, m, att, true), att, "sha256:" + strings.Repeat("2", 64), "does not verify over this artifact"},
		"no authority":                           {recVerifier(k, m, att, false), att, chain, "holds no recorder:attest"},
		"unsupported kind":                       {recVerifier(k, m, att, true), o1verify.Attestation{Kind: "git-signed-commit", SignerKeyID: att.SignerKeyID, SignatureDigest: att.SignatureDigest}, chain, "not verifiable here"},
		"no material source":                     {RecorderVerifier{Authorities: map[string]bool{k.id: true}}, att, chain, "no signature material source"},
		"unknown digest":                         {recVerifier(k, m, att, true), o1verify.Attestation{Kind: att.Kind, SignerKeyID: att.SignerKeyID, SignatureDigest: sha([]byte("x"))}, chain, "no signature material"},
		"material swapped for another signature": {recVerifier(k, Material{Signature: otherSig, SPKI: k.spki}, att, true), att, chain, "does not hash to signature_digest"},
		"key_id of another key":                  {recVerifier(k, m, att, true), o1verify.Attestation{Kind: att.Kind, SignerKeyID: other.id, SignatureDigest: att.SignatureDigest}, chain, "is not the presented key"},
		"unparseable key":                        {recVerifier(k, Material{Signature: m.Signature, SPKI: []byte("junk")}, att, true), att, chain, "cannot be parsed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.v.VerifyAttestation(c.att, c.hash)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}

	// a signature by a different key than the one presented (and authorised) does not verify
	forged := Material{Signature: otherSig, SPKI: k.spki}
	forgedAtt := o1verify.Attestation{Kind: att.Kind, SignerKeyID: k.id, SignatureDigest: sha(otherSig)}
	v := RecorderVerifier{Authorities: map[string]bool{k.id: true}, Lookup: func(string) (Material, bool) { return forged, true }}
	require.Error(t, v.VerifyAttestation(forgedAtt, chain))
}

func TestRecorderAttestationWithAnEcdsaKey(t *testing.T) {
	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	spki, _ := x509.MarshalPKIXPublicKey(&ek.PublicKey)
	id := sha(spki)
	d := sha256.Sum256(RecorderSignedBytes(chain, id, AlgECDSAP256))
	sig, err := ecdsa.SignASN1(rand.Reader, ek, d[:])
	require.NoError(t, err)
	att := o1verify.Attestation{Kind: "recorder-signature", SignerKeyID: id, SignatureDigest: sha(sig)}
	v := RecorderVerifier{Authorities: map[string]bool{id: true}, Lookup: func(string) (Material, bool) { return Material{sig, spki}, true }}
	require.NoError(t, v.VerifyAttestation(att, chain))

	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	spki384, _ := x509.MarshalPKIXPublicKey(&p384.PublicKey)
	_, _, _, err = keyIdentity(spki384)
	assert.ErrorContains(t, err, "not P-256")
}

// the attestation verifier plugs into the independent verifier: a persisted claim is believed only through it
func TestTheAttestationVerifierIsTheVerifiersTrustInput(t *testing.T) {
	var _ o1verify.AttestationVerifier = RecorderVerifier{}
}

func resultFixture() o1verify.Result {
	return o1verify.Result{Kind: "VerificationResult", ArtifactVersion: "2.0",
		Verifier: o1verify.VerifierInfo{ID: "o1verify", Version: "1", ImplementationDigest: "sha256:" + strings.Repeat("ab", 32)},
		Subject:  o1verify.Subject{ArtifactChainHash: chain}, EvidenceVerdict: o1verify.VerdictVerified, Acceptance: o1verify.AcceptanceNotEvaluated,
		Dimensions: o1verify.Dimensions{Execution: "SUCCEEDED", Authorization: "NOT_EVALUATED", Evidence: "NOT_REQUIRED", Verification: o1verify.VerificationComplete}}
}

func signed(t *testing.T, k edKey, r o1verify.Result) SignedResult {
	sr, err := SignResult(r, k.spki, func(msg []byte) ([]byte, error) { return ed25519.Sign(k.priv, msg), nil })
	require.NoError(t, err)
	return *sr
}

func TestVerifierResultAttributionAndPinning(t *testing.T) {
	k := newEd(t)
	auth := map[string]bool{k.id: true}
	sr := signed(t, k, resultFixture())

	a, err := VerifyResult(sr, auth, nil)
	require.NoError(t, err)
	assert.Equal(t, k.id, a.SignerKeyID)
	assert.False(t, a.ImplementationPinned, "no pin: attributable, and it says the implementation is not pinned")

	impl := resultFixture().Verifier.ImplementationDigest
	a, err = VerifyResult(sr, auth, map[string]bool{impl: true})
	require.NoError(t, err)
	assert.True(t, a.ImplementationPinned)

	_, err = VerifyResult(sr, auth, map[string]bool{"sha256:" + strings.Repeat("cd", 32): true})
	assert.ErrorContains(t, err, "IMPLEMENTATION_NOT_PINNED")
}

func TestVerifierResultNegatives(t *testing.T) {
	k, other := newEd(t), newEd(t)
	auth := map[string]bool{k.id: true}
	good := signed(t, k, resultFixture())

	tampered := good
	tampered.Result.EvidenceVerdict = o1verify.VerdictNotVerified
	_, err := VerifyResult(tampered, auth, nil)
	assert.ErrorContains(t, err, "does not verify", "a changed verdict breaks the signature")

	tamperedDigest := good
	tamperedDigest.Result.Verifier.ImplementationDigest = "sha256:" + strings.Repeat("ee", 32)
	_, err = VerifyResult(tamperedDigest, auth, nil)
	assert.ErrorContains(t, err, "does not verify", "the declared implementation digest is covered by the signature")

	_, err = VerifyResult(good, map[string]bool{k.id: false}, nil)
	assert.ErrorContains(t, err, "no verifier:attest authority")
	_, err = VerifyResult(good, nil, nil)
	assert.Error(t, err)

	wrongKey := good
	wrongKey.SignerKey = other.id
	_, err = VerifyResult(wrongKey, auth, nil)
	assert.ErrorContains(t, err, "is not the presented key")

	wrongAlg := good
	wrongAlg.Alg = AlgECDSAP256
	_, err = VerifyResult(wrongAlg, auth, nil)
	assert.ErrorContains(t, err, "does not match the key type")

	badSPKI := good
	badSPKI.SPKI = []byte("junk")
	_, err = VerifyResult(badSPKI, auth, nil)
	assert.Error(t, err)

	badImpl := resultFixture()
	badImpl.Verifier.ImplementationDigest = "declared"
	_, err = VerifyResult(signed(t, k, badImpl), auth, nil)
	assert.ErrorContains(t, err, "no well-formed implementation_digest")

	// signed by an unauthorised key that presents its own, consistent SPKI
	rogue := signed(t, other, resultFixture())
	_, err = VerifyResult(rogue, auth, nil)
	assert.ErrorContains(t, err, "no verifier:attest authority")
}

func TestSignResultReportsSignerFailureAndBadKeys(t *testing.T) {
	k := newEd(t)
	_, err := SignResult(resultFixture(), k.spki, func([]byte) ([]byte, error) { return nil, errors.New("hsm down") })
	assert.ErrorContains(t, err, "hsm down")
	_, err = SignResult(resultFixture(), []byte("junk"), func([]byte) ([]byte, error) { return nil, nil })
	assert.Error(t, err)
}

func TestRuntimeImplementationDigestIsTheExecutablesDigest(t *testing.T) {
	d, err := RuntimeImplementationDigest()
	require.NoError(t, err)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, d)
}
