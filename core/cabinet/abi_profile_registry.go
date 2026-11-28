package cabinet

import "fmt"

// This file is the ABI-profile admission path (ref 02 §1/§3, 05-admission-reuse.md
// §2): the operator-pinned (id, digest) list, the CICABIPROFv1 signature check,
// and the ABI-01..05/SS-05 semantics (abi_profile_semantics.go). An unknown,
// not-pinned, or unauthenticated profile is rejected — there is no fallback
// (ref 02 §1).

// Registry-level violation codes. These are the ABI-profile instance of the
// common A1/A2 admission result codes (05-admission-reuse.md §2): "ugyanazok;
// a #162 hibakódjai ... az A1/A2 alkódjai" — same family, profile-specific name.
const (
	// CodeProfileUnknown: the profile_id is not in the operator's pinned list.
	CodeProfileUnknown = "PROFILE_UNKNOWN"
	// CodeProfileDigestMismatch: the raw bytes do not hash to the pinned digest for this id.
	CodeProfileDigestMismatch = "PROFILE_DIGEST_MISMATCH"
	// CodeProfileSignatureInvalid: the CICABIPROFv1 signature check failed.
	CodeProfileSignatureInvalid = "PROFILE_SIGNATURE_INVALID"
	// CodeProfileAdmissionUnconfigured: no signature verifier or schema registry is configured; admission fails closed.
	CodeProfileAdmissionUnconfigured = "PROFILE_ADMISSION_UNCONFIGURED"
)

// PinnedProfile is one entry of the operator's pinned profile list
// (05-admission-reuse.md §2: "a bundle accepted_profiles és az operátori policy
// (id, digest) párokat enged be").
type PinnedProfile struct {
	ID     string
	Digest string
}

// ProfileSignature is the detached signature material a profile artifact
// carries alongside its bytes, for the CICABIPROFv1 domain (ref 02 §3).
type ProfileSignature struct {
	SignerKeyID string
	SignerAlg   string
	Signature   []byte
}

// ProfileSignatureVerifier checks a profile's CICABIPROFv1 signed domain
// against the admission trust bundle's authority bindings.
//
// This is explicitly the WP-02 admission mechanism's job (job input: "a
// CICABIPROFv1 aláírt tartomány verifikációja a WP-02 admission-mechanizmusán
// át (ha az még nincs kész, interfész mögötti, mockolt trust-bundle, jelölve)")
// — WP-02 has not landed in this repository snapshot (CIC_Relay 86ea1ab), so
// there is no real trust bundle to call here yet. This interface is that seam;
// MockProfileSignatureVerifier (test files only: abi_profile_mock_test.go, never compiled into the production build) is the explicit, clearly-labelled stand-in
// the job input calls for. No production code in this job wires the mock in —
// it exists so ProfileRegistry.Admit's pinning/semantics logic can be tested
// without waiting on WP-02, and so WP-02 can later supply the real
// implementation without any change to ProfileRegistry itself.
type ProfileSignatureVerifier interface {
	VerifySignedProfile(signedBytes []byte, sig ProfileSignature) error
}

// ProfileRegistry is the Relay's ABI-profile loader (ref 02 §1/§3): admission
// (pinning + signature) and semantics (ABI-01..05, SS-05) for every profile a
// module manifest can pin against. The operator's pinned list, signature
// verifier, schema registry and limit caps are supplied at construction, so a
// second (or Nth) profile is admissible purely by adding its pin and schema
// entries to this configuration — no change to ProfileRegistry or
// CheckProfileSemantics (AC-03-1).
type ProfileRegistry struct {
	pins     map[string]string // profile_id -> pinned digest
	verifier ProfileSignatureVerifier
	schemas  SchemaRegistry
	caps     OperatorLimits
}

// NewProfileRegistry constructs a ProfileRegistry from the operator's pinned
// profile list, signature verifier, schema registry and limit caps.
func NewProfileRegistry(pins []PinnedProfile, verifier ProfileSignatureVerifier, schemas SchemaRegistry, caps OperatorLimits) *ProfileRegistry {
	m := make(map[string]string, len(pins))
	for _, p := range pins {
		m[p.ID] = p.Digest
	}
	return &ProfileRegistry{pins: m, verifier: verifier, schemas: schemas, caps: caps}
}

// Admit is the Relay's ABI-profile admission decision: parse, pin-check,
// signature-check, semantics-check, in that order — the first failure is
// returned and nothing later runs. On success it returns the parsed profile
// and its digest (ABI-05: sha256 of raw). sig is the profile artifact's
// detached CICABIPROFv1 signature material.
func (r *ProfileRegistry) Admit(raw []byte, sig ProfileSignature) (*AbiProfile, string, error) {
	digest := ProfileDigest(raw)

	p, err := ParseProfile(raw)
	if err != nil {
		return nil, digest, err
	}

	pinnedDigest, known := r.pins[p.ProfileID]
	if !known {
		return nil, digest, &ABIProfileError{Code: CodeProfileUnknown}
	}
	if pinnedDigest != digest {
		return nil, digest, &ABIProfileError{Code: CodeProfileDigestMismatch}
	}

	if r.verifier == nil || r.schemas == nil {
		return nil, digest, &ABIProfileError{Code: CodeProfileAdmissionUnconfigured}
	}
	signedBytes := ProfileSignedBytes(raw, sig.SignerKeyID, sig.SignerAlg)
	if err := r.verifier.VerifySignedProfile(signedBytes, sig); err != nil {
		return nil, digest, fmt.Errorf("%w: %s: %v", ErrInvalid, CodeProfileSignatureInvalid, err)
	}

	if err := CheckProfileSemantics(p, r.schemas, r.caps); err != nil {
		return nil, digest, err
	}

	return p, digest, nil
}
