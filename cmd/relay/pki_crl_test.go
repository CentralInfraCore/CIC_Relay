package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"centralrelay/core/cabinet"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crlTestPKI is a root CA with one code-signing leaf, enough to issue and verify CRLs.
type crlTestPKI struct {
	rootCert *x509.Certificate
	rootKey  *ecdsa.PrivateKey
	leaf     *x509.Certificate
}

func newCA(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-48 * time.Hour),
		NotAfter:              time.Now().Add(48 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key
}

func newCRLTestPKI(t *testing.T) *crlTestPKI {
	t.Helper()
	root, rootKey := newCA(t, "CRL Test Root")
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(4242),
		Subject:      pkix.Name{CommonName: "CRL Test Leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root, &leafKey.PublicKey, rootKey)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &crlTestPKI{rootCert: root, rootKey: rootKey, leaf: leaf}
}

func certPEM(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}

// crlPEM issues a CRL revoking the leaf, signed by signer (cert + key).
func (p *crlTestPKI) crlPEM(t *testing.T, signer *x509.Certificate, key *ecdsa.PrivateKey, nextUpdate time.Time) []byte {
	t.Helper()
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number:                    big.NewInt(1),
		ThisUpdate:                time.Now().Add(-3 * time.Hour),
		NextUpdate:                nextUpdate,
		RevokedCertificateEntries: []x509.RevocationListEntry{{SerialNumber: p.leaf.SerialNumber, RevocationTime: time.Now().Add(-2 * time.Hour)}},
	}, signer, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der})
}

// forgedCRLPEM is a CRL with the real root's name but signed by an attacker's key.
func (p *crlTestPKI) forgedCRLPEM(t *testing.T) []byte {
	t.Helper()
	attackerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	// Same subject as the real root, different key: the issuer name matches, the signature cannot.
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(666), Subject: p.rootCert.Subject,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &attackerKey.PublicKey, attackerKey)
	require.NoError(t, err)
	attacker, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return p.crlPEM(t, attacker, attackerKey, time.Now().Add(24*time.Hour))
}

func writeBundle(t *testing.T, parts ...[]byte) string {
	t.Helper()
	var all []byte
	for _, part := range parts {
		all = append(all, part...)
	}
	path := filepath.Join(t.TempDir(), "bundle.pem")
	require.NoError(t, os.WriteFile(path, all, 0600))
	t.Setenv("CIC_PLATFORM_CA_BUNDLE", path)
	return path
}

func verifyLeaf(p *crlTestPKI, assets *PKIAssets) error {
	_, err := cabinet.VerifyCertificateChain(p.leaf, cabinet.VerifyOptions{
		Roots: assets.Roots, Intermediates: assets.Intermediates, CRLs: assets.CRLs, CRLFreshness: assets.CRLFreshness,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	})
	return err
}

// Acceptance 1: a CRL signed by a different key is rejected at load; its entries are not used.
func TestBootstrapPKI_Prod_CRL_ForeignKeySignature_Error(t *testing.T) {
	p := newCRLTestPKI(t)
	writeBundle(t, certPEM(p.rootCert), p.forgedCRLPEM(t))
	_, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature does not verify")
}

func TestLoadPKIAssets_Dev_CRL_ForeignKeySignature_NotUsed(t *testing.T) {
	p := newCRLTestPKI(t)
	fsys := fstest.MapFS{
		"pki/root.pem": {Data: certPEM(p.rootCert)},
		"pki/crl.pem":  {Data: p.forgedCRLPEM(t)},
	}
	assets, err := loadPKIAssets(fsys, "pki", newDiscardLogger())
	require.NoError(t, err, "dev stays lenient: the forged CRL is dropped, start-up continues")
	assert.Empty(t, assets.CRLs, "the entries of a CRL with a bad signature must not be used")
	assert.NoError(t, verifyLeaf(p, assets), "the forged CRL must not be able to revoke the leaf")
}

// A CRL whose issuer is not in the trust store cannot be authenticated either.
func TestBootstrapPKI_Prod_CRL_UnknownIssuer_Error(t *testing.T) {
	p := newCRLTestPKI(t)
	otherCA, otherKey := newCA(t, "A Different Root") // not in the bundle, and a different name
	writeBundle(t, certPEM(p.rootCert), p.crlPEM(t, otherCA, otherKey, time.Now().Add(24*time.Hour)))
	_, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no issuer certificate")
}

// Acceptance 2: an expired CRL in prod is an error; an explicit tolerance can allow it.
func TestBootstrapPKI_Prod_CRL_Stale_Error(t *testing.T) {
	p := newCRLTestPKI(t)
	writeBundle(t, certPEM(p.rootCert), p.crlPEM(t, p.rootCert, p.rootKey, time.Now().Add(-2*time.Hour)))

	t.Setenv("CIC_CRL_MAX_STALENESS", "")
	_, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stale")

	t.Setenv("CIC_CRL_MAX_STALENESS", "3h")
	assets, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
	require.NoError(t, err, "a configured tolerance covers a CRL 2h past NextUpdate")
	assert.Len(t, assets.CRLs, 1)
}

func TestBootstrapPKI_Prod_InvalidTolerance_Error(t *testing.T) {
	p := newCRLTestPKI(t)
	writeBundle(t, certPEM(p.rootCert))
	for _, bad := range []string{"abc", "-1h"} {
		t.Setenv("CIC_CRL_MAX_STALENESS", bad)
		_, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
		require.Error(t, err, "value %q", bad)
		assert.Contains(t, err.Error(), "CIC_CRL_MAX_STALENESS")
	}
}

// Acceptance 3: an unparsable CRL in prod is a start-up error, not a WARN.
func TestBootstrapPKI_Prod_CRL_Unparsable_Error(t *testing.T) {
	p := newCRLTestPKI(t)
	writeBundle(t, certPEM(p.rootCert), []byte("-----BEGIN X509 CRL-----\nYWJjZGVmZw==\n-----END X509 CRL-----\n"))
	_, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unparsable CRL")
}

// Acceptance 4: a revoked certificate stays rejected after a restart with the same bundle.
func TestBootstrapPKI_Prod_RevokedLeafStaysRejectedAfterRestart(t *testing.T) {
	p := newCRLTestPKI(t)
	writeBundle(t, certPEM(p.rootCert), p.crlPEM(t, p.rootCert, p.rootKey, time.Now().Add(24*time.Hour)))

	for _, boot := range []string{"first start", "restart"} {
		assets, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
		require.NoError(t, err, boot)
		require.Len(t, assets.CRLs, 1, boot)
		err = verifyLeaf(p, assets)
		require.Error(t, err, "%s: the revoked leaf must be rejected", boot)
		assert.Contains(t, err.Error(), "revoked", boot)
	}
}

// Without a CRL, prod still starts but says so; the empty pin set is stated too.
func TestBootstrapPKI_Prod_NoCRL_NoPins_AreVisible(t *testing.T) {
	p := newCRLTestPKI(t)
	writeBundle(t, certPEM(p.rootCert))
	logger, hook := logrustest.NewNullLogger()
	assets, err := bootstrapPKI(TrustModeProd, embeddedFS, logger)
	require.NoError(t, err)
	assert.Empty(t, assets.CRLs)

	var msgs []string
	for _, e := range hook.AllEntries() {
		msgs = append(msgs, e.Message)
	}
	assert.Contains(t, msgs, "Revocation checking is inactive: the CA bundle contains no CRL")
	assert.Contains(t, msgs, "SPKI pinning is not configured: the pin set is empty")
}

func TestCrlToleranceFromEnv(t *testing.T) {
	t.Setenv("CIC_CRL_MAX_STALENESS", "")
	d, err := crlToleranceFromEnv()
	require.NoError(t, err)
	assert.Zero(t, d)

	t.Setenv("CIC_CRL_MAX_STALENESS", " 30m ")
	d, err = crlToleranceFromEnv()
	require.NoError(t, err)
	assert.Equal(t, 30*time.Minute, d)
}
