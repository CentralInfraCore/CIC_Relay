package schemacompile_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"centralrelay/core/modules/schemacompile"
	"centralrelay/pkg/canonicaljson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// specChecksumHex computes sha256(canonical_json(spec)) as a lowercase hex string.
// This mirrors CIC-Schemas artifact.py:compute_spec_checksum.
func specChecksumHex(spec interface{}) string {
	b, err := canonicaljson.ToJSON(spec)
	if err != nil {
		panic("specChecksumHex: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Pre-computed checksums for common spec shapes used across tests.
var (
	// emptySpecChecksum = sha256(canonical_json({}))
	emptySpecChecksum = specChecksumHex(map[string]interface{}{})
	// fieldsSpecChecksum = sha256(canonical_json({"fields":[]}))
	fieldsSpecChecksum = specChecksumHex(map[string]interface{}{"fields": []interface{}{}})
)

// --- PKI helpers ---

func makeCACert(t *testing.T, cn string, parent *x509.Certificate, parentKey any) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if parent == nil {
		// self-signed
		parent = tmpl
		parentKey = priv
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &priv.PublicKey, parentKey)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(certBytes)
	require.NoError(t, err)
	return cert, priv
}

func makeLeafCert(t *testing.T, issuer *x509.Certificate, issuerKey any) (*x509.Certificate, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "developer@cic"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, issuer, &priv.PublicKey, issuerKey)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	cert, err := x509.ParseCertificate(certBytes)
	require.NoError(t, err)
	_ = cert
	return cert, string(pemBytes)
}

func makeVerifier(roots, intermediates *x509.CertPool) schemacompile.CertVerifyFunc {
	return func(cert *x509.Certificate) error {
		opts := x509.VerifyOptions{
			Roots:         roots,
			Intermediates: intermediates,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		}
		_, err := cert.Verify(opts)
		return err
	}
}

func assertionYAML(certPEM string) string {
	return fmt.Sprintf(`metadata:
  name: test.schema
  version: "1.0"
  checksum: "%s"
  sign: "sig"
  createdBy:
    certificate: |
%s
spec:
  fields: []
`, fieldsSpecChecksum, indentCert(certPEM))
}

func indentCert(certPEM string) string {
	result := ""
	for _, line := range splitLines(certPEM) {
		result += "      " + line + "\n"
	}
	return result
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// --- tests ---

func TestAssertSource_StubMode_NoAssertion(t *testing.T) {
	fn := schemacompile.NewAssertSourceFunc(nil)
	out, err := fn(context.Background(), map[string]interface{}{
		"schema":  "some.schema",
		"version": "1.0",
	})
	require.NoError(t, err)
	m := out.(map[string]interface{})
	assert.Equal(t, true, m["asserted"])
	assert.Equal(t, "1.0", m["policy_version"])
}

func TestAssertSource_WithVerifier_ValidChain(t *testing.T) {
	rootCA, rootKey := makeCACert(t, "CICRootCA", nil, nil)
	intermediateCA, intermediateKey := makeCACert(t, "CICDeveloperCA2026", rootCA, rootKey)
	_, leafPEM := makeLeafCert(t, intermediateCA, intermediateKey)

	rootPool := x509.NewCertPool()
	rootPool.AddCert(rootCA)
	intermediatePool := x509.NewCertPool()
	intermediatePool.AddCert(intermediateCA)

	fn := schemacompile.NewAssertSourceFunc(makeVerifier(rootPool, intermediatePool))
	out, err := fn(context.Background(), map[string]interface{}{
		"source_assertion": assertionYAML(leafPEM),
		"schema":           "test.schema",
		"version":          "1.0",
	})
	require.NoError(t, err)
	m := out.(map[string]interface{})
	assert.Equal(t, true, m["asserted"])
	assert.Equal(t, "test.schema", m["schema"])
}

func TestAssertSource_WithVerifier_UntrustedCA(t *testing.T) {
	rootCA, rootKey := makeCACert(t, "CICRootCA", nil, nil)
	intermediateCA, intermediateKey := makeCACert(t, "CICDeveloperCA2026", rootCA, rootKey)
	_, leafPEM := makeLeafCert(t, intermediateCA, intermediateKey)

	// Use a different root — cert should be rejected
	untrustedRoot, _ := makeCACert(t, "UntrustedRoot", nil, nil)
	untrustedPool := x509.NewCertPool()
	untrustedPool.AddCert(untrustedRoot)

	fn := schemacompile.NewAssertSourceFunc(makeVerifier(untrustedPool, nil))
	_, err := fn(context.Background(), map[string]interface{}{
		"source_assertion": assertionYAML(leafPEM),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate verification failed")
}

func TestAssertSource_WithVerifier_InvalidPEM(t *testing.T) {
	fn := schemacompile.NewAssertSourceFunc(func(cert *x509.Certificate) error { return nil })
	yamlStr := fmt.Sprintf(`metadata:
  name: test.schema
  version: "1.0"
  checksum: "%s"
  sign: "sig"
  createdBy:
    certificate: "not-a-pem"
spec: {}
`, emptySpecChecksum)
	_, err := fn(context.Background(), map[string]interface{}{
		"source_assertion": yamlStr,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid PEM CERTIFICATE block")
}

func TestAssertSource_MissingRequiredField(t *testing.T) {
	fn := schemacompile.NewAssertSourceFunc(nil)
	yaml := `metadata:
  name: test.schema
  version: "1.0"
  createdBy:
    certificate: "stub"
spec: {}
`
	_, err := fn(context.Background(), map[string]interface{}{
		"source_assertion": yaml,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata.checksum is required")
}

func TestAssertSource_SchemaMismatch(t *testing.T) {
	fn := schemacompile.NewAssertSourceFunc(nil)
	_, err := fn(context.Background(), map[string]interface{}{
		"schema":           "other.schema",
		"version":          "1.0",
		"source_assertion": minimalAssertion("test.schema", "1.0"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema mismatch")
}

func TestAssertSource_VersionMismatch(t *testing.T) {
	fn := schemacompile.NewAssertSourceFunc(nil)
	_, err := fn(context.Background(), map[string]interface{}{
		"schema":           "test.schema",
		"version":          "2.0",
		"source_assertion": minimalAssertion("test.schema", "1.0"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version mismatch")
}

func TestAssertSource_NilVerifier_SkipsCertCheck(t *testing.T) {
	// With nil verifier, even an invalid cert PEM should not fail cert check
	fn := schemacompile.NewAssertSourceFunc(nil)
	yamlStr := fmt.Sprintf(`metadata:
  name: test.schema
  version: "1.0"
  checksum: "%s"
  sign: "sig"
  createdBy:
    certificate: "not-a-pem"
spec: {}
`, emptySpecChecksum)
	out, err := fn(context.Background(), map[string]interface{}{
		"source_assertion": yamlStr,
	})
	require.NoError(t, err)
	assert.Equal(t, true, out.(map[string]interface{})["asserted"])
}

func minimalAssertion(name, version string) string {
	return fmt.Sprintf(`metadata:
  name: %s
  version: "%s"
  checksum: "%s"
  sign: "sig"
  createdBy:
    certificate: "stub"
spec: {}
`, name, version, emptySpecChecksum)
}

// --- L0.4: metadata.checksum cross-validation tests ---

func TestAssertSource_L04_ChecksumMismatch(t *testing.T) {
	fn := schemacompile.NewAssertSourceFunc(nil)
	// Provide a wrong checksum — should be rejected.
	yamlStr := `metadata:
  name: test.schema
  version: "1.0"
  checksum: "deadbeef"
  sign: "sig"
  createdBy:
    certificate: "stub"
spec: {}
`
	_, err := fn(context.Background(), map[string]interface{}{
		"source_assertion": yamlStr,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata.checksum mismatch")
	assert.Contains(t, err.Error(), "deadbeef")
}

func TestAssertSource_L04_ChecksumValid_EmptySpec(t *testing.T) {
	fn := schemacompile.NewAssertSourceFunc(nil)
	// emptySpecChecksum matches sha256(canonical_json({})).
	yamlStr := fmt.Sprintf(`metadata:
  name: test.schema
  version: "1.0"
  checksum: "%s"
  sign: "sig"
  createdBy:
    certificate: "stub"
spec: {}
`, emptySpecChecksum)
	out, err := fn(context.Background(), map[string]interface{}{
		"source_assertion": yamlStr,
	})
	require.NoError(t, err)
	assert.Equal(t, true, out.(map[string]interface{})["asserted"])
}

func TestAssertSource_L04_ChecksumValid_NonEmptySpec(t *testing.T) {
	fn := schemacompile.NewAssertSourceFunc(nil)
	// fieldsSpecChecksum matches sha256(canonical_json({"fields":[]})).
	yamlStr := fmt.Sprintf(`metadata:
  name: test.schema
  version: "1.0"
  checksum: "%s"
  sign: "sig"
  createdBy:
    certificate: "stub"
spec:
  fields: []
`, fieldsSpecChecksum)
	out, err := fn(context.Background(), map[string]interface{}{
		"source_assertion": yamlStr,
	})
	require.NoError(t, err)
	assert.Equal(t, true, out.(map[string]interface{})["asserted"])
}

func TestAssertSource_L04_NilSpec_SkipsChecksumCheck(t *testing.T) {
	fn := schemacompile.NewAssertSourceFunc(nil)
	// spec absent (nil after unmarshal) — checksum check is skipped.
	yamlStr := `metadata:
  name: test.schema
  version: "1.0"
  checksum: "wrong-but-skipped"
  sign: "sig"
  createdBy:
    certificate: "stub"
`
	out, err := fn(context.Background(), map[string]interface{}{
		"source_assertion": yamlStr,
	})
	require.NoError(t, err)
	assert.Equal(t, true, out.(map[string]interface{})["asserted"])
}
