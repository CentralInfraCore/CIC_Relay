package cabinet

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
)

// VerifyOptions encapsulates the parameters for certificate chain verification.
// This structure will hold the trusted root CAs, expected key usages, CRLs, and SPKI pins.
type VerifyOptions struct {
	Roots         *x509.CertPool
	Intermediates *x509.CertPool
	KeyUsages     []x509.ExtKeyUsage
	CRLs          []*x509.RevocationList // Added for M3 revocation check
	PinnedSPKIs   map[string]bool        // Added for M3 SPKI pinning. Map of base64-encoded SHA-256 SPKI hashes.
}

// checkRevocation checks if any certificate in the provided chains is in the revocation list.
func checkRevocation(chains [][]*x509.Certificate, crls []*x509.RevocationList) error {
	if len(crls) == 0 {
		return nil // No CRLs to check against
	}

	for _, chain := range chains {
		for _, cert := range chain {
			for _, crl := range crls {
				for _, revokedCert := range crl.RevokedCertificateEntries {
					if cert.SerialNumber.Cmp(revokedCert.SerialNumber) == 0 {
						return fmt.Errorf("certificate with serial number %s is revoked", cert.SerialNumber)
					}
				}
			}
		}
	}
	return nil
}

// checkSPKIPinning checks if any certificate in the chains matches the pinned SPKI hashes.
func checkSPKIPinning(chains [][]*x509.Certificate, pinnedSPKIs map[string]bool) error {
	if len(pinnedSPKIs) == 0 {
		return nil // No pins to check against
	}

	for _, chain := range chains {
		// The chain is ordered from leaf to root. We check each cert against the pins.
		for _, cert := range chain {
			// Calculate SHA-256 hash of the Subject Public Key Info (SPKI).
			spkiHash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
			// Base64 encode the hash.
			spkiBase64 := base64.StdEncoding.EncodeToString(spkiHash[:])

			if pinnedSPKIs[spkiBase64] {
				// Found a matching pin, SPKI check is successful.
				return nil
			}
		}
	}

	// If we reach here, no certificate in any chain matched any of the pins.
	return fmt.Errorf("no certificate in the chain matched any of the pinned SPKI hashes")
}

// VerifyCertificateChain checks a certificate against a trust store and specific key usages.
// This is the core function for M3 - Private PKI verification.
func VerifyCertificateChain(cert *x509.Certificate, opts VerifyOptions) ([][]*x509.Certificate, error) {
	if opts.Roots == nil {
		return nil, fmt.Errorf("a root CA pool must be provided")
	}

	// Create the verification options
	verifyOpts := x509.VerifyOptions{
		Roots:         opts.Roots,
		Intermediates: opts.Intermediates,
		KeyUsages:     opts.KeyUsages,
	}

	// 1. Verify the certificate chain
	chains, err := cert.Verify(verifyOpts)
	if err != nil {
		return nil, fmt.Errorf("certificate verification failed: %w", err)
	}

	// 2. Check for revocation
	if err := checkRevocation(chains, opts.CRLs); err != nil {
		return nil, fmt.Errorf("revocation check failed: %w", err)
	}

	// 3. Check for SPKI pinning
	if err := checkSPKIPinning(chains, opts.PinnedSPKIs); err != nil {
		return nil, fmt.Errorf("SPKI pinning validation failed: %w", err)
	}

	return chains, nil
}
