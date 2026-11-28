package main

// PR-06 (#169 "Relay-specific trust boundaries", module identity): negative tests at the PRODUCTION entry point,
// loadModule, for the ways a signer certificate can be unacceptable that were only tested one layer down
// (core/cabinet) or not at all: expired, not yet valid, revoked. A matching digest is not proof of origin or
// authorization; the verdict has to come from the certificate chain.
//
// Every negative case has a positive control built from the SAME CA and the same code path: the only difference is
// the property under test, so a rejection cannot be a side effect of a broken fixture.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"centralrelay/core/cabinet"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCA struct {
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "PR06 Test Root CA"},
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
	return &testCA{key: key, cert: cert}
}

// issue returns a code-signing leaf (PEM and parsed) valid in [notBefore, notAfter].
func (ca *testCA) issue(t *testing.T, serial int64, notBefore, notAfter time.Time) ([]byte, *x509.Certificate) {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "PR06 Test Signer"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &leafKey.PublicKey, ca.key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cert
}

// crl returns a CRL signed by the CA that revokes the given serial numbers.
func (ca *testCA) crl(t *testing.T, revoked ...int64) *x509.RevocationList {
	t.Helper()
	var entries []x509.RevocationListEntry
	for _, s := range revoked {
		entries = append(entries, x509.RevocationListEntry{SerialNumber: big.NewInt(s), RevocationTime: time.Now().Add(-time.Hour)})
	}
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number:                    big.NewInt(1),
		ThisUpdate:                time.Now().Add(-time.Hour),
		NextUpdate:                time.Now().Add(24 * time.Hour),
		RevokedCertificateEntries: entries,
	}, ca.cert, ca.key)
	require.NoError(t, err)
	list, err := x509.ParseRevocationList(der)
	require.NoError(t, err)
	return list
}

func (ca *testCA) assets(crls ...*x509.RevocationList) *PKIAssets {
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	return &PKIAssets{
		Roots:            roots,
		Intermediates:    x509.NewCertPool(),
		CRLs:             crls,
		CRLFreshness:     &cabinet.CRLFreshness{},
		TrustStoreLoaded: true,
	}
}

func loadWithSigner(t *testing.T, signerPEM []byte, assets *PKIAssets) (string, error) {
	t.Helper()
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(),
		cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1})
	require.NoError(t, err)
	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	moduleFS, yamlPath := buildModuleFS(t, wasm, signerPEM)
	var logBuf bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logBuf)
	err = loadModule(context.Background(), svc, moduleFS, yamlPath, moduleFS[yamlPath].Data, assets, int64(16*1024*1024), logger)
	return logBuf.String(), err
}

func TestLoadModule_SignerCertificate_Acceptable_And_Unacceptable(t *testing.T) {
	ca := newTestCA(t)
	now := time.Now()
	cases := []struct {
		name      string
		serial    int64
		notBefore time.Time
		notAfter  time.Time
		revoked   bool
		wantErr   string // empty: the positive control, must load and log "verified"
	}{
		{"control: a valid signer of the trusted CA", 10, now.Add(-time.Hour), now.Add(24 * time.Hour), false, ""},
		{"control: a valid signer and a CRL that does not list it", 11, now.Add(-time.Hour), now.Add(24 * time.Hour), false, ""},
		{"expired signer", 12, now.Add(-48 * time.Hour), now.Add(-time.Hour), false, "expired"},
		{"signer that is not valid yet", 13, now.Add(time.Hour), now.Add(48 * time.Hour), false, "expired"},
		{"revoked signer", 14, now.Add(-time.Hour), now.Add(24 * time.Hour), true, "revoked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			signer, _ := ca.issue(t, c.serial, c.notBefore, c.notAfter)
			var crls []*x509.RevocationList
			if c.revoked || c.serial == 11 {
				if c.revoked {
					crls = append(crls, ca.crl(t, c.serial))
				} else {
					crls = append(crls, ca.crl(t, 99999))
				}
			}
			log, err := loadWithSigner(t, signer, ca.assets(crls...))
			if c.wantErr == "" {
				require.NoError(t, err)
				assert.Contains(t, log, "verified")
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "PKI verification failed")
			assert.Contains(t, err.Error(), c.wantErr, "the rejection must name the real cause, not just fail")
			assert.NotContains(t, log, "outcome=verified")
		})
	}
}

// A rejected signer must also leave the module unregistered: failing the check and still loading is not a rejection.
func TestLoadModule_RejectedSigner_LeavesNoModuleRegistered(t *testing.T) {
	ca := newTestCA(t)
	now := time.Now()
	signer, _ := ca.issue(t, 20, now.Add(-48*time.Hour), now.Add(-time.Hour)) // expired
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(),
		cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1})
	require.NoError(t, err)
	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	moduleFS, yamlPath := buildModuleFS(t, wasm, signer)
	err = loadModule(context.Background(), svc, moduleFS, yamlPath, moduleFS[yamlPath].Data, ca.assets(), int64(16*1024*1024), newDiscardLogger())
	require.Error(t, err)
	_, getErr := svc.GetModule("pki.test@1.0.0")
	assert.Error(t, getErr, "a module whose signer was rejected must not be in the registry")
}
