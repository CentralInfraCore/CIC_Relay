package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
)

// #162 artifact admission: the ModuleReleaseEnvelope (CICMODENVv1) binds the raw wasm, the executed (canonical,
// stripped) wasm, the full release manifest and the signer key into one signed identity. The relay verifies it on
// the load path (loadModule) BEFORE any capability is bound, from the very buffers it hashes and builds the module
// from. Contract: decisions/o1-preparation/01-162-implementation-boundary.md V1-V7, ENV-01..03.

// The admission result codes. Every rejection of the envelope path carries exactly one of them (admissionError), so a
// test can tell a rejection for the named reason from a rejection for any other reason.
const (
	// codeEnvelopeMissing: the envelope or its .sig is missing while the other is present.
	codeEnvelopeMissing = "ENVELOPE_MISSING"
	// codeEnvelopeInvalid: the envelope is not a schema-valid ModuleReleaseEnvelope, or names another module.
	codeEnvelopeInvalid = "ENVELOPE_INVALID"
	// codeEnvelopeVersion: envelope_version is not 1.
	codeEnvelopeVersion = "ENVELOPE_VERSION"
	// codeAlgMismatch: signer.alg does not match the certificate key type.
	codeAlgMismatch = "ALG_MISMATCH"
	// codeKeyMismatch: signer.key_id is not sha256 of the certificate's SubjectPublicKeyInfo.
	codeKeyMismatch = "KEY_MISMATCH"
	// codeSigEncoding: the .sig file is not one line of canonical standard base64 ended by LF.
	codeSigEncoding = "SIG_ENCODING"
	// codeSignatureInvalid: the signature does not verify over the CICMODENVv1 signed bytes.
	codeSignatureInvalid = "SIGNATURE_INVALID"
	// codeSignerNotAuthorized: the signer key holds no module:sign authority in the pinned bindings (V2b).
	codeSignerNotAuthorized = "SIGNER_NOT_AUTHORIZED"
	// codeWasmDigestMismatch: the raw wasm is not the one the manifest, buildHash or envelope names.
	codeWasmDigestMismatch = "WASM_DIGEST_MISMATCH"
	// codeExecutedDigestMismatch: the executed (stripped) wasm is not the one the envelope names.
	codeExecutedDigestMismatch = "EXECUTED_DIGEST_MISMATCH"
	// codeManifestDigestMismatch: the release manifest bytes are not the ones the envelope names.
	codeManifestDigestMismatch = "MANIFEST_DIGEST_MISMATCH"
	// codeModuleRejected: a module that must be authenticated is not (V6), or carries signature material without a certificate.
	codeModuleRejected = "MODULE_REJECTED"
)

// The trust states of a loaded module (D-1). authenticated: V1-V7 hold. transitional: a certificate-verified module
// without a (verified) envelope, the pre-#162 path; it is never granted a capability. dev: an unsigned module admitted
// by the explicit dev opt-in; it is never granted a capability and carries no trusted claim.
const (
	// trustAuthenticated: V1-V7 hold: the only state a capability is bound in.
	trustAuthenticated = "authenticated"
	// trustTransitional: certificate-verified, no verified envelope (or no provenance): no capability.
	trustTransitional = "transitional"
	// trustDev: unsigned, admitted by the explicit dev opt-in: no capability, no trusted claim.
	trustDev = "dev"
)

// envelopeDomain is the domain-separation line of the signed bytes; it also names the transformation contract
// wasm-canon/v1 (the relay's existing custom-section strip, core/cabinet stripWasmModuleFunc).
const envelopeDomain = "CICMODENVv1"

// moduleSignRole is the authority a signer key must hold in the operator's pinned authority bindings (V2b).
const moduleSignRole = "module:sign"

// The signature algorithms the envelope may name (ENV-02); any other value is ENVELOPE_INVALID.
const (
	// algECDSAP256: ECDSA P-256 over SHA-256 of the signed bytes, ASN.1 DER signature.
	algECDSAP256 = "ecdsa-p256-sha256"
	// algEd25519: pure Ed25519 over the signed bytes, 64-byte signature.
	algEd25519 = "ed25519"
)

var (
	// envDigestRE is the digest form of the envelope: sha256: and 64 lowercase hex.
	envDigestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// envModuleIDRE is the module_id form: name@semver.
	envModuleIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*@v?[0-9]+\.[0-9]+\.[0-9]+$`)
)

// admissionError is a rejection on the envelope path with its contract result code.
type admissionError struct {
	Code   string
	Detail string
}

// Error renders the code first so that logs and wrapped errors keep the named reason visible.
func (e *admissionError) Error() string { return e.Code + ": " + e.Detail }

// reject builds an admissionError.
func reject(code, format string, args ...any) error {
	return &admissionError{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// admissionCode returns the result code carried by err, or "" if err is not an admission rejection.
func admissionCode(err error) string {
	var ae *admissionError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

// moduleReleaseEnvelope is the wire form of the envelope (schemas/module-release-envelope.schema.json).
type moduleReleaseEnvelope struct {
	Kind               string `json:"kind"`
	EnvelopeVersion    int    `json:"envelope_version"`
	ModuleID           string `json:"module_id"`
	WasmSHA256         string `json:"wasm_sha256"`
	ExecutedWasmSHA256 string `json:"executed_wasm_sha256"`
	ManifestDigest     string `json:"manifest_digest"`
	Signer             struct {
		KeyID string `json:"key_id"`
		Alg   string `json:"alg"`
	} `json:"signer"`
}

// signedBytes is the byte-exact CICMODENVv1 signed domain: the domain line, then the six fields in fixed order, LF
// terminated. The values are validated (validateEnvelopeForm) before this is called, so none contains a line break.
func (e *moduleReleaseEnvelope) signedBytes() []byte {
	var b strings.Builder
	b.WriteString(envelopeDomain + "\n")
	b.WriteString("module_id=" + e.ModuleID + "\n")
	b.WriteString("wasm_sha256=" + e.WasmSHA256 + "\n")
	b.WriteString("executed_wasm_sha256=" + e.ExecutedWasmSHA256 + "\n")
	b.WriteString("manifest_digest=" + e.ManifestDigest + "\n")
	b.WriteString("signer_key_id=" + e.Signer.KeyID + "\n")
	b.WriteString("signer_alg=" + e.Signer.Alg + "\n")
	return []byte(b.String())
}

// sha256Ref returns "sha256:<lower hex>" of b.
func sha256Ref(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// parseEnvelope decodes the envelope file strictly: one JSON object, no unknown field, no trailing data. The version
// is checked first (an unknown version is ENVELOPE_VERSION, not a form error), then the form of every field.
func parseEnvelope(data []byte) (*moduleReleaseEnvelope, error) {
	var probe struct {
		EnvelopeVersion *json.RawMessage `json:"envelope_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, reject(codeEnvelopeInvalid, "envelope is not a JSON object: %v", err)
	}
	if probe.EnvelopeVersion != nil && string(*probe.EnvelopeVersion) != "1" {
		return nil, reject(codeEnvelopeVersion, "unknown envelope_version %s", clipForError(string(*probe.EnvelopeVersion)))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var env moduleReleaseEnvelope
	if err := dec.Decode(&env); err != nil {
		return nil, reject(codeEnvelopeInvalid, "envelope does not match the schema: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, reject(codeEnvelopeInvalid, "trailing data after the envelope object")
	}
	if err := validateEnvelopeForm(&env); err != nil {
		return nil, err
	}
	return &env, nil
}

// validateEnvelopeForm checks every field against the schema: kind, version, module_id form, the four digests in the
// sha256:<64 lower hex> form, and the algorithm allowlist.
func validateEnvelopeForm(env *moduleReleaseEnvelope) error {
	if env.EnvelopeVersion != 1 {
		return reject(codeEnvelopeVersion, "envelope_version must be 1")
	}
	if env.Kind != "ModuleReleaseEnvelope" {
		return reject(codeEnvelopeInvalid, "kind must be ModuleReleaseEnvelope")
	}
	if !envModuleIDRE.MatchString(env.ModuleID) {
		return reject(codeEnvelopeInvalid, "module_id %q is not name@semver", clipForError(env.ModuleID))
	}
	for name, v := range map[string]string{"wasm_sha256": env.WasmSHA256, "executed_wasm_sha256": env.ExecutedWasmSHA256,
		"manifest_digest": env.ManifestDigest, "signer.key_id": env.Signer.KeyID} {
		if !envDigestRE.MatchString(v) {
			return reject(codeEnvelopeInvalid, "%s is not sha256:<64 lowercase hex>", name)
		}
	}
	if env.Signer.Alg != algECDSAP256 && env.Signer.Alg != algEd25519 {
		return reject(codeEnvelopeInvalid, "signer.alg %q is not allowed", clipForError(env.Signer.Alg))
	}
	return nil
}

// decodeSignatureFile accepts exactly one line of RFC 4648 section 4 standard base64 (padded, no whitespace) ended
// by one LF (ENV-02); anything else is SIG_ENCODING.
func decodeSignatureFile(sf []byte) ([]byte, error) {
	s := string(sf)
	if !strings.HasSuffix(s, "\n") {
		return nil, reject(codeSigEncoding, "signature file has no final LF")
	}
	body := s[:len(s)-1]
	if body == "" || strings.ContainsAny(body, "\n\r \t") {
		return nil, reject(codeSigEncoding, "signature file must be one base64 line without whitespace")
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(body)
	if err != nil || base64.StdEncoding.EncodeToString(sig) != body {
		return nil, reject(codeSigEncoding, "signature is not canonical standard base64")
	}
	return sig, nil
}

// envelopeInput is everything the envelope check looks at, taken from the buffers the loader already holds: nothing
// here is read from disk again.
type envelopeInput struct {
	EnvelopeJSON  []byte // the <module>.envelope.json bytes
	SignatureFile []byte // the <module>.sig bytes
	SignerSPKI    []byte // DER SubjectPublicKeyInfo of the signer certificate (the .pem)
	RawWasm       []byte // the wasm bytes that were read once and hashed
	ExecutedWasm  []byte // ModuleDescriptor.WasmCode built from RawWasm: the bytes that will be compiled
	Manifest      []byte // the release manifest bytes (the module .yaml) that were read once and are parsed
}

// verifiedEnvelope is the outcome of a successful envelope check.
type verifiedEnvelope struct {
	Envelope *moduleReleaseEnvelope
	Digest   string // envelope_digest = sha256(CICMODENVv1 signed bytes) (ENV-03)
}

// verifyEnvelope runs V1, V2, V4 (digests) and V5 of the contract in the reference order: version, form, algorithm
// against the key type, key_id against the SPKI, signature encoding, signature, raw wasm digest, executed wasm digest,
// manifest digest. It is pure: the signer authority (V2b), the certificate chain (V3) and buildHash are the loader's.
func verifyEnvelope(in envelopeInput) (*verifiedEnvelope, error) {
	env, err := parseEnvelope(in.EnvelopeJSON)
	if err != nil {
		return nil, err
	}
	pub, err := x509.ParsePKIXPublicKey(in.SignerSPKI)
	if err != nil {
		return nil, reject(codeKeyMismatch, "signer public key cannot be parsed: %v", err)
	}
	ecKey, isEC := pub.(*ecdsa.PublicKey)
	edKey, isEd := pub.(ed25519.PublicKey)
	switch env.Signer.Alg {
	case algECDSAP256:
		if !isEC || ecKey.Curve != elliptic.P256() {
			return nil, reject(codeAlgMismatch, "signer.alg %s does not match the certificate key type", env.Signer.Alg)
		}
	case algEd25519:
		if !isEd {
			return nil, reject(codeAlgMismatch, "signer.alg %s does not match the certificate key type", env.Signer.Alg)
		}
	}
	if got := sha256Ref(in.SignerSPKI); got != env.Signer.KeyID {
		return nil, reject(codeKeyMismatch, "signer.key_id %s is not the certificate key %s", env.Signer.KeyID, got)
	}
	sig, err := decodeSignatureFile(in.SignatureFile)
	if err != nil {
		return nil, err
	}
	signed := env.signedBytes()
	var ok bool
	if isEC {
		digest := sha256.Sum256(signed)
		ok = ecdsa.VerifyASN1(ecKey, digest[:], sig)
	} else {
		ok = ed25519.Verify(edKey, signed, sig)
	}
	if !ok {
		return nil, reject(codeSignatureInvalid, "the signature does not verify over the envelope's signed bytes")
	}
	if got := sha256Ref(in.RawWasm); got != env.WasmSHA256 {
		return nil, reject(codeWasmDigestMismatch, "raw wasm %s != envelope wasm_sha256 %s", got, env.WasmSHA256)
	}
	if got := sha256Ref(in.ExecutedWasm); got != env.ExecutedWasmSHA256 {
		return nil, reject(codeExecutedDigestMismatch, "executed wasm %s != envelope executed_wasm_sha256 %s", got, env.ExecutedWasmSHA256)
	}
	if got := sha256Ref(in.Manifest); got != env.ManifestDigest {
		return nil, reject(codeManifestDigestMismatch, "manifest %s != envelope manifest_digest %s", got, env.ManifestDigest)
	}
	return &verifiedEnvelope{Envelope: env, Digest: sha256Ref(signed)}, nil
}

// signerAuthorized reports whether the operator's pinned authority bindings grant keyID the module:sign role. The
// key's presence in a certificate, even a verified one, is not an authority (V2b).
func signerAuthorized(pkiAssets *PKIAssets, keyID string) bool {
	if pkiAssets == nil || pkiAssets.ModuleSignAuthorities == nil {
		return false
	}
	return pkiAssets.ModuleSignAuthorities[keyID]
}

// admitEnvelope is the loader's full envelope admission for a certificate-verified module: the pure envelope check
// (verifyEnvelope), then the signer authority from the operator's pinned bindings (V2b), the module identity, and the
// equality chain sha256(raw) = envelope.wasm_sha256 = wasmDigest = metadata.buildHash (V4, verifyBuildHash).
func admitEnvelope(in envelopeInput, cert *x509.Certificate, moduleID, wasmDigestHex string, pkiAssets *PKIAssets) (*verifiedEnvelope, error) {
	in.SignerSPKI = cert.RawSubjectPublicKeyInfo
	ve, err := verifyEnvelope(in)
	if err != nil {
		return nil, err
	}
	if !signerAuthorized(pkiAssets, ve.Envelope.Signer.KeyID) {
		return nil, reject(codeSignerNotAuthorized, "signer %s holds no %s authority in the pinned bindings", ve.Envelope.Signer.KeyID, moduleSignRole)
	}
	if ve.Envelope.ModuleID != moduleID {
		return nil, reject(codeEnvelopeInvalid, "envelope module_id %q is not the manifest's %q", clipForError(ve.Envelope.ModuleID), clipForError(moduleID))
	}
	if "sha256:"+wasmDigestHex != ve.Envelope.WasmSHA256 {
		return nil, reject(codeWasmDigestMismatch, "manifest wasmDigest is not the envelope wasm_sha256")
	}
	project, err := parseModuleProject(in.Manifest)
	if err != nil {
		return nil, reject(codeEnvelopeInvalid, "%v", err)
	}
	if err := project.verifyBuildHash(in.RawWasm); err != nil {
		return nil, reject(codeWasmDigestMismatch, "%v", err)
	}
	return ve, nil
}

// importsCICFlow reports whether the release manifest declares a cic-flow import (V6).
func importsCICFlow(manifest []byte) bool {
	p, err := parseModuleProject(manifest)
	if err != nil {
		return true // an unparsable manifest is treated as the strictest case
	}
	return len(p.cicFlowImports()) > 0
}

// envelopeSuffix and signatureSuffix name the detached admission files beside <module>.yaml: the envelope and its
// signature. The certificate stays <module>.pem.
const (
	// envelopeSuffix: the envelope file beside <module>.yaml.
	envelopeSuffix = ".envelope.json"
	// signatureSuffix: the detached signature of the envelope.
	signatureSuffix = ".sig"
)

// readOptional reads one companion file; a missing file is (nil, nil), an unreadable one is an error.
func readOptional(fsys fs.FS, path string) ([]byte, error) {
	data, err := fs.ReadFile(fsys, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// signatureMaterialPresent reports whether an envelope or a .sig exists beside the module manifest.
func signatureMaterialPresent(fsys fs.FS, yamlPath string) (bool, error) {
	base := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath))
	for _, p := range []string{base + envelopeSuffix, base + signatureSuffix} {
		if _, err := fs.Stat(fsys, p); err == nil {
			return true, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

// requiresAuthentication reports whether the V6 rule applies: everywhere except an explicit dev trust mode.
func requiresAuthentication(pkiAssets *PKIAssets) bool {
	return pkiAssets == nil || pkiAssets.TrustMode != TrustModeDev
}

// admitModuleArtifact decides the trust state of a module whose signer certificate already verified against the
// trust store (V3, the #155 path). It is given the buffers loadModule read once: the manifest, the raw wasm and the
// executed wasm of the descriptor built from it.
//
//   - envelope and .sig both present: V1-V5 and V2b must all hold, else the module is rejected with the named code
//     (a present signature always has to verify, in every mode). Then the CICSourceCA provenance decides between
//     authenticated and transitional.
//   - exactly one of them present: ENVELOPE_MISSING.
//   - neither: transitional (the pre-#162 path).
//
// A module that is not authenticated never gets a capability; in prod one that imports cic-flow is rejected (V6,
// MODULE_REJECTED) instead of being loaded without its capability.
func admitModuleArtifact(fsys fs.FS, yamlPath, moduleID, wasmDigestHex string, manifest, rawWasm, executedWasm []byte, cert *x509.Certificate, pkiAssets *PKIAssets, logger *logrus.Entry) (string, string, error) {
	base := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath))
	envData, err := readOptional(fsys, base+envelopeSuffix)
	if err != nil {
		return "", "", reject(codeEnvelopeInvalid, "the envelope cannot be read: %v", err)
	}
	sigFile, err := readOptional(fsys, base+signatureSuffix)
	if err != nil {
		return "", "", reject(codeSigEncoding, "the envelope signature cannot be read: %v", err)
	}
	state, digest := trustTransitional, ""
	switch {
	case envData == nil && sigFile == nil:
		// no envelope: the pre-#162 path, transitional
	case envData == nil || sigFile == nil:
		return "", "", reject(codeEnvelopeMissing, "the envelope and its signature must both be present (%s, %s)", filepath.Base(base+envelopeSuffix), filepath.Base(base+signatureSuffix))
	default:
		ve, err := admitEnvelope(envelopeInput{EnvelopeJSON: envData, SignatureFile: sigFile, RawWasm: rawWasm, ExecutedWasm: executedWasm, Manifest: manifest},
			cert, moduleID, wasmDigestHex, pkiAssets)
		if err != nil {
			return "", "", err
		}
		if provErr := verifyCICFlowProvenance(cert, pkiAssets); provErr != nil {
			logger.WithError(provErr).Warn("Envelope verified but provenance not verified: transitional, no capability.")
		} else {
			state, digest = trustAuthenticated, ve.Digest
		}
	}
	if state != trustAuthenticated && requiresAuthentication(pkiAssets) && importsCICFlow(manifest) {
		return "", "", reject(codeModuleRejected, "module imports cic-flow but is %s, not authenticated", state)
	}
	return state, digest, nil
}
