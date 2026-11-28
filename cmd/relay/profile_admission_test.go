package main

// INT-05 acceptance at the PRODUCTION entry point, loadModule: an authenticated module that names an ABI profile is
// admitted only with an authenticated profile binding (trust-bundle pin, profile:sign signer, valid CICABIPROFv1
// signature, semantics, manifest subset). Every negative asserts the named code and that nothing was registered; the
// positive control differs from each negative in exactly the property under test.

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/schemalite"
	"centralrelay/pkg/trustbundle"
)

func dg(s string) string { return "sha256:" + strings.Repeat(s, 32) }

func profileFixtureOperator() *profileOperatorConfig {
	return &profileOperatorConfig{
		Schemas: cabinet.InMemorySchemaRegistry{
			"cic:provider:describe-request": {Digest: dg("ab")}, "cic:provider:module-manifest": {Digest: dg("cd")},
			"cic:provider:observe-request": {Digest: dg("ef")}, "cic:provider:observation": {Digest: dg("12")},
		},
		Caps: cabinet.OperatorLimits{MaxRequestBytes: 1048576, MaxResponseBytes: 1048576, TimeoutMs: 60000},
		Bodies: bodyStore{
			{"cic:provider:describe-request", dg("ab")}: compileBody(`{"type":"object"}`), {"cic:provider:module-manifest", dg("cd")}: compileBody(`{"type":"object"}`),
			{"cic:provider:observe-request", dg("ef")}: compileBody(`{"type":"object"}`), {"cic:provider:observation", dg("12")}: compileBody(`{"type":"object"}`),
		},
	}
}

// compileBody compiles a test schema body.
func compileBody(doc string) *schemalite.Schema {
	s, err := schemalite.Compile([]byte(doc))
	if err != nil {
		panic(err)
	}
	return s
}

func profileJSON(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	ref := func(id, d string) map[string]any { return map[string]any{"id": id, "digest": d} }
	p := map[string]any{
		"kind": "AbiProfile", "profile_version": 1, "profile_id": "cic:provider@0.1.0",
		"required_host_capabilities": []any{"provider-http@1"},
		"limits":                     map[string]any{"max_request_bytes": 1048576, "max_response_bytes": 262144, "timeout_ms": 30000},
		"operations": []any{
			map[string]any{"name": "describe", "host_boundary": "none", "effect": "local",
				"request_schema": ref("cic:provider:describe-request", dg("ab")), "response_schema": ref("cic:provider:module-manifest", dg("cd"))},
			map[string]any{"name": "observe", "host_boundary": "sign+send", "effect": "external-read",
				"request_schema": ref("cic:provider:observe-request", dg("ef")), "response_schema": ref("cic:provider:observation", dg("12"))},
		},
	}
	if mutate != nil {
		mutate(p)
	}
	b, err := json.Marshal(p)
	require.NoError(t, err)
	return b
}

// profFixture is an authenticated module (the AC-162 fixture) whose manifest pins the profile, with the three sidecars
// and a trust context in which the profile is accepted and its signer holds profile:sign.
type profFixture struct {
	*admFixture
	raw    []byte
	digest string
	key    ed25519.PrivateKey
	spki   []byte
	keyID  string
}

func manifestWithProfile(id, digest, ops string) string {
	return "abi:\n  profile:\n    id: " + id + "\n    digest: \"" + digest + "\"\n  operations: " + ops + "\n  imports:\n    - module: git\n      functions: [status]\n" +
		"capability_manifest:\n  git:\n    - repo: infra\n      ops: [status]\n"
}

func newProfFixture(t *testing.T, mutateProfile func(map[string]any), manifestOps string) *profFixture {
	t.Helper()
	profileOperator = profileFixtureOperator()
	t.Cleanup(func() { profileOperator = nil })
	raw := profileJSON(t, mutateProfile)
	digest := cabinet.ProfileDigest(raw)
	f := &profFixture{raw: raw, digest: digest}
	f.admFixture = newAdmFixture(t, manifestWithProfile("cic:provider@0.1.0", digest, manifestOps), nil)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	f.key = priv
	f.spki, err = x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	f.keyID = sha256Ref(f.spki)
	f.writeSidecars(t, raw, priv, f.spki, f.keyID, algEd25519)
	f.assets.AcceptedProfiles = []trustbundle.Profile{{ID: "cic:provider@0.1.0", Digest: digest}}
	f.assets.ProfileSignAuthorities = map[string]bool{f.keyID: true}
	return f
}

func (f *profFixture) writeSidecars(t *testing.T, raw []byte, key ed25519.PrivateKey, spki []byte, keyID, alg string) {
	t.Helper()
	sig := ed25519.Sign(key, cabinet.ProfileSignedBytes(raw, keyID, alg))
	f.fsys["m"+profileSuffix] = &fstest.MapFile{Data: raw}
	f.fsys["m"+profileSigSuffix] = &fstest.MapFile{Data: []byte(base64.StdEncoding.EncodeToString(sig) + "\n")}
	f.fsys["m"+profileSPKISuffix] = &fstest.MapFile{Data: spki}
}

func (f *profFixture) binding() (*cabinet.ModuleProfileBinding, bool) {
	return f.svc.(interface {
		ModuleProfile(string) (*cabinet.ModuleProfileBinding, bool)
	}).ModuleProfile("adm.mod@1.0.0")
}

func TestINT05_PositiveAuthenticatedProfileBinding(t *testing.T) {
	f := newProfFixture(t, nil, "[describe, observe]")
	_, err := f.load(t)
	require.NoError(t, err)
	b, ok := f.binding()
	require.True(t, ok, "the admitted profile is bound to the module")
	assert.Equal(t, "cic:provider@0.1.0", b.ProfileID)
	assert.Equal(t, f.digest, b.ProfileDigest)
	assert.Equal(t, sha256Ref(envelopeFor(t, f.fsys, "m.yaml", f.signer).signedBytes()), b.EnvelopeDigest, "bound to the AUTHENTICATED envelope identity")
	assert.Equal(t, []string{"describe", "observe"}, b.Operations)
	assert.Equal(t, "adm.mod@1.0.0", b.ModuleID)
	assert.True(t, f.registered())

	// INT-09: the admission record the V2 trace will claim as the executed identity
	mod, err := f.svc.GetModule("adm.mod@1.0.0")
	require.NoError(t, err)
	ident, ok := moduleIdentities.get("adm.mod@1.0.0", mod.Checksum())
	require.True(t, ok, "an authenticated module with an admitted profile has an executed identity")
	assert.Equal(t, b.EnvelopeDigest, *ident.EnvelopeDigest)
	assert.Equal(t, sha256Of(mod.WasmCode), ident.ExecutedDigest)
	assert.Equal(t, sha256Of(f.fsys["m.yaml"].Data), ident.ManifestDigest)
	assert.Equal(t, cabinet.ProfileRefV2{ID: b.ProfileID, Digest: b.ProfileDigest}, ident.Profile)
	assert.Equal(t, cabinet.TrustStateAuthenticated, ident.TrustState)
}

func TestINT05_Negatives(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *profFixture)
		code  string
	}{
		{"bundle does not accept the profile", func(t *testing.T, f *profFixture) { f.assets.AcceptedProfiles = nil }, cabinet.CodeProfileUnknown},
		{"bundle pins another digest for the id", func(t *testing.T, f *profFixture) {
			f.assets.AcceptedProfiles = []trustbundle.Profile{{ID: "cic:provider@0.1.0", Digest: dg("99")}}
		}, cabinet.CodeProfileDigestMismatch},
		{"signer has no profile:sign (module:sign is not enough)", func(t *testing.T, f *profFixture) {
			f.assets.ProfileSignAuthorities = map[string]bool{}
			f.assets.ModuleSignAuthorities[f.keyID] = true
		}, codeProfileSignerNotAuthorized},
		{"signer listed without the role", func(t *testing.T, f *profFixture) { f.assets.ProfileSignAuthorities = map[string]bool{f.keyID: false} }, codeProfileSignerNotAuthorized},
		{"no authorities at all", func(t *testing.T, f *profFixture) { f.assets.ProfileSignAuthorities = nil }, codeProfileSignerNotAuthorized},
		{"signature over other bytes", func(t *testing.T, f *profFixture) {
			sig := ed25519.Sign(f.key, []byte("other"))
			f.fsys["m"+profileSigSuffix] = &fstest.MapFile{Data: []byte(base64.StdEncoding.EncodeToString(sig) + "\n")}
		}, cabinet.CodeProfileSignatureInvalid},
		{"profile bytes changed after signing", func(t *testing.T, f *profFixture) {
			f.fsys["m"+profileSuffix] = &fstest.MapFile{Data: append(append([]byte{}, f.raw...), '\n')}
		}, cabinet.CodeProfileDigestMismatch},
		{"signature by a different key than the presented one", func(t *testing.T, f *profFixture) {
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			f.writeSidecars(t, f.raw, other, f.spki, f.keyID, algEd25519) // signed by `other`, SPKI of the authorised key
		}, cabinet.CodeProfileSignatureInvalid},
		{"authorised key id presented with another key's SPKI", func(t *testing.T, f *profFixture) {
			pub, priv, _ := ed25519.GenerateKey(rand.Reader)
			spki, _ := x509.MarshalPKIXPublicKey(pub)
			f.writeSidecars(t, f.raw, priv, spki, sha256Ref(spki), algEd25519) // valid signature, but the key holds no authority
		}, codeProfileSignerNotAuthorized},
		{"profile file missing", func(t *testing.T, f *profFixture) { delete(f.fsys, "m"+profileSuffix) }, codeProfileMissing},
		{"signature file missing", func(t *testing.T, f *profFixture) { delete(f.fsys, "m"+profileSigSuffix) }, codeProfileMissing},
		{"signer key file missing", func(t *testing.T, f *profFixture) { delete(f.fsys, "m"+profileSPKISuffix) }, codeProfileMissing},
		{"signature file malformed", func(t *testing.T, f *profFixture) {
			f.fsys["m"+profileSigSuffix] = &fstest.MapFile{Data: []byte("not base64 !!\n")}
		}, codeProfileRejected},
		{"operator config absent", func(t *testing.T, f *profFixture) { profileOperator = nil }, cabinet.CodeProfileAdmissionUnconfigured},
		{"the profile exceeds the operator cap", func(t *testing.T, f *profFixture) {
			profileOperator.Caps.TimeoutMs = 1000
		}, cabinet.CodeLimitExceedsOperatorCap},
		{"unknown schema", func(t *testing.T, f *profFixture) {
			profileOperator.Schemas = cabinet.InMemorySchemaRegistry{}
		}, cabinet.CodeSchemaRefUnresolved},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newProfFixture(t, nil, "[describe, observe]")
			c.setup(t, f)
			requireRejected(t, f.admFixture, c.code)
			_, bound := f.binding()
			assert.False(t, bound, "a rejected module has no profile binding")
		})
	}
}

func TestINT05_ManifestCannotWidenTheProfile(t *testing.T) {
	f := newProfFixture(t, nil, "[describe, observe, destroy]") // destroy is not in the profile
	requireRejected(t, f.admFixture, cabinet.CodeManifestOpNotInProfile)
	f = newProfFixture(t, nil, "[]")
	requireRejected(t, f.admFixture, codeProfileRejected)
}

func TestINT05_ManifestPinMustNameTheAdmittedProfile(t *testing.T) {
	raw := profileJSON(t, nil)
	f := newProfFixture(t, nil, "[describe]")
	f.fsys["m.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(string(f.fsys["m.yaml"].Data), cabinet.ProfileDigest(raw), dg("77"), 1))}
	f.resign(t, nil)
	requireRejected(t, f.admFixture, cabinet.CodeProfileDigestMismatch)
}

// a profile cannot be bound to a module that is not authenticated: the binding is tied to the envelope digest
func TestINT05_AProfileNeedsAnAuthenticatedModule(t *testing.T) {
	f := newProfFixture(t, nil, "[describe, observe]")
	delete(f.fsys, "m"+envelopeSuffix)
	delete(f.fsys, "m"+signatureSuffix)
	_, err := f.load(t)
	require.Error(t, err)
	assert.Equal(t, codeModuleRejected, admissionCode(err), "%v", err)
	assert.False(t, f.registered())
	_, bound := f.binding()
	assert.False(t, bound)
}

// no profile named: nothing changes, and a reload without a profile drops the earlier binding
func TestINT05_NoProfileNamedAndReloadClearsTheBinding(t *testing.T) {
	f := newProfFixture(t, nil, "[describe, observe]")
	_, err := f.load(t)
	require.NoError(t, err)
	_, bound := f.binding()
	require.True(t, bound)

	f.fsys["m.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(string(f.fsys["m.yaml"].Data), "  profile:\n    id: cic:provider@0.1.0\n    digest: \""+f.digest+"\"\n", "", 1))}
	f.resign(t, nil)
	_, err = f.load(t)
	require.NoError(t, err, "a module that names no profile loads as before")
	_, bound = f.binding()
	assert.False(t, bound, "the earlier binding does not survive a reload without a profile")
	mod, err := f.svc.GetModule("adm.mod@1.0.0")
	require.NoError(t, err)
	_, hasIdentity := moduleIdentities.get("adm.mod@1.0.0", mod.Checksum())
	assert.False(t, hasIdentity, "nor does the executed identity: a module without a profile claims none")
}

func TestINT05_EcdsaSignerAlsoVerifies(t *testing.T) {
	f := newProfFixture(t, nil, "[describe, observe]")
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	spki, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	require.NoError(t, err)
	keyID := sha256Ref(spki)
	d := sha256.Sum256(cabinet.ProfileSignedBytes(f.raw, keyID, algECDSAP256))
	sig, err := ecdsa.SignASN1(rand.Reader, k, d[:])
	require.NoError(t, err)
	f.fsys["m"+profileSigSuffix] = &fstest.MapFile{Data: []byte(base64.StdEncoding.EncodeToString(sig) + "\n")}
	f.fsys["m"+profileSPKISuffix] = &fstest.MapFile{Data: spki}
	f.assets.ProfileSignAuthorities = map[string]bool{keyID: true}
	_, err = f.load(t)
	require.NoError(t, err)
}

func TestINT05_VerifierRefusals(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	spki, _ := x509.MarshalPKIXPublicKey(pub)
	id := sha256Ref(spki)
	signed := []byte("x")
	good := cabinet.ProfileSignature{SignerKeyID: id, SignerAlg: algEd25519, Signature: ed25519.Sign(priv, signed)}
	v := bundleProfileVerifier{spki: spki, authorities: map[string]bool{id: true}}
	require.NoError(t, v.VerifySignedProfile(signed, good))

	bad := good
	bad.SignerAlg = algECDSAP256
	assert.ErrorContains(t, v.VerifySignedProfile(signed, bad), "does not match the key type")
	bad = good
	bad.SignerKeyID = "sha256:" + strings.Repeat("0", 64)
	assert.ErrorContains(t, v.VerifySignedProfile(signed, bad), "is not the presented key")
	assert.ErrorContains(t, bundleProfileVerifier{spki: []byte("junk"), authorities: v.authorities}.VerifySignedProfile(signed, cabinet.ProfileSignature{SignerKeyID: sha256Ref([]byte("junk"))}), "cannot be parsed")

	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecSPKI, _ := x509.MarshalPKIXPublicKey(&ec.PublicKey)
	ecv := bundleProfileVerifier{spki: ecSPKI, authorities: map[string]bool{sha256Ref(ecSPKI): true}}
	assert.ErrorContains(t, ecv.VerifySignedProfile(signed, cabinet.ProfileSignature{SignerKeyID: sha256Ref(ecSPKI), SignerAlg: algEd25519}), "does not match the key type")
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p384SPKI, _ := x509.MarshalPKIXPublicKey(&p384.PublicKey)
	assert.ErrorContains(t, bundleProfileVerifier{spki: p384SPKI, authorities: map[string]bool{sha256Ref(p384SPKI): true}}.VerifySignedProfile(signed, cabinet.ProfileSignature{SignerKeyID: sha256Ref(p384SPKI), SignerAlg: algECDSAP256}), "does not match the key type")
}

func TestINT05_OperatorConfig(t *testing.T) {
	ok := `{"kind":"AbiOperatorConfig","version":1,"limits":{"max_request_bytes":10,"max_response_bytes":20,"timeout_ms":30},` +
		`"schemas":[{"id":"s1","digest":"` + dg("ab") + `","fields":{"operation_role":["read","action"]}},{"id":"s2","digest":"` + dg("cd") + `"}]}`
	cfg, err := parseProfileOperatorConfig([]byte(ok), t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, cabinet.OperatorLimits{MaxRequestBytes: 10, MaxResponseBytes: 20, TimeoutMs: 30}, cfg.Caps)
	e, found := cfg.Schemas.Resolve("s1")
	require.True(t, found)
	assert.Equal(t, []string{"read", "action"}, e.Fields["operation_role"])
	for name, bad := range map[string]string{
		"duplicate key":    strings.Replace(ok, `"version":1,`, `"version":1,"version":1,`, 1),
		"unknown key":      strings.Replace(ok, `"kind"`, `"x":1,"kind"`, 1),
		"wrong kind":       strings.Replace(ok, "AbiOperatorConfig", "Other", 1),
		"wrong version":    strings.Replace(ok, `"version":1`, `"version":2`, 1),
		"zero limit":       strings.Replace(ok, `"timeout_ms":30`, `"timeout_ms":0`, 1),
		"negative limit":   strings.Replace(ok, `"max_request_bytes":10`, `"max_request_bytes":-1`, 1),
		"bad digest":       strings.Replace(ok, dg("ab"), "sha256:zz", 1),
		"empty schema id":  strings.Replace(ok, `"id":"s1"`, `"id":""`, 1),
		"duplicate schema": strings.Replace(ok, `"id":"s2"`, `"id":"s1"`, 1),
		"trailing data":    ok + "{}",
		"limit missing":    strings.Replace(ok, `"timeout_ms":30`, `"x":30`, 1),
	} {
		_, err := parseProfileOperatorConfig([]byte(bad), t.TempDir())
		assert.Error(t, err, name)
	}
}

func TestINT05_OperatorConfigFromTheEnvironment(t *testing.T) {
	t.Cleanup(func() { profileOperator = nil })
	t.Setenv(abiOperatorConfigEnv, "")
	profileOperator = nil
	require.NoError(t, loadProfileOperatorConfig())
	assert.Nil(t, profileOperator, "unset: unconfigured")

	dir := t.TempDir()
	path := dir + "/cfg.json"
	require.NoError(t, osWriteFile(path, `{"kind":"AbiOperatorConfig","version":1,"limits":{"max_request_bytes":1,"max_response_bytes":1,"timeout_ms":1},"schemas":[]}`))
	t.Setenv(abiOperatorConfigEnv, path)
	require.NoError(t, loadProfileOperatorConfig())
	require.NotNil(t, profileOperator)

	t.Setenv(abiOperatorConfigEnv, dir+"/absent.json")
	assert.Error(t, loadProfileOperatorConfig(), "set but unreadable is a start-up error")
	require.NoError(t, osWriteFile(path, `nope`))
	t.Setenv(abiOperatorConfigEnv, path)
	assert.Error(t, loadProfileOperatorConfig(), "set but invalid is a start-up error")
}

func TestINT05_ProfileCode(t *testing.T) {
	assert.Equal(t, codeProfileRejected, profileCode(assert.AnError))
	assert.Equal(t, codeProfileSignerNotAuthorized, profileCode(&profileAuthError{keyID: "k"}))
	assert.Equal(t, cabinet.CodeProfileUnknown, profileCode(&cabinet.ABIProfileError{Code: cabinet.CodeProfileUnknown}))
}

// osWriteFile writes a small test file.
func osWriteFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

func TestINT06_EffectPolicyFromTheEnvironment(t *testing.T) {
	t.Setenv(abiAllowedEffectsEnv, "")
	opts, err := effectPolicyOptionFromEnv()
	require.NoError(t, err)
	assert.Empty(t, opts, "unset: no policy")

	t.Setenv(abiAllowedEffectsEnv, " local , external-read ")
	opts, err = effectPolicyOptionFromEnv()
	require.NoError(t, err)
	assert.Len(t, opts, 1)

	for _, bad := range []string{"local,external-admin", "write", "local,,external-read"} {
		t.Setenv(abiAllowedEffectsEnv, bad)
		_, err = effectPolicyOptionFromEnv()
		assert.Error(t, err, bad)
	}
}

// INT-07: a profile is not admitted whose schema bodies the operator does not hold, and a body must be exactly the digest.
func TestINT07_ProfileNeedsItsSchemaBodies(t *testing.T) {
	f := newProfFixture(t, nil, "[describe, observe]")
	delete(profileOperator.Bodies, [2]string{"cic:provider:observation", dg("12")})
	requireRejected(t, f.admFixture, cabinet.CodeSchemaUnavailable)
}

func TestINT07_BodyFilesAreDigestBoundAndCompiled(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"type":"object","required":["a"]}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s.json"), body, 0o600))
	cfg := func(digest, file string) string {
		return `{"kind":"AbiOperatorConfig","version":1,"limits":{"max_request_bytes":1,"max_response_bytes":1,"timeout_ms":1},"schemas":[{"id":"s","digest":"` + digest + `","body_file":"` + file + `"}]}`
	}
	good, err := parseProfileOperatorConfig([]byte(cfg(sha256Ref(body), "s.json")), dir)
	require.NoError(t, err)
	assert.True(t, good.Bodies.has(cabinet.AbiSchemaRef{ID: "s", Digest: sha256Ref(body)}))
	assert.False(t, good.Bodies.has(cabinet.AbiSchemaRef{ID: "s", Digest: dg("00")}))
	assert.False(t, good.Bodies.has(cabinet.AbiSchemaRef{ID: "other", Digest: sha256Ref(body)}))

	_, err = parseProfileOperatorConfig([]byte(cfg(dg("aa"), "s.json")), dir)
	assert.ErrorContains(t, err, "pinned digest", "a body that is not the pinned digest is refused at start-up")
	_, err = parseProfileOperatorConfig([]byte(cfg(sha256Ref(body), "absent.json")), dir)
	assert.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"$ref":"x"}`), 0o600))
	_, err = parseProfileOperatorConfig([]byte(cfg(sha256Ref([]byte(`{"$ref":"x"}`)), "bad.json")), dir)
	assert.ErrorContains(t, err, "not supported", "a schema outside the supported subset is refused, not ignored")
	abs, err := parseProfileOperatorConfig([]byte(cfg(sha256Ref(body), filepath.Join(dir, "s.json"))), "/nonexistent")
	require.NoError(t, err, "an absolute body path ignores the config directory")
	assert.Len(t, abs.Bodies, 1)
}

func TestINT07_OperatorValidator(t *testing.T) {
	t.Cleanup(func() { profileOperator = nil })
	profileOperator = nil
	assert.ErrorIs(t, operatorValidator{}.ValidatePayload("s", dg("aa"), []byte(`{}`)), cabinet.ErrSchemaUnavailable, "no configuration: fail closed")
	profileOperator = &profileOperatorConfig{Bodies: bodyStore{{"s", dg("aa")}: compileBody(`{"type":"object","required":["a"]}`)}}
	assert.NoError(t, operatorValidator{}.ValidatePayload("s", dg("aa"), []byte(`{"a":1}`)))
	err := operatorValidator{}.ValidatePayload("s", dg("aa"), []byte(`{}`))
	require.Error(t, err)
	assert.NotErrorIs(t, err, cabinet.ErrSchemaUnavailable, "a non-conforming payload is not an unavailable schema")
	assert.ErrorIs(t, operatorValidator{}.ValidatePayload("s", dg("bb"), []byte(`{}`)), cabinet.ErrSchemaUnavailable, "another digest is not the pinned body")
}

// G-1: the principal's type comes from the authentication layer; a dedicated service principal is valid, the shared bearer
// token stays the restricted default, and a misconfiguration stops the start.
func TestG1_APIPrincipalFromTheEnvironment(t *testing.T) {
	saved := apiPrincipal
	t.Cleanup(func() { apiPrincipal = saved })
	set := func(id, typ string) {
		t.Setenv("CIC_API_PRINCIPAL_ID", id)
		t.Setenv("CIC_API_PRINCIPAL_TYPE", typ)
	}

	set("", "")
	p, err := apiPrincipalFromEnv()
	require.NoError(t, err)
	assert.Equal(t, cabinet.Principal{ID: "api-token", Method: "bearer", Type: cabinet.PrincipalTypeSharedBearer}, p, "default: the shared bearer identity")

	set("relay-observer@prod", "")
	p, err = apiPrincipalFromEnv()
	require.NoError(t, err)
	assert.Equal(t, cabinet.Principal{ID: "relay-observer@prod", Method: "bearer", Type: cabinet.PrincipalTypeService}, p, "a named principal defaults to a service")

	for _, typ := range []string{"human", "service", "agent"} {
		set("p1", typ)
		p, err = apiPrincipalFromEnv()
		require.NoError(t, err, typ)
		assert.Equal(t, typ, p.Type)
	}

	for name, c := range map[string][2]string{
		"type without id":           {"", "service"},
		"unknown type":              {"p1", "root"},
		"shared type not choosable": {"p1", "shared-bearer-token"},
		"id with a space":           {"a b", ""},
		"id with a newline":         {"a\nb", ""},
		"leading dot":               {".x", ""},
		"too long":                  {strings.Repeat("a", 129), ""},
	} {
		set(c[0], c[1])
		_, err := apiPrincipalFromEnv()
		assert.Error(t, err, name)
	}
}

func TestG1_ThePrincipalInTheRequestContextIsTheConfiguredOne(t *testing.T) {
	saved := apiPrincipal
	t.Cleanup(func() { apiPrincipal = saved })
	apiPrincipal = cabinet.Principal{ID: "svc-1", Method: "bearer", Type: cabinet.PrincipalTypeService}
	r := withAPIPrincipal(httptest.NewRequest("GET", "/x", nil))
	p, ok := cabinet.PrincipalFrom(r.Context())
	require.True(t, ok)
	assert.Equal(t, apiPrincipal, p)

	assert.True(t, cabinet.ValidPrincipalType("human"))
	assert.True(t, cabinet.ValidPrincipalType("shared-bearer-token"))
	assert.False(t, cabinet.ValidPrincipalType(""))
	assert.False(t, cabinet.ValidPrincipalType("admin"))
}
