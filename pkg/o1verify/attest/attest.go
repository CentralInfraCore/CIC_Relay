// Package attest gives the independent verifier (pkg/o1verify) two identities it can check instead of take on trust
// (O-1 INT-10, AC-V2-13):
//
//   - the recorder's persistence attestation (kind recorder-signature): a signature over the artifact's chain hash by a
//     key that holds recorder:attest in the operator's pinned trust bundle;
//   - the verifier's own result: a signature over the VerificationResult by a key that holds verifier:attest.
//
// In both, the public key travels with the signature and is checked against the key_id (sha256 of its DER SPKI), and the
// AUTHORITY comes from the bundle's role map, never from the presented key: a key in a file is not an authority.
//
// What this does and does not prove. A signed result is ATTRIBUTABLE: an authorised verifier key stands behind exactly this
// result. It does not prove that the declared implementation_digest is the code that ran; that holds only when the
// operator also pins the implementation digests of the released verifier (Pinned), which is what the release pipeline
// (INT-13) supplies. With no pin the outcome says so (ImplementationPinned false) instead of implying it. Like the rest of
// pkg/o1verify this package imports nothing from the producer.
package attest

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"

	"centralrelay/pkg/o1verify"
)

// Signed-domain headers.
const (
	// RecorderDomain heads the recorder attestation's signed bytes.
	RecorderDomain = "CICRECATTv1"
	// VerifierDomain heads the verifier result's signed bytes.
	VerifierDomain = "CICVERRESv1"
)

// Algorithm names, as in the envelope and profile signatures.
const (
	// AlgECDSAP256 is ecdsa over P-256 with sha256, ASN.1 DER signature.
	AlgECDSAP256 = "ecdsa-p256-sha256"
	// AlgEd25519 is ed25519.
	AlgEd25519 = "ed25519"
)

// digestRE is the closed form of a digest: sha256: and 64 lowercase hex.
var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// sha is the sha256:<hex> form of the O-1 digests.
func sha(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }

// keyIdentity parses an SPKI and returns its key_id, algorithm and a verify function.
func keyIdentity(spki []byte) (keyID, alg string, verify func(msg, sig []byte) bool, err error) {
	pub, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		return "", "", nil, fmt.Errorf("the public key cannot be parsed: %v", err)
	}
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if k.Curve != elliptic.P256() {
			return "", "", nil, errors.New("the key is not P-256")
		}
		return sha(spki), AlgECDSAP256, func(msg, sig []byte) bool { d := sha256.Sum256(msg); return ecdsa.VerifyASN1(k, d[:], sig) }, nil
	case ed25519.PublicKey:
		return sha(spki), AlgEd25519, func(msg, sig []byte) bool { return ed25519.Verify(k, msg, sig) }, nil
	}
	return "", "", nil, errors.New("the key type is not in the allowlist")
}

// RecorderSignedBytes is the byte-exact input of the recorder attestation signature.
func RecorderSignedBytes(chainHash, keyID, alg string) []byte {
	return []byte(RecorderDomain + "\nartifact_chain_hash=" + chainHash + "\nsigner_key_id=" + keyID + "\nsigner_alg=" + alg + "\n")
}

// Material is what a recorder attestation is checked with: the signature bytes and the signer's SPKI.
type Material struct {
	Signature []byte
	SPKI      []byte
}

// RecorderVerifier implements o1verify.AttestationVerifier for kind recorder-signature.
type RecorderVerifier struct {
	// Authorities holds, per key_id, whether the bundle grants recorder:attest.
	Authorities map[string]bool
	// Lookup returns the signature material stored under a signature digest; nil refuses everything.
	Lookup func(signatureDigest string) (Material, bool)
}

// VerifyAttestation checks that att binds chainHash: the kind is supported, the signature material exists and hashes to the
// stated signature_digest, the SPKI is the stated key_id, the key holds recorder:attest, and the signature verifies over
// the attestation's signed bytes for exactly this chain hash.
func (v RecorderVerifier) VerifyAttestation(att o1verify.Attestation, chainHash string) error {
	if att.Kind != "recorder-signature" {
		return fmt.Errorf("attestation kind %q is not verifiable here (git-signed-commit needs the commit and its signer)", att.Kind)
	}
	if v.Lookup == nil {
		return errors.New("no signature material source is configured")
	}
	m, ok := v.Lookup(att.SignatureDigest)
	if !ok {
		return errors.New("no signature material for the stated signature_digest")
	}
	if sha(m.Signature) != att.SignatureDigest {
		return errors.New("the signature material does not hash to signature_digest")
	}
	keyID, alg, verify, err := keyIdentity(m.SPKI)
	if err != nil {
		return err
	}
	if keyID != att.SignerKeyID {
		return errors.New("signer_key_id is not the presented key")
	}
	if !v.Authorities[keyID] {
		return fmt.Errorf("signer %s holds no recorder:attest authority in the pinned bundle", keyID)
	}
	if !verify(RecorderSignedBytes(chainHash, keyID, alg), m.Signature) {
		return errors.New("the signature does not verify over this artifact's chain hash")
	}
	return nil
}

// SignedResult is a VerificationResult with the verifier's signature.
type SignedResult struct {
	Result    o1verify.Result
	SignerKey string // key_id
	Alg       string
	Signature []byte
	SPKI      []byte
}

// resultDigest is the digest of the result's JSON encoding (Go's struct-field order; a second implementation would
// need to match it, which is why this domain is versioned).
func resultDigest(r o1verify.Result) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return sha(b), nil
}

// ResultSignedBytes is the byte-exact input of the verifier result signature.
func ResultSignedBytes(r o1verify.Result, keyID, alg string) ([]byte, error) {
	d, err := resultDigest(r)
	if err != nil {
		return nil, err
	}
	return []byte(VerifierDomain + "\nresult_sha256=" + d + "\nsigner_key_id=" + keyID + "\nsigner_alg=" + alg + "\n"), nil
}

// SignResult signs r with sign (which receives ResultSignedBytes) for the key with the given SPKI.
func SignResult(r o1verify.Result, spki []byte, sign func(msg []byte) ([]byte, error)) (*SignedResult, error) {
	keyID, alg, _, err := keyIdentity(spki)
	if err != nil {
		return nil, err
	}
	msg, err := ResultSignedBytes(r, keyID, alg)
	if err != nil {
		return nil, err
	}
	sig, err := sign(msg)
	if err != nil {
		return nil, err
	}
	return &SignedResult{Result: r, SignerKey: keyID, Alg: alg, Signature: sig, SPKI: spki}, nil
}

// Attribution is the outcome of VerifyResult.
type Attribution struct {
	// SignerKeyID is the authorised key that stands behind the result.
	SignerKeyID string
	// ImplementationPinned is true only when the operator pinned implementation digests and the result's one is among them.
	ImplementationPinned bool
}

// VerifyResult checks that sr is a result an authorised verifier key signed: the key is the stated one, it holds
// verifier:attest, the signature verifies, and the declared implementation_digest is well-formed. If pinned is non-empty
// the declared digest must be one of them (else IMPLEMENTATION_NOT_PINNED); if pinned is empty the result is accepted as
// attributable only, and Attribution.ImplementationPinned says so.
func VerifyResult(sr SignedResult, authorities map[string]bool, pinned map[string]bool) (*Attribution, error) {
	keyID, alg, verify, err := keyIdentity(sr.SPKI)
	if err != nil {
		return nil, err
	}
	if keyID != sr.SignerKey {
		return nil, errors.New("signer key_id is not the presented key")
	}
	if alg != sr.Alg {
		return nil, fmt.Errorf("signer alg %s does not match the key type", sr.Alg)
	}
	if !authorities[keyID] {
		return nil, fmt.Errorf("signer %s holds no verifier:attest authority in the pinned bundle", keyID)
	}
	msg, err := ResultSignedBytes(sr.Result, keyID, alg)
	if err != nil {
		return nil, err
	}
	if !verify(msg, sr.Signature) {
		return nil, errors.New("the signature does not verify over this result")
	}
	impl := sr.Result.Verifier.ImplementationDigest
	if !digestRE.MatchString(impl) {
		return nil, errors.New("the result declares no well-formed implementation_digest")
	}
	a := &Attribution{SignerKeyID: keyID}
	if len(pinned) > 0 {
		if !pinned[impl] {
			return nil, errors.New("IMPLEMENTATION_NOT_PINNED: the declared implementation digest is not one the operator pinned")
		}
		a.ImplementationPinned = true
	}
	return a, nil
}

// RuntimeImplementationDigest is the sha256 of the running executable: what a verifier binary declares as its
// implementation_digest. It is still a self-declaration; the operator's pin is what makes it an identity.
func RuntimeImplementationDigest() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(exe) // #nosec G304 -- the process's own executable
	if err != nil {
		return "", err
	}
	return sha(b), nil
}
