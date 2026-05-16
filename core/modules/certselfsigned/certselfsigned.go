// Package certselfsigned implements the cert.selfsigned native module.
// It generates a self-signed X.509 certificate and returns the PEM-encoded
// certificate and private key, so the result can be recorded in the ProofTrace.
package certselfsigned

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// Input is the expected input structure for the cert.selfsigned module.
type Input struct {
	CN           string   `json:"cn"`            // Common Name (required)
	DNSSANs      []string `json:"dns_sans"`      // DNS Subject Alternative Names (optional)
	ValidityDays int      `json:"validity_days"` // Certificate validity in days (default: 365)
}

// Output is the result returned by Generate.
type Output struct {
	CertPEM    string `json:"cert_pem"`    // PEM-encoded certificate
	KeyPEM     string `json:"key_pem"`     // PEM-encoded private key
	CommonName string `json:"common_name"` // CN from the generated cert
	NotBefore  string `json:"not_before"`  // RFC3339
	NotAfter   string `json:"not_after"`   // RFC3339
}

// Test-injectable function hooks for crypto operations.
var (
	generateKey         = func() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }
	generateSerial      = func() (*big.Int, error) { return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128)) }
	createCertificate   = func(template, parent *x509.Certificate, pub, priv interface{}) ([]byte, error) {
		return x509.CreateCertificate(rand.Reader, template, parent, pub, priv)
	}
	marshalECPrivateKey = x509.MarshalECPrivateKey
)

// Generate creates a self-signed ECDSA P-256 certificate.
// Satisfies the cabinet NativeFunc signature:
//
//	func(context.Context, interface{}) (interface{}, error)
func Generate(ctx context.Context, input interface{}) (interface{}, error) {
	in, err := parseInput(input)
	if err != nil {
		return nil, fmt.Errorf("cert.selfsigned: invalid input: %w", err)
	}

	key, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("cert.selfsigned: generate key: %w", err)
	}

	validityDays := in.ValidityDays
	if validityDays <= 0 {
		validityDays = 365
	}

	now := time.Now().UTC()
	notAfter := now.Add(time.Duration(validityDays) * 24 * time.Hour)

	serial, err := generateSerial()
	if err != nil {
		return nil, fmt.Errorf("cert.selfsigned: generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: in.CN},
		DNSNames:     in.DNSSANs,
		NotBefore:    now,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         false,
	}

	certDER, err := createCertificate(template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("cert.selfsigned: create certificate: %w", err)
	}

	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))

	keyDER, err := marshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("cert.selfsigned: marshal key: %w", err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))

	return &Output{
		CertPEM:    certPEM,
		KeyPEM:     keyPEM,
		CommonName: in.CN,
		NotBefore:  now.Format(time.RFC3339),
		NotAfter:   notAfter.Format(time.RFC3339),
	}, nil
}

func parseInput(raw interface{}) (*Input, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	if in.CN == "" {
		return nil, fmt.Errorf("cn is required")
	}
	return &in, nil
}
