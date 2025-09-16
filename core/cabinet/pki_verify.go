package cabinet

import (
	"crypto/x509"
	"fmt"
)

// VerifyOptions encapsulates the parameters for certificate chain verification.
// This structure will hold the trusted root CAs and expected key usages.
type VerifyOptions struct {
	Roots *x509.CertPool
	Intermediates *x509.CertPool
	KeyUsages     []x509.ExtKeyUsage
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

	// Verify the certificate chain
	chains, err := cert.Verify(verifyOpts)
	if err != nil {
		return nil, fmt.Errorf("certificate verification failed: %w", err)
	}

	return chains, nil
}
