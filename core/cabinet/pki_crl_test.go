package cabinet

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func crlCA(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key
}

func crlOf(t *testing.T, signer *x509.Certificate, key *ecdsa.PrivateKey, next time.Time) *x509.RevocationList {
	t.Helper()
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: next.Add(-time.Hour), NextUpdate: next,
	}, signer, key)
	require.NoError(t, err)
	crl, err := x509.ParseRevocationList(der)
	require.NoError(t, err)
	return crl
}

func TestVerifyCRLSignature(t *testing.T) {
	root, rootKey := crlCA(t, "Root")
	crl := crlOf(t, root, rootKey, time.Now().Add(time.Hour))

	assert.NoError(t, VerifyCRLSignature(crl, []*x509.Certificate{root}))

	// Same name, different key: the issuer matches by name, the signature must not verify.
	impostor, impostorKey := crlCA(t, "Root")
	forged := crlOf(t, impostor, impostorKey, time.Now().Add(time.Hour))
	err := VerifyCRLSignature(forged, []*x509.Certificate{root})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature does not verify")

	other, _ := crlCA(t, "Another CA")
	err = VerifyCRLSignature(crl, []*x509.Certificate{other})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no issuer certificate")

	assert.Error(t, VerifyCRLSignature(crl, nil))
}

func TestCheckCRLFresh(t *testing.T) {
	root, rootKey := crlCA(t, "Root")
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	fresh := crlOf(t, root, rootKey, now.Add(time.Minute))
	stale := crlOf(t, root, rootKey, now.Add(-time.Minute))

	assert.NoError(t, CheckCRLFresh(fresh, &CRLFreshness{Now: clock}))
	err := CheckCRLFresh(stale, &CRLFreshness{Now: clock})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stale")

	assert.NoError(t, CheckCRLFresh(stale, &CRLFreshness{Now: clock, Tolerance: 2 * time.Minute}), "tolerance covers it")
	assert.NoError(t, CheckCRLFresh(stale, nil), "no rule, no check")

	noNext := &x509.RevocationList{Issuer: pkix.Name{CommonName: "Root"}, ThisUpdate: now.Add(-time.Hour)}
	err = CheckCRLFresh(noNext, &CRLFreshness{Now: clock})
	require.Error(t, err, "a CRL without NextUpdate cannot prove it is current")
	assert.Contains(t, err.Error(), "no NextUpdate")
}

// A long-running relay verifies certificates long after load: a CRL that went stale since
// then must stop being trusted when a freshness rule is set.
func TestVerifyCertificateChain_StaleCRL(t *testing.T) {
	root, rootKey := crlCA(t, "Root")
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "Leaf"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, leafTmpl, root, &leafKey.PublicKey, rootKey)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(root)

	crl := crlOf(t, root, rootKey, time.Now().Add(time.Hour)) // fresh now
	opts := VerifyOptions{Roots: pool, CRLs: []*x509.RevocationList{crl}, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}}

	opts.CRLFreshness = &CRLFreshness{}
	_, err = VerifyCertificateChain(leaf, opts)
	require.NoError(t, err, "a fresh CRL is accepted")

	later := time.Now().Add(2 * time.Hour)
	opts.CRLFreshness = &CRLFreshness{Now: func() time.Time { return later }}
	_, err = VerifyCertificateChain(leaf, opts)
	require.Error(t, err, "the same CRL, two hours later, is stale")
	assert.Contains(t, err.Error(), "stale")

	opts.CRLFreshness = nil
	_, err = VerifyCertificateChain(leaf, opts)
	assert.NoError(t, err, "no freshness rule (dev) keeps the old behaviour")
}

// F1: a CRL must be bound to the certificate that actually issued the chain, not to a name.
// An impostor CA with the same name but another key must not be able to revoke (or, by being
// stale, break) a certificate of the real CA.
func TestCheckRevocation_BindsToTheChainIssuer(t *testing.T) {
	root, rootKey := crlCA(t, "Shared Name CA")
	impostor, impostorKey := crlCA(t, "Shared Name CA") // same name, different key

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(77), Subject: pkix.Name{CommonName: "Leaf"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, leafTmpl, root, &leafKey.PublicKey, rootKey)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(root)

	revoking := func(signer *x509.Certificate, key *ecdsa.PrivateKey, next time.Time) *x509.RevocationList {
		d, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
			Number: big.NewInt(1), ThisUpdate: next.Add(-time.Hour), NextUpdate: next,
			RevokedCertificateEntries: []x509.RevocationListEntry{{SerialNumber: leaf.SerialNumber, RevocationTime: time.Now()}},
		}, signer, key)
		require.NoError(t, err)
		crl, err := x509.ParseRevocationList(d)
		require.NoError(t, err)
		return crl
	}
	verify := func(crls ...*x509.RevocationList) error {
		_, err := VerifyCertificateChain(leaf, VerifyOptions{
			Roots: pool, CRLs: crls, CRLFreshness: &CRLFreshness{},
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		})
		return err
	}
	future := time.Now().Add(time.Hour)

	// The leaf is chain[0] and its issuer chain[1]: the root. The root itself is the anchor.
	assert.NoError(t, verify(revoking(impostor, impostorKey, future)),
		"a CRL from a same-name CA with another key must not revoke the leaf")
	assert.NoError(t, verify(revoking(impostor, impostorKey, time.Now().Add(-2*time.Hour))),
		"nor may its staleness break verification: it is not this issuer's CRL at all")
	err = verify(revoking(root, rootKey, future))
	require.Error(t, err, "the real issuer's CRL still revokes")
	assert.Contains(t, err.Error(), "revoked")
}

// F2: temporal validity is checked in full, not only NextUpdate.
func TestCheckCRLFresh_FullInterval(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	f := &CRLFreshness{Now: func() time.Time { return now }}
	crl := func(this, next time.Time) *x509.RevocationList {
		return &x509.RevocationList{Issuer: pkix.Name{CommonName: "CA"}, ThisUpdate: this, NextUpdate: next}
	}
	h := time.Hour

	assert.NoError(t, CheckCRLFresh(crl(now.Add(-h), now.Add(h)), f), "a current CRL is accepted")

	err := CheckCRLFresh(crl(now.Add(h), now.Add(2*h)), f)
	require.Error(t, err, "a CRL that starts in the future is not valid yet")
	assert.Contains(t, err.Error(), "not yet valid")

	err = CheckCRLFresh(crl(now, now), f)
	require.Error(t, err, "NextUpdate equal to ThisUpdate is not a sensible interval")
	assert.Contains(t, err.Error(), "inconsistent interval")
	err = CheckCRLFresh(crl(now.Add(-h), now.Add(-2*h)), f)
	require.Error(t, err, "NextUpdate before ThisUpdate is not a sensible interval")
	assert.Contains(t, err.Error(), "inconsistent interval")

	err = CheckCRLFresh(crl(time.Time{}, now.Add(h)), f)
	require.Error(t, err, "a CRL without ThisUpdate cannot prove its validity")
	assert.Contains(t, err.Error(), "no ThisUpdate")

	err = CheckCRLFresh(crl(now.Add(-3*h), now.Add(-h)), f)
	require.Error(t, err, "an expired CRL is rejected")
	assert.Contains(t, err.Error(), "stale")

	// The tolerance is the one explicit allowance and is symmetric.
	tol := &CRLFreshness{Now: f.Now, Tolerance: 2 * h}
	assert.NoError(t, CheckCRLFresh(crl(now.Add(-3*h), now.Add(-h)), tol), "grace after NextUpdate")
	assert.NoError(t, CheckCRLFresh(crl(now.Add(h), now.Add(3*h)), tol), "clock skew before ThisUpdate")
	assert.Error(t, CheckCRLFresh(crl(now.Add(3*h), now.Add(4*h)), tol), "skew beyond the tolerance is still rejected")
}
