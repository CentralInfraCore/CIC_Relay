package cabinet

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"time"
)

// VerifyOptions encapsulates the parameters for certificate chain verification.
// This structure will hold the trusted root CAs, expected key usages, CRLs, and SPKI pins.
type VerifyOptions struct {
	Roots         *x509.CertPool
	Intermediates *x509.CertPool
	KeyUsages     []x509.ExtKeyUsage
	CRLs          []*x509.RevocationList // Added for M3 revocation check
	PinnedSPKIs   map[string]bool        // Added for M3 SPKI pinning. Map of base64-encoded SHA-256 SPKI hashes.

	// CRLFreshness, when set, rejects a verification that would rely on a CRL past its
	// NextUpdate (plus tolerance). Nil leaves freshness unchecked (the dev behaviour).
	CRLFreshness *CRLFreshness
}

// CRLFreshness is the freshness rule for CRLs. A CRL without a NextUpdate cannot prove
// it is current and counts as stale.
type CRLFreshness struct {
	Now       func() time.Time // nil means time.Now
	Tolerance time.Duration    // grace after NextUpdate; zero means none
}

// CheckCRLFresh returns an error unless crl is currently valid: it must state a sensible
// interval (a ThisUpdate, and a NextUpdate after it), must not start in the future, and must
// not be past NextUpdate. Tolerance is the one explicit allowance, applied symmetrically: a
// grace after NextUpdate and the clock skew accepted before ThisUpdate. Zero means none.
func CheckCRLFresh(crl *x509.RevocationList, f *CRLFreshness) error {
	if f == nil {
		return nil
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	if crl.ThisUpdate.IsZero() {
		return fmt.Errorf("CRL of %s has no ThisUpdate, so its validity cannot be established", crl.Issuer)
	}
	if crl.NextUpdate.IsZero() {
		return fmt.Errorf("CRL of %s has no NextUpdate, so its freshness cannot be established", crl.Issuer)
	}
	if !crl.NextUpdate.After(crl.ThisUpdate) {
		return fmt.Errorf("CRL of %s has an inconsistent interval: NextUpdate %s is not after ThisUpdate %s",
			crl.Issuer, crl.NextUpdate.UTC().Format(time.RFC3339), crl.ThisUpdate.UTC().Format(time.RFC3339))
	}
	if now().Add(f.Tolerance).Before(crl.ThisUpdate) {
		return fmt.Errorf("CRL of %s is not yet valid: ThisUpdate %s is in the future (tolerance %s)",
			crl.Issuer, crl.ThisUpdate.UTC().Format(time.RFC3339), f.Tolerance)
	}
	if now().After(crl.NextUpdate.Add(f.Tolerance)) {
		return fmt.Errorf("CRL of %s is stale: NextUpdate %s, tolerance %s",
			crl.Issuer, crl.NextUpdate.UTC().Format(time.RFC3339), f.Tolerance)
	}
	return nil
}

// VerifyCRLSignature checks that crl is signed by one of the given issuer certificates
// (matched by the CRL's issuer name). A CRL whose issuer is not among them, or whose
// signature does not verify against the issuer's key, must not be used: its entries are
// unauthenticated, and a forged one could both revoke and hide revocations.
func VerifyCRLSignature(crl *x509.RevocationList, issuers []*x509.Certificate) error {
	var lastErr error
	for _, issuer := range issuers {
		if !bytes.Equal(crl.RawIssuer, issuer.RawSubject) {
			continue
		}
		if err := crl.CheckSignatureFrom(issuer); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("CRL of %s: signature does not verify against the issuer certificate: %w", crl.Issuer, lastErr)
	}
	return fmt.Errorf("CRL of %s: no issuer certificate for it in the trust store", crl.Issuer)
}

// checkRevocation checks if any certificate in the provided chains is in the revocation list.
// With a freshness rule, a CRL that applies to a certificate of the chain and is stale is an error.
func checkRevocation(chains [][]*x509.Certificate, crls []*x509.RevocationList, fresh *CRLFreshness) error {
	if len(crls) == 0 {
		return nil // No CRLs to check against
	}

	for _, chain := range chains {
		// chain is leaf-first and verified: chain[i+1] is the certificate that actually issued
		// chain[i]. The last element is the trust anchor, which has no issuer to be revoked by.
		for i := 0; i+1 < len(chain); i++ {
			cert, issuer := chain[i], chain[i+1]
			for _, crl := range crls {
				// A CRL counts for cert only if it was issued by cert's real issuer: same name
				// AND a signature that verifies against that very certificate's key. Matching the
				// name alone would let a CA that merely shares a name (with another key) speak
				// for it, both to revoke and to vouch.
				if !bytes.Equal(crl.RawIssuer, issuer.RawSubject) || crl.CheckSignatureFrom(issuer) != nil {
					continue
				}
				if err := CheckCRLFresh(crl, fresh); err != nil {
					return err
				}
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
	if err := checkRevocation(chains, opts.CRLs, opts.CRLFreshness); err != nil {
		return nil, fmt.Errorf("revocation check failed: %w", err)
	}

	// 3. Check for SPKI pinning
	if err := checkSPKIPinning(chains, opts.PinnedSPKIs); err != nil {
		return nil, fmt.Errorf("SPKI pinning validation failed: %w", err)
	}

	return chains, nil
}
