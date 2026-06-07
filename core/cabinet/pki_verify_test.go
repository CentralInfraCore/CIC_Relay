package cabinet

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestCertificate is a helper function to generate certificates for testing.
func createTestCertificate(t *testing.T, template, parent *x509.Certificate, pub, priv any) *x509.Certificate {
	certBytes, err := x509.CreateCertificate(rand.Reader, template, parent, pub, priv)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(certBytes)
	require.NoError(t, err)
	return cert
}

// generateCACert generates a self-signed CA certificate.
func generateCACert(t *testing.T, subject pkix.Name) (*x509.Certificate, any) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               subject,
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	return createTestCertificate(t, template, template, &priv.PublicKey, priv), priv
}

func TestVerifyCertificateChain(t *testing.T) {
	// 1. Create a full PKI chain: Root CA -> Intermediate CA -> Leaf Certificate
	rootCA, rootKey := generateCACert(t, pkix.Name{CommonName: "Test Root CA"})
	intermediateCA, intermediateKey := generateCACert(t, pkix.Name{CommonName: "Test Intermediate CA"})

	// The intermediate is signed by the root
	intermediateCA = createTestCertificate(t, intermediateCA, rootCA, intermediateCA.PublicKey, rootKey)

	// Create the leaf certificate for code signing
	leafPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "Test Leaf"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	leafCert := createTestCertificate(t, leafTemplate, intermediateCA, &leafPriv.PublicKey, intermediateKey)

	// 2. Setup verification options
	rootPool := x509.NewCertPool()
	rootPool.AddCert(rootCA)
	intermediatePool := x509.NewCertPool()
	intermediatePool.AddCert(intermediateCA)

	t.Run("successful verification with correct chain and EKU", func(t *testing.T) {
		opts := VerifyOptions{
			Roots:         rootPool,
			Intermediates: intermediatePool,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		}

		chains, err := VerifyCertificateChain(leafCert, opts)
		require.NoError(t, err)
		assert.NotEmpty(t, chains, "Should return at least one valid chain")
	})

	t.Run("fails with wrong EKU", func(t *testing.T) {
		opts := VerifyOptions{
			Roots:         rootPool,
			Intermediates: intermediatePool,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, // Expecting ServerAuth, but cert has CodeSigning
		}

		_, err := VerifyCertificateChain(leafCert, opts)
		assert.Error(t, err, "Should fail due to EKU mismatch")
		assert.Contains(t, err.Error(), "incompatible key usage", "Error message should indicate EKU issue")
	})

	t.Run("fails with untrusted root", func(t *testing.T) {
		// Create a different, untrusted root
		untrustedRoot, _ := generateCACert(t, pkix.Name{CommonName: "Untrusted Root"})
		untrustedPool := x509.NewCertPool()
		untrustedPool.AddCert(untrustedRoot)

		opts := VerifyOptions{
			Roots:         untrustedPool,
			Intermediates: intermediatePool,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		}

		_, err := VerifyCertificateChain(leafCert, opts)
		assert.Error(t, err, "Should fail because the root is not trusted")
		assert.Contains(t, err.Error(), "certificate signed by unknown authority", "Error message should indicate trust issue")
	})

	t.Run("fails with incomplete chain", func(t *testing.T) {
		// Verify without providing the intermediate certificate pool
		opts := VerifyOptions{
			Roots:     rootPool,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		}

		_, err := VerifyCertificateChain(leafCert, opts)
		assert.Error(t, err, "Should fail because the chain is incomplete")
		assert.Contains(t, err.Error(), "certificate signed by unknown authority", "Error message should indicate a break in the trust chain")
	})

	t.Run("fails with revoked certificate", func(t *testing.T) {
		// Create a CRL that revokes the leaf certificate
		revokedCerts := []x509.RevocationListEntry{
			{
				SerialNumber:   leafCert.SerialNumber,
				RevocationTime: time.Now(),
			},
		}
		crlTemplate := x509.RevocationList{
			Number:                    big.NewInt(1),
			RevokedCertificateEntries: revokedCerts,
			ThisUpdate:                time.Now(),
			NextUpdate:                time.Now().Add(24 * time.Hour),
		}

		crlBytes, err := x509.CreateRevocationList(rand.Reader, &crlTemplate, intermediateCA, intermediateKey.(crypto.Signer))
		require.NoError(t, err)

		crl, err := x509.ParseRevocationList(crlBytes)
		require.NoError(t, err)

		// Setup verification options including the CRL
		opts := VerifyOptions{
			Roots:         rootPool,
			Intermediates: intermediatePool,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
			CRLs:          []*x509.RevocationList{crl},
		}

		// Execute and verify
		_, err = VerifyCertificateChain(leafCert, opts)
		assert.Error(t, err, "Should fail because the certificate is revoked")
		assert.Contains(t, err.Error(), "revocation check failed", "Error message should indicate revocation issue")
		assert.Contains(t, err.Error(), "certificate with serial number 3 is revoked", "Error message should specify the revoked serial number")
	})

	t.Run("successful verification with non-revoked certificate and CRL", func(t *testing.T) {
		// Create a CRL that revokes a *different* certificate
		revokedCerts := []x509.RevocationListEntry{
			{
				SerialNumber:   big.NewInt(99),
				RevocationTime: time.Now(),
			},
		}

		crlTemplate := x509.RevocationList{
			Number:                    big.NewInt(2),
			RevokedCertificateEntries: revokedCerts,
			ThisUpdate:                time.Now(),
			NextUpdate:                time.Now().Add(24 * time.Hour),
		}

		crlBytes, err := x509.CreateRevocationList(rand.Reader, &crlTemplate, intermediateCA, intermediateKey.(crypto.Signer))
		require.NoError(t, err)

		crl, err := x509.ParseRevocationList(crlBytes)
		require.NoError(t, err)

		// Setup verification options including the CRL
		opts := VerifyOptions{
			Roots:         rootPool,
			Intermediates: intermediatePool,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
			CRLs:          []*x509.RevocationList{crl},
		}

		// Execute and verify that it succeeds
		_, err = VerifyCertificateChain(leafCert, opts)
		assert.NoError(t, err, "Should succeed as the certificate is not in the CRL")
	})

	t.Run("fails when no root CA pool is provided", func(t *testing.T) {
		opts := VerifyOptions{
			Roots: nil, // No root pool
		}

		_, err := VerifyCertificateChain(leafCert, opts)
		assert.Error(t, err, "Should fail because no root CA pool is provided")
		assert.Contains(t, err.Error(), "a root CA pool must be provided", "Error message should indicate missing root pool")
	})

	// --- SPKI Pinning Tests ---
	// Calculate the SPKI hash for the intermediate CA
	spkiHash := sha256.Sum256(intermediateCA.RawSubjectPublicKeyInfo)
	correctPin := base64.StdEncoding.EncodeToString(spkiHash[:])

	t.Run("successful verification with correct SPKI pin", func(t *testing.T) {
		opts := VerifyOptions{
			Roots:         rootPool,
			Intermediates: intermediatePool,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
			PinnedSPKIs:   map[string]bool{correctPin: true},
		}

		chains, err := VerifyCertificateChain(leafCert, opts)
		require.NoError(t, err)
		assert.NotEmpty(t, chains, "Should return at least one valid chain with correct pinning")
	})

	t.Run("fails with incorrect SPKI pin", func(t *testing.T) {
		incorrectPin := "dGVzdFBpbgo=\"" // "testPin" in base64
		opts := VerifyOptions{
			Roots:         rootPool,
			Intermediates: intermediatePool,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
			PinnedSPKIs:   map[string]bool{incorrectPin: true},
		}

		_, err := VerifyCertificateChain(leafCert, opts)
		assert.Error(t, err, "Should fail because of SPKI pin mismatch")
		assert.Contains(t, err.Error(), "SPKI pinning validation failed", "Error message should indicate pinning issue")
		assert.Contains(t, err.Error(), "no certificate in the chain matched any of the pinned SPKI hashes", "Error message should specify the pinning failure")
	})
}
