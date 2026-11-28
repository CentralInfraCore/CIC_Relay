package main

// Test fixtures for the #162 module artifact admission: a test signer (the testdata module code-signing leaf and its
// key; TEST KEY, it protects nothing), and a helper that writes a CICMODENVv1 envelope and its detached signature
// beside a module in an fstest.MapFS, computed from exactly the bytes in that MapFS.

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"centralrelay/core/cabinet"
)

// envelopeSigner is a test signer: a certificate, its private key and the derived key_id.
type envelopeSigner struct {
	key     crypto.Signer
	certPEM []byte
	cert    *x509.Certificate
	keyID   string
	alg     string
	root    *x509.Certificate // the test root the certificate chains to (nil: already in the trust store)
}

// testModuleSigner returns the process-wide test module signer: a fresh ECDSA P-256 code-signing leaf under its own
// test root (TEST KEY). The testdata module signer (pki/signers/sources/module/ee.pem) is RSA, which the envelope's
// algorithm allowlist (ENV-02: ecdsa-p256-sha256, ed25519) does not admit, so it can not sign an envelope; see
// TestAdmission_RSASignerCanNotSignAnEnvelope. authorize also adds this root to the trust store.
func testModuleSigner(t *testing.T) *envelopeSigner {
	t.Helper()
	testSignerOnce.Do(func() { testSigner = newECDSATestSigner(t, "Envelope Test Root") })
	return testSigner
}

var (
	testSignerOnce sync.Once
	testSigner     *envelopeSigner
)

// newECDSATestSigner issues an ECDSA P-256 code-signing leaf under a fresh ECDSA root.
func newECDSATestSigner(t *testing.T, cn string) *envelopeSigner {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	root := selfSignedTestRoot(t, cn, rootKey)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	s := newEnvelopeSigner(t, issueCodeSigningLeaf(t, root, rootKey, &leafKey.PublicKey, cn+" module signer"), leafKey)
	s.root = root
	return s
}

// selfSignedTestRoot creates a CA certificate for key.
func selfSignedTestRoot(t *testing.T, cn string, key *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	root, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return root
}

// issueCodeSigningLeaf issues a code-signing leaf for pub under root and returns it as PEM.
func issueCodeSigningLeaf(t *testing.T, root *x509.Certificate, rootKey *ecdsa.PrivateKey, pub any, cn string) []byte {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root, pub, rootKey)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// newEnvelopeSigner wraps a certificate PEM and its private key.
func newEnvelopeSigner(t *testing.T, certPEM []byte, key crypto.Signer) *envelopeSigner {
	t.Helper()
	b, _ := pem.Decode(certPEM)
	require.NotNil(t, b)
	cert, err := x509.ParseCertificate(b.Bytes)
	require.NoError(t, err)
	alg := algECDSAP256
	if _, ok := key.(ed25519.PrivateKey); ok {
		alg = algEd25519
	}
	return &envelopeSigner{key: key, certPEM: certPEM, cert: cert, keyID: sha256Ref(cert.RawSubjectPublicKeyInfo), alg: alg}
}

// sign returns the detached .sig file content (one base64 line + LF) over env's signed bytes.
func (s *envelopeSigner) sign(t *testing.T, env *moduleReleaseEnvelope) []byte {
	t.Helper()
	msg := env.signedBytes()
	var sig []byte
	var err error
	switch k := s.key.(type) {
	case *ecdsa.PrivateKey:
		d := sha256.Sum256(msg)
		sig, err = ecdsa.SignASN1(rand.Reader, k, d[:])
	case ed25519.PrivateKey:
		sig = ed25519.Sign(k, msg)
	default:
		t.Fatalf("unsupported test key %T", k)
	}
	require.NoError(t, err)
	return []byte(base64.StdEncoding.EncodeToString(sig) + "\n")
}

// authorize pins s as a module:sign authority in assets and adds its test root to the trust store (and to the
// CICSourceCA pool when one is loaded).
func (s *envelopeSigner) authorize(assets *PKIAssets) *PKIAssets {
	if s.root != nil {
		assets.Roots.AddCert(s.root)
		if assets.SourceLoaded && assets.SourceRoots != nil && assets.SourceRoots != assets.Roots {
			assets.SourceRoots.AddCert(s.root)
		}
	}
	if assets.ModuleSignAuthorities == nil {
		assets.ModuleSignAuthorities = map[string]bool{}
	}
	assets.ModuleSignAuthorities[s.keyID] = true
	return assets
}

// executedOf is the executed representation the relay derives from raw (the descriptor's WasmCode). A wasm the
// relay can not decode has none; the raw bytes stand in so that a fixture for an unbuildable module can be signed.
func executedOf(t *testing.T, raw []byte) []byte {
	t.Helper()
	d, err := cabinet.NewModuleDescriptor(context.Background(), cabinet.BaseBlock{Name: "x", Version: "1"}, "", nil, nil, raw)
	if err != nil {
		return raw
	}
	return d.WasmCode
}

// envelopeFor builds the envelope of the module at yamlPath in fsys, from the bytes in fsys.
func envelopeFor(t *testing.T, fsys fstest.MapFS, yamlPath string, s *envelopeSigner) *moduleReleaseEnvelope {
	t.Helper()
	base := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath))
	manifest := fsys[yamlPath].Data
	raw := fsys[base+".wasm"].Data
	var mf ModuleFile
	require.NoError(t, yaml.Unmarshal(manifest, &mf))
	env := &moduleReleaseEnvelope{Kind: "ModuleReleaseEnvelope", EnvelopeVersion: 1, ModuleID: mf.Metadata.Name + "@" + mf.Metadata.Version,
		WasmSHA256: sha256Ref(raw), ExecutedWasmSHA256: sha256Ref(executedOf(t, raw)), ManifestDigest: sha256Ref(manifest)}
	env.Signer.KeyID, env.Signer.Alg = s.keyID, s.alg
	return env
}

// writeEnvelope writes env and its signature by s beside the module; it returns the envelope digest.
func writeEnvelope(t *testing.T, fsys fstest.MapFS, yamlPath string, env *moduleReleaseEnvelope, sig []byte) string {
	t.Helper()
	base := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath))
	data, err := json.Marshal(env)
	require.NoError(t, err)
	fsys[base+envelopeSuffix] = &fstest.MapFile{Data: data}
	fsys[base+signatureSuffix] = &fstest.MapFile{Data: sig}
	return sha256Ref(env.signedBytes())
}

// addEnvelope signs the module at yamlPath with s (after the optional mutate) and writes the envelope, its signature
// and the signer certificate (<base>.pem) into fsys. It returns the envelope digest.
func addEnvelope(t *testing.T, fsys fstest.MapFS, yamlPath string, s *envelopeSigner, mutate func(*moduleReleaseEnvelope)) string {
	t.Helper()
	env := envelopeFor(t, fsys, yamlPath, s)
	if mutate != nil {
		mutate(env)
	}
	base := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath))
	fsys[base+".pem"] = &fstest.MapFile{Data: s.certPEM}
	return writeEnvelope(t, fsys, yamlPath, env, s.sign(t, env))
}

// hexSHA is the lower-hex sha256 of b.
func hexSHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// testManifestBinding is the envelope digest stand-in of registry-level tests that publish a grant without loading
// a module (publishVerified): the hex digest of the manifest, so instUnder keeps naming the binding.
func testManifestBinding(y []byte) string { return hexSHA(y) }

// registerVerified publishes an active grant with no artifact and no envelope identity (it authorises nothing): the
// registry-level tests use it to exercise manifest parsing and import checks.
func (r *cicFlowRegistry) registerVerified(moduleID string, projectYAML []byte) error {
	return r.publish(moduleID, projectYAML, "", "", true)
}

// publishVerified publishes an active grant for artifact under the testManifestBinding identity.
func (r *cicFlowRegistry) publishVerified(moduleID string, projectYAML []byte, artifact string) error {
	return r.publish(moduleID, projectYAML, artifact, testManifestBinding(projectYAML), true)
}

// lookup returns the cic-flow deps recorded for moduleID, whatever their state (registry-level inspection only; the
// host functions use lookupFor).
func (r *cicFlowRegistry) lookup(moduleID string) (cicFlowDeps, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.deps[moduleID]
	return g.deps, ok
}

// ed25519TestSigner returns a fresh ECDSA root and an Ed25519 code-signing leaf issued by it, as a signer.
func ed25519TestSigner(t *testing.T) (*x509.Certificate, *envelopeSigner) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	root := selfSignedTestRoot(t, "Ed25519 Test Root", rootKey)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	s := newEnvelopeSigner(t, issueCodeSigningLeaf(t, root, rootKey, pub, "Ed25519 module signer"), priv)
	s.root = root
	return root, s
}

// loadedPKIWithoutSource is a trust store in which the test module signer's chain verifies and the signer holds
// module:sign, but no CICSourceCA provenance anchor is loaded: an enveloped module loads as transitional.
func loadedPKIWithoutSource(t *testing.T) *PKIAssets {
	t.Helper()
	return testModuleSigner(t).authorize(buildLoadedPKIAssets(t))
}

// instOf is the identity of an instance of artifact started while the release in fsys (its envelope) was in force.
func instOf(t *testing.T, name, artifact string, fsys fstest.MapFS) cabinet.InstanceIdentity {
	t.Helper()
	env, err := parseEnvelope(fsys["m"+envelopeSuffix].Data)
	require.NoError(t, err)
	return cabinet.InstanceIdentity{Name: name, Artifact: artifact, Serial: 1, Binding: sha256Ref(env.signedBytes())}
}
