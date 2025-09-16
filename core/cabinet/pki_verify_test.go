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
}
