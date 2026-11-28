package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/strictjson"
)

// INT-05: the authenticated ABI-profile binding on the real loadModule path.
//
// A module whose manifest names a profile (abi.profile {id, digest}) is admitted only if
//   - its artifact is authenticated (the envelope verified: the binding is tied to the envelope digest),
//   - the profile artifact (<module>.abi-profile.json) is pinned in the operator's trust bundle (accepted_profiles),
//   - its detached CICABIPROFv1 signature (<module>.abi-profile.sig) verifies under the key in <module>.abi-profile.spki,
//   - that key holds profile:sign in the bundle's authority bindings (a key present in a file is not an authority),
//   - the profile passes the semantic checks (ABI-01..05, SS-05) against the operator's schemas and limits, and
//   - the manifest's operations are a subset of the profile's.
// Any failure rejects the module: there is no fallback to legacy dispatch. A manifest without abi.profile is untouched.
//
// The three sidecar file names and the operator configuration file are implementation choices of this change, not an
// accepted wire contract (ref 02 does not name them); they are listed as such in the integration findings.

const (
	// profileSuffix is the sidecar holding the profile artifact's raw bytes.
	profileSuffix = ".abi-profile.json"
	// profileSigSuffix is the sidecar holding the detached CICABIPROFv1 signature (one base64 line + LF).
	profileSigSuffix = ".abi-profile.sig"
	// profileSPKISuffix is the sidecar holding the signer's DER SubjectPublicKeyInfo.
	profileSPKISuffix = ".abi-profile.spki"
	// abiOperatorConfigEnv is the path of the operator's schema registry and limit caps.
	abiOperatorConfigEnv = "CIC_ABI_OPERATOR_CONFIG"
)

// Result codes of the loader that are not the cabinet's own.
const (
	// codeProfileMissing: the manifest names a profile but a sidecar file is absent.
	codeProfileMissing = "PROFILE_MISSING"
	// codeProfileSignerNotAuthorized: the profile's signer key holds no profile:sign authority in the pinned bundle.
	codeProfileSignerNotAuthorized = "PROFILE_SIGNER_NOT_AUTHORIZED"
	// codeProfileRejected: any other profile admission failure.
	codeProfileRejected = "PROFILE_REJECTED"
)

// profileOperatorConfig is the operator's schema registry and limit caps (ref 02 section 6: the store location is open;
// this is a file the operator controls, loaded at start-up).
type profileOperatorConfig struct {
	Schemas cabinet.SchemaRegistry
	Caps    cabinet.OperatorLimits
}

// profileOperator is the loaded configuration; nil means profile admission is unconfigured (fail-closed).
var profileOperator *profileOperatorConfig

// operatorConfigShape is the exact wire shape of the operator configuration file.
var operatorConfigShape = &strictjson.Shape{
	Keys: map[string]*strictjson.Shape{
		"kind": nil, "version": nil,
		"limits": {Keys: map[string]*strictjson.Shape{"max_request_bytes": nil, "max_response_bytes": nil, "timeout_ms": nil},
			Required: []string{"max_request_bytes", "max_response_bytes", "timeout_ms"}},
		"schemas": {Elem: &strictjson.Shape{Keys: map[string]*strictjson.Shape{"id": nil, "digest": nil, "fields": {Free: true}}, Required: []string{"id", "digest"}}},
	},
	Required: []string{"kind", "version", "limits", "schemas"},
}

// parseProfileOperatorConfig decodes the operator configuration strictly.
func parseProfileOperatorConfig(data []byte) (*profileOperatorConfig, error) {
	if err := strictjson.Scan(data, operatorConfigShape); err != nil {
		return nil, fmt.Errorf("abi operator config: %v", err)
	}
	var f struct {
		Kind    string `json:"kind"`
		Version int    `json:"version"`
		Limits  struct {
			Req  int `json:"max_request_bytes"`
			Resp int `json:"max_response_bytes"`
			Ms   int `json:"timeout_ms"`
		} `json:"limits"`
		Schemas []struct {
			ID     string              `json:"id"`
			Digest string              `json:"digest"`
			Fields map[string][]string `json:"fields"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("abi operator config: %w", err)
	}
	if f.Kind != "AbiOperatorConfig" || f.Version != 1 {
		return nil, fmt.Errorf("abi operator config: kind must be AbiOperatorConfig and version 1")
	}
	if f.Limits.Req <= 0 || f.Limits.Resp <= 0 || f.Limits.Ms <= 0 {
		return nil, fmt.Errorf("abi operator config: every limit cap must be positive")
	}
	reg := cabinet.InMemorySchemaRegistry{}
	for i, s := range f.Schemas {
		if s.ID == "" || !envDigestRE.MatchString(s.Digest) {
			return nil, fmt.Errorf("abi operator config: schemas[%d]: id or digest is malformed", i)
		}
		if _, dup := reg[s.ID]; dup {
			return nil, fmt.Errorf("abi operator config: schemas[%d]: duplicate id", i)
		}
		reg[s.ID] = cabinet.SchemaRegistryEntry{Digest: s.Digest, Fields: s.Fields}
	}
	return &profileOperatorConfig{Schemas: reg, Caps: cabinet.OperatorLimits{MaxRequestBytes: f.Limits.Req, MaxResponseBytes: f.Limits.Resp, TimeoutMs: f.Limits.Ms}}, nil
}

// loadProfileOperatorConfig reads CIC_ABI_OPERATOR_CONFIG into profileOperator. Unset: profile admission stays
// unconfigured and a module that names a profile is rejected. Set but unreadable or invalid: start-up error.
func loadProfileOperatorConfig() error {
	p := strings.TrimSpace(os.Getenv(abiOperatorConfigEnv))
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p) // #nosec G304 -- operator-supplied trust-domain path
	if err != nil {
		return fmt.Errorf("read abi operator config %q: %w", p, err)
	}
	cfg, err := parseProfileOperatorConfig(data)
	if err != nil {
		return err
	}
	profileOperator = cfg
	return nil
}

// bundleProfileVerifier is the production cabinet.ProfileSignatureVerifier: the signature is checked under the key that
// was presented with the profile, and that key must hold profile:sign in the operator's pinned bundle.
type bundleProfileVerifier struct {
	spki        []byte
	authorities map[string]bool
}

// VerifySignedProfile implements cabinet.ProfileSignatureVerifier.
func (v bundleProfileVerifier) VerifySignedProfile(signed []byte, sig cabinet.ProfileSignature) error {
	if got := sha256Ref(v.spki); got != sig.SignerKeyID {
		return fmt.Errorf("signer key_id %s is not the presented key %s", sig.SignerKeyID, got)
	}
	pub, err := x509.ParsePKIXPublicKey(v.spki)
	if err != nil {
		return fmt.Errorf("signer public key cannot be parsed: %v", err)
	}
	var ok bool
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if sig.SignerAlg != algECDSAP256 || k.Curve != elliptic.P256() {
			return fmt.Errorf("signer alg %s does not match the key type", sig.SignerAlg)
		}
		digest := sha256.Sum256(signed)
		ok = ecdsa.VerifyASN1(k, digest[:], sig.Signature)
	case ed25519.PublicKey:
		if sig.SignerAlg != algEd25519 {
			return fmt.Errorf("signer alg %s does not match the key type", sig.SignerAlg)
		}
		ok = ed25519.Verify(k, signed, sig.Signature)
	default:
		return fmt.Errorf("signer key type is not in the allowlist")
	}
	if !ok {
		return fmt.Errorf("the signature does not verify over the CICABIPROFv1 signed bytes")
	}
	if !v.authorities[sig.SignerKeyID] {
		return &profileAuthError{keyID: sig.SignerKeyID}
	}
	return nil
}

// profileAuthError marks a valid signature by a key without profile:sign.
type profileAuthError struct{ keyID string }

// Error renders the unauthorized signer.
func (e *profileAuthError) Error() string {
	return codeProfileSignerNotAuthorized + ": " + e.keyID + " holds no profile:sign authority in the pinned bundle"
}

// profileCode maps an admission error to a result code of the loader.
func profileCode(err error) string {
	if c, ok := cabinet.ABIProfileErrorCode(err); ok {
		return c
	}
	if strings.Contains(err.Error(), codeProfileSignerNotAuthorized) {
		return codeProfileSignerNotAuthorized
	}
	if strings.Contains(err.Error(), cabinet.CodeProfileSignatureInvalid) {
		return cabinet.CodeProfileSignatureInvalid
	}
	return codeProfileRejected
}

// admitModuleProfile runs the profile admission for a module whose manifest names a profile and returns the binding.
// It returns (nil, nil) when the manifest names none. envelopeDigest is the module's authenticated identity; an empty
// one (a module that is not authenticated) is refused.
func admitModuleProfile(fsys fs.FS, yamlPath string, project *moduleProject, mod *cabinet.ModuleDescriptor, envelopeDigest string, pkiAssets *PKIAssets) (*cabinet.ModuleProfileBinding, error) {
	pin := project.ABI.Profile
	if pin.ID == "" && pin.Digest == "" {
		return nil, nil
	}
	if envelopeDigest == "" {
		return nil, reject(codeModuleRejected, "the manifest names ABI profile %q but the module is not authenticated: a profile binds to the envelope digest", pin.ID)
	}
	if profileOperator == nil {
		return nil, reject(cabinet.CodeProfileAdmissionUnconfigured, "the manifest names ABI profile %q but no operator schema registry is configured (%s)", pin.ID, abiOperatorConfigEnv)
	}
	base := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath))
	files := map[string][]byte{}
	for _, suffix := range []string{profileSuffix, profileSigSuffix, profileSPKISuffix} {
		data, err := readOptional(fsys, base+suffix)
		if err != nil {
			return nil, reject(codeProfileRejected, "%s cannot be read: %v", filepath.Base(base+suffix), err)
		}
		if data == nil {
			return nil, reject(codeProfileMissing, "the manifest names ABI profile %q but %s is absent", pin.ID, filepath.Base(base+suffix))
		}
		files[suffix] = data
	}
	rawSig, err := decodeSignatureFile(files[profileSigSuffix])
	if err != nil {
		return nil, reject(codeProfileRejected, "the profile signature file: %v", err)
	}
	spki := files[profileSPKISuffix]
	alg := algEd25519
	if k, perr := x509.ParsePKIXPublicKey(spki); perr == nil {
		if _, isEC := k.(*ecdsa.PublicKey); isEC {
			alg = algECDSAP256
		}
	}
	pins := make([]cabinet.PinnedProfile, 0, len(pkiAssets.AcceptedProfiles))
	for _, p := range pkiAssets.AcceptedProfiles {
		pins = append(pins, cabinet.PinnedProfile{ID: p.ID, Digest: p.Digest})
	}
	reg := cabinet.NewProfileRegistry(pins, bundleProfileVerifier{spki: spki, authorities: pkiAssets.ProfileSignAuthorities}, profileOperator.Schemas, profileOperator.Caps)
	profile, digest, err := reg.Admit(files[profileSuffix], cabinet.ProfileSignature{SignerKeyID: sha256Ref(spki), SignerAlg: alg, Signature: rawSig})
	if err != nil {
		return nil, reject(profileCode(err), "ABI profile %q: %v", pin.ID, err)
	}
	b, err := cabinet.NewModuleProfileBinding(mod, envelopeDigest, profile, digest, cabinet.PinnedProfile{ID: pin.ID, Digest: pin.Digest}, project.ABI.Operations)
	if err != nil {
		return nil, reject(profileCode(err), "ABI profile %q: %v", pin.ID, err)
	}
	return b, nil
}

// abiAllowedEffectsEnv is the operator's effect policy for profile-bound operations: a comma-separated list of effect
// classes (local, external-read, external-write). Unset: no policy, and sign+send operations fail closed in generic
// dispatch. The observation PoC's intended value is "local,external-read"; external-write is a separate operator decision.
const abiAllowedEffectsEnv = "CIC_ABI_ALLOWED_EFFECTS"

// effectPolicyOptionFromEnv returns the cabinet option of the effect policy, or none when unset.
func effectPolicyOptionFromEnv() ([]cabinet.CabinetOption, error) {
	v := strings.TrimSpace(os.Getenv(abiAllowedEffectsEnv))
	if v == "" {
		return nil, nil
	}
	var allowed []string
	for _, e := range strings.Split(v, ",") {
		e = strings.TrimSpace(e)
		switch e {
		case cabinet.EffectLocal, cabinet.EffectExternalRead, cabinet.EffectExternalWrite:
			allowed = append(allowed, e)
		default:
			return nil, fmt.Errorf("%s: unknown effect class %q (want local, external-read, external-write)", abiAllowedEffectsEnv, e)
		}
	}
	return []cabinet.CabinetOption{cabinet.WithEffectPolicy(allowed...)}, nil
}
