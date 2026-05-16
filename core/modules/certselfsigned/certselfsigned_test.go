package certselfsigned

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerate_Success(t *testing.T) {
	input := map[string]interface{}{
		"cn":           "test.local",
		"dns_sans":     []interface{}{"test.local", "www.test.local"},
		"validity_days": 30,
	}

	result, err := Generate(context.Background(), input)
	require.NoError(t, err)

	out, ok := result.(*Output)
	require.True(t, ok)

	assert.Equal(t, "test.local", out.CommonName)
	assert.NotEmpty(t, out.CertPEM)
	assert.NotEmpty(t, out.KeyPEM)
	assert.NotEmpty(t, out.NotBefore)
	assert.NotEmpty(t, out.NotAfter)

	// Verify the generated cert is parseable and has the right CN + SANs
	block, _ := pem.Decode([]byte(out.CertPEM))
	require.NotNil(t, block, "cert PEM must be decodable")
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.Equal(t, "test.local", cert.Subject.CommonName)
	assert.Contains(t, cert.DNSNames, "test.local")
	assert.Contains(t, cert.DNSNames, "www.test.local")
	assert.False(t, cert.IsCA)
}

func TestGenerate_DefaultValidity(t *testing.T) {
	input := map[string]interface{}{"cn": "default.local"}

	result, err := Generate(context.Background(), input)
	require.NoError(t, err)

	out := result.(*Output)
	block, _ := pem.Decode([]byte(out.CertPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	// Default validity is 365 days — NotAfter − NotBefore ≈ 365 days
	validity := cert.NotAfter.Sub(cert.NotBefore)
	assert.InDelta(t, 365*24, validity.Hours(), 1)
}

func TestGenerate_NoCN_Error(t *testing.T) {
	_, err := Generate(context.Background(), map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cn is required")
}

func TestGenerate_InvalidInput_Error(t *testing.T) {
	_, err := Generate(context.Background(), "not-an-object")
	require.Error(t, err)
}

func TestGenerate_SelfSigned(t *testing.T) {
	input := map[string]interface{}{"cn": "selfsigned.local"}
	result, err := Generate(context.Background(), input)
	require.NoError(t, err)

	out := result.(*Output)
	block, _ := pem.Decode([]byte(out.CertPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	// Self-signed: Issuer == Subject
	assert.Equal(t, cert.Subject.String(), cert.Issuer.String())
}

func TestGenerate_MarshalError(t *testing.T) {
	// Channels cannot be marshaled to JSON, triggering the json.Marshal error path.
	_, err := Generate(context.Background(), make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid input")
}

func TestGenerate_KeyGenError(t *testing.T) {
	orig := generateKey
	t.Cleanup(func() { generateKey = orig })
	generateKey = func() (*ecdsa.PrivateKey, error) { return nil, fmt.Errorf("entropy exhausted") }

	_, err := Generate(context.Background(), map[string]interface{}{"cn": "test.local"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate key")
}

func TestGenerate_SerialError(t *testing.T) {
	orig := generateSerial
	t.Cleanup(func() { generateSerial = orig })
	generateSerial = func() (*big.Int, error) { return nil, fmt.Errorf("serial gen failed") }

	_, err := Generate(context.Background(), map[string]interface{}{"cn": "test.local"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate serial")
}

func TestGenerate_CertCreateError(t *testing.T) {
	orig := createCertificate
	t.Cleanup(func() { createCertificate = orig })
	createCertificate = func(_, _ *x509.Certificate, _, _ interface{}) ([]byte, error) {
		return nil, fmt.Errorf("cert create failed")
	}

	_, err := Generate(context.Background(), map[string]interface{}{"cn": "test.local"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create certificate")
}

func TestGenerate_MarshalKeyError(t *testing.T) {
	orig := marshalECPrivateKey
	t.Cleanup(func() { marshalECPrivateKey = orig })
	marshalECPrivateKey = func(_ *ecdsa.PrivateKey) ([]byte, error) { return nil, fmt.Errorf("marshal failed") }

	_, err := Generate(context.Background(), map[string]interface{}{"cn": "test.local"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal key")
}
