package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/o1verify"
	"centralrelay/pkg/o1verify/attest"
	"centralrelay/pkg/trustbundle"
)

var now = time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

func sha(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }
func d(c string) string   { return "sha256:" + strings.Repeat(c, 32) }

// src is a minimal TraceV2Source for building a real artifact.
type src struct{}

func (src) ExecutedIdentity(m *cabinet.ModuleDescriptor, dispatched string) (cabinet.ExecutedIdentityV2, bool) {
	env := d("12")
	return cabinet.ExecutedIdentityV2{ArtifactDigest: d("ab"), ExecutedDigest: d("cd"), ManifestDigest: d("ef"), EnvelopeDigest: &env,
		Profile: cabinet.ProfileRefV2{ID: "p", Digest: d("34")}, Dispatched: dispatched, TrustState: cabinet.TrustStateAuthenticated}, true
}
func (src) Authorization(context.Context, cabinet.ExecutionGraphStep) (string, string) {
	return cabinet.AuthorizationNotEvaluated, ""
}
func (src) ExecutionContext(context.Context, string) string { return d("78") }
func (src) HostInteractions(context.Context, cabinet.ExecutionGraphStep) (string, int) {
	return cabinet.EmptyHostInteractionsDigestV2(), 0
}

func artifact(t *testing.T) *cabinet.ProofArtifactV2 {
	ctx := context.Background()
	mods := cabinet.NewMemModuleStore()
	mod, err := cabinet.NewModuleDescriptor(ctx, cabinet.BaseBlock{Name: "echo", Version: "1.0"}, "native", nil, nil, nil)
	require.NoError(t, err)
	mod.NativeImpl = cabinet.NativeFunctions{"echo": func(_ context.Context, in interface{}) (interface{}, error) { return in, nil }}
	mods.Put(mod)
	var sealed []*cabinet.ProofArtifactV2
	svc, err := cabinet.NewCabinetService(ctx, cabinet.NewMemSchemaStore(), mods, cabinet.NewMemWorkflowStore(), cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1},
		cabinet.WithTraceV2(src{}, func(_ context.Context, a *cabinet.ProofArtifactV2) { sealed = append(sealed, a) }))
	require.NoError(t, err)
	wf, err := cabinet.NewWorkflow(ctx, cabinet.WorkflowIdentity{Version: "1.0"}, cabinet.WorkflowConfig{Name: "wf.cli", Steps: []string{"echo@1.0.echo(input) -> out"}}, cabinet.WorkflowSigning{})
	require.NoError(t, err)
	require.NoError(t, svc.PutWorkflow(wf))
	_, err = svc.Setx(ctx, &cabinet.SetPayload{WorkflowID: wf.ID(), SourceDigest: "git:abc", Payload: map[string]any{"k": "v"}})
	require.NoError(t, err)
	require.Len(t, sealed, 1)
	return sealed[0]
}

type key struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	spki []byte
	id   string
	pem  []byte
}

func newKey(t *testing.T) key {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	spki, _ := x509.MarshalPKIXPublicKey(pub)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	return key{pub, priv, spki, sha(spki), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})}
}

type rig struct {
	dir                      string
	art                      *cabinet.ProofArtifactV2
	rec, ver                 key
	artifactPath, bundlePath string
	bundlePin                string
}

func newRig(t *testing.T, recorderRole, verifierRole string) *rig {
	r := &rig{dir: t.TempDir(), art: artifact(t), rec: newKey(t), ver: newKey(t)}
	doc, _ := json.Marshal(r.art)
	r.artifactPath = filepath.Join(r.dir, "a.json")
	require.NoError(t, os.WriteFile(r.artifactPath, doc, 0o600))
	roles := func(role string) string {
		if role == "" {
			return "[]"
		}
		return `["` + role + `"]`
	}
	bundle := `{"kind":"CICTrustBundle","bundle_version":3,"issued_at":"2026-11-28T10:00:00Z","authorities":[` +
		`{"key_id":"` + r.rec.id + `","roles":` + roles(recorderRole) + `},{"key_id":"` + r.ver.id + `","roles":` + roles(verifierRole) + `}],"accepted_profiles":[]}`
	r.bundlePath = filepath.Join(r.dir, "b.json")
	require.NoError(t, os.WriteFile(r.bundlePath, []byte(bundle), 0o600))
	r.bundlePin = trustbundle.Digest([]byte(bundle))
	return r
}

// persistence writes the persistence object and attestation material for the rig's artifact, signed by k.
func (r *rig) persistence(t *testing.T, k key, chain string) (pPath, mPath string) {
	sig := ed25519.Sign(k.priv, attest.RecorderSignedBytes(chain, k.id, attest.AlgEd25519))
	p := o1verify.Persistence{ArtifactChainHash: r.art.ChainHash, Persisted: true, Recorder: "relay", Commit: "file:" + d("99"),
		Attestation: &o1verify.Attestation{Kind: "recorder-signature", SignerKeyID: k.id, SignatureDigest: sha(sig)}}
	pj, _ := json.Marshal(p)
	mj, _ := json.Marshal(map[string]string{"signature_b64": base64.StdEncoding.EncodeToString(sig), "spki_b64": base64.StdEncoding.EncodeToString(k.spki)})
	pPath, mPath = filepath.Join(r.dir, "p.json"), filepath.Join(r.dir, "m.json")
	require.NoError(t, os.WriteFile(pPath, pj, 0o600))
	require.NoError(t, os.WriteFile(mPath, mj, 0o600))
	return
}

func exec(t *testing.T, args ...string) (int, map[string]any, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, now)
	var doc map[string]any
	if out.Len() > 0 {
		require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	}
	return code, doc, errb.String()
}

func resultOf(doc map[string]any) map[string]any { return doc["result"].(map[string]any) }

func TestVerifiesAnArtifactAndReportsVerifiedNotAccepted(t *testing.T) {
	r := newRig(t, "recorder:attest", "verifier:attest")
	code, doc, _ := exec(t, "--artifact", r.artifactPath)
	assert.Equal(t, 0, code)
	assert.Equal(t, "VERIFIED", resultOf(doc)["evidence_verdict"])
	assert.Equal(t, "NOT_EVALUATED", resultOf(doc)["acceptance"], "no policy: acceptance is not evaluated")
	impl := resultOf(doc)["verifier"].(map[string]any)["implementation_digest"].(string)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, impl)
}

func TestATamperedArtifactIsNotVerified(t *testing.T) {
	r := newRig(t, "", "")
	b, _ := os.ReadFile(r.artifactPath)
	require.NoError(t, os.WriteFile(r.artifactPath, bytes.Replace(b, []byte(`"outcome"`), []byte(`"outcome"`), 1), 0o600)) // unchanged: control
	code, _, _ := exec(t, "--artifact", r.artifactPath)
	assert.Equal(t, 0, code, "control: the untouched artifact verifies")

	tampered := strings.Replace(string(b), r.art.Workflow.SourceDigest, "git:other", 1)
	require.NotEqual(t, string(b), tampered)
	require.NoError(t, os.WriteFile(r.artifactPath, []byte(tampered), 0o600))
	code, doc, _ := exec(t, "--artifact", r.artifactPath)
	assert.Equal(t, 1, code)
	assert.Equal(t, "NOT_VERIFIED", resultOf(doc)["evidence_verdict"])
}

func TestAttestedPersistenceNeedsTheBundleAndItsAuthority(t *testing.T) {
	r := newRig(t, "recorder:attest", "verifier:attest")
	pPath, mPath := r.persistence(t, r.rec, r.art.ChainHash)
	base := []string{"--artifact", r.artifactPath, "--persistence", pPath, "--attestation-material", mPath, "--bundle", r.bundlePath, "--bundle-pin", r.bundlePin,
		"--policy-digest", d("cc"), "--require-persistence"}
	code, doc, _ := exec(t, base...)
	assert.Equal(t, 0, code)
	assert.Equal(t, "PERSISTED", resultOf(doc)["dimensions"].(map[string]any)["evidence"])
	assert.Equal(t, "NOT_ACCEPTED", resultOf(doc)["acceptance"], "VERIFIED is not ACCEPTED: authorization was not evaluated")

	// without the bundle the attestation cannot be checked at all
	code, _, errs := exec(t, "--artifact", r.artifactPath, "--persistence", pPath, "--attestation-material", mPath)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "needs --bundle")

	// a bundle that is not the pinned one is not admitted
	code, _, errs = exec(t, append(base[:len(base):len(base)], "--bundle-pin", d("00"))...)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "BUNDLE_NOT_PINNED")

	// the recorder key has no recorder:attest in this bundle
	r2 := newRig(t, "", "verifier:attest")
	pPath2, mPath2 := r2.persistence(t, r2.rec, r2.art.ChainHash)
	code, doc, _ = exec(t, "--artifact", r2.artifactPath, "--persistence", pPath2, "--attestation-material", mPath2, "--bundle", r2.bundlePath, "--bundle-pin", r2.bundlePin)
	assert.Equal(t, 1, code)
	assert.NotEqual(t, "PERSISTED", resultOf(doc)["dimensions"].(map[string]any)["evidence"])

	// an attestation that signs another artifact's chain hash
	pPath3, mPath3 := r.persistence(t, r.rec, d("99"))
	code, _, _ = exec(t, "--artifact", r.artifactPath, "--persistence", pPath3, "--attestation-material", mPath3, "--bundle", r.bundlePath, "--bundle-pin", r.bundlePin)
	assert.Equal(t, 1, code)
}

func TestSigningTheResultNeedsVerifierAuthority(t *testing.T) {
	r := newRig(t, "recorder:attest", "verifier:attest")
	keyPath := filepath.Join(r.dir, "v.pem")
	require.NoError(t, os.WriteFile(keyPath, r.ver.pem, 0o600))
	args := []string{"--artifact", r.artifactPath, "--bundle", r.bundlePath, "--bundle-pin", r.bundlePin, "--sign-key", keyPath}
	code, doc, _ := exec(t, args...)
	require.Equal(t, 0, code)
	sig := doc["signature"].(map[string]any)
	assert.Equal(t, r.ver.id, sig["signer_key_id"])
	assert.Equal(t, false, sig["implementation_pinned"], "no pinned implementation digests were given")

	// the signature verifies for exactly that result
	rj, _ := json.Marshal(doc["result"])
	var res o1verify.Result
	require.NoError(t, json.Unmarshal(rj, &res))
	sb, _ := base64.StdEncoding.DecodeString(sig["signature_b64"].(string))
	spki, _ := base64.StdEncoding.DecodeString(sig["spki_b64"].(string))
	_, err := attest.VerifyResult(attest.SignedResult{Result: res, SignerKey: r.ver.id, Alg: attest.AlgEd25519, Signature: sb, SPKI: spki}, map[string]bool{r.ver.id: true}, nil)
	assert.NoError(t, err)

	// a pinned set that does not contain this binary's digest: refused
	code, _, errs := exec(t, append(args, "--pinned-implementation", d("00"))...)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "IMPLEMENTATION_NOT_PINNED")

	// the key holds no verifier:attest
	r2 := newRig(t, "recorder:attest", "")
	keyPath2 := filepath.Join(r2.dir, "v.pem")
	require.NoError(t, os.WriteFile(keyPath2, r2.ver.pem, 0o600))
	code, _, errs = exec(t, "--artifact", r2.artifactPath, "--bundle", r2.bundlePath, "--bundle-pin", r2.bundlePin, "--sign-key", keyPath2)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "not authorised")

	// signing needs the bundle
	code, _, errs = exec(t, "--artifact", r.artifactPath, "--sign-key", keyPath)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "needs --bundle")
}

func TestUsageAndInputErrors(t *testing.T) {
	r := newRig(t, "", "")
	for name, args := range map[string][]string{
		"no artifact":         {},
		"unknown flag":        {"--nope"},
		"missing artifact":    {"--artifact", filepath.Join(r.dir, "absent.json")},
		"bad interactions":    {"--artifact", r.artifactPath, "--interactions", filepath.Join(r.dir, "absent.json")},
		"bad persistence":     {"--artifact", r.artifactPath, "--persistence", filepath.Join(r.dir, "absent.json")},
		"missing bundle file": {"--artifact", r.artifactPath, "--bundle", filepath.Join(r.dir, "absent.json"), "--bundle-pin", d("00")},
	} {
		code, _, _ := exec(t, args...)
		assert.Equal(t, 2, code, name)
	}
	bad := filepath.Join(r.dir, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte(`{} {}`), 0o600))
	code, _, errs := exec(t, "--artifact", r.artifactPath, "--interactions", bad)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "unexpected data")
	nonPEM := filepath.Join(r.dir, "np.pem")
	require.NoError(t, os.WriteFile(nonPEM, []byte("x"), 0o600))
	code, _, errs = exec(t, "--artifact", r.artifactPath, "--bundle", r.bundlePath, "--bundle-pin", r.bundlePin, "--sign-key", nonPEM)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "not PEM")
	ec := filepath.Join(r.dir, "junk.pem")
	require.NoError(t, os.WriteFile(ec, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("junk")}), 0o600))
	code, _, errs = exec(t, "--artifact", r.artifactPath, "--bundle", r.bundlePath, "--bundle-pin", r.bundlePin, "--sign-key", ec)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "ed25519")
	badMat := filepath.Join(r.dir, "bm.json")
	pPath, _ := r.persistence(t, r.rec, r.art.ChainHash)
	require.NoError(t, os.WriteFile(badMat, []byte(`{"signature_b64":"!!","spki_b64":"!!"}`), 0o600))
	code, _, errs = exec(t, "--artifact", r.artifactPath, "--persistence", pPath, "--attestation-material", badMat, "--bundle", r.bundlePath, "--bundle-pin", r.bundlePin)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "base64")
}
