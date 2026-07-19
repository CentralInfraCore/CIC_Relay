package main

import (
	"crypto/x509"
	"encoding/pem"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTempPEM(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

func rootCAPEM(t *testing.T) []byte {
	t.Helper()
	data, err := fs.ReadFile(getSubFS(t, "pki/trust"), "root-ca.pem")
	require.NoError(t, err)
	return data
}

func TestLoadCompanionCAPool(t *testing.T) {
	pool, err := loadCompanionCAPool(writeTempPEM(t, "ca.pem", rootCAPEM(t)))
	require.NoError(t, err)
	assert.NotNil(t, pool)

	_, err = loadCompanionCAPool(filepath.Join(t.TempDir(), "nope.pem"))
	assert.Error(t, err) // missing file

	_, err = loadCompanionCAPool(writeTempPEM(t, "bad.pem", []byte("not a pem")))
	assert.Error(t, err) // no valid certs
}

func TestLoadProvenanceAnchors(t *testing.T) {
	t.Run("unconfigured → not loaded", func(t *testing.T) {
		os.Unsetenv("CIC_SOURCE_CA_BUNDLE")
		os.Unsetenv("CIC_LICENSE_CA_BUNDLE")
		a := &PKIAssets{}
		require.NoError(t, loadProvenanceAnchors(a, newDiscardLogger()))
		assert.False(t, a.SourceLoaded)
		assert.False(t, a.LicenseLoaded)
	})

	t.Run("configured → loaded", func(t *testing.T) {
		t.Setenv("CIC_SOURCE_CA_BUNDLE", writeTempPEM(t, "source.pem", rootCAPEM(t)))
		t.Setenv("CIC_LICENSE_CA_BUNDLE", writeTempPEM(t, "license.pem", rootCAPEM(t)))
		a := &PKIAssets{}
		require.NoError(t, loadProvenanceAnchors(a, newDiscardLogger()))
		assert.True(t, a.SourceLoaded)
		assert.NotNil(t, a.SourceRoots)
		assert.True(t, a.LicenseLoaded)
	})

	t.Run("configured but unreadable → error", func(t *testing.T) {
		t.Setenv("CIC_SOURCE_CA_BUNDLE", filepath.Join(t.TempDir(), "missing.pem"))
		os.Unsetenv("CIC_LICENSE_CA_BUNDLE")
		assert.Error(t, loadProvenanceAnchors(&PKIAssets{}, newDiscardLogger()))
	})
}

func TestVerifyCICFlowProvenance(t *testing.T) {
	eeCert, err := fs.ReadFile(getSubFS(t, "pki/signers/sources/module"), "ee.pem")
	require.NoError(t, err)
	block, _ := pem.Decode(eeCert)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	// not loaded → error (fail-closed)
	assert.Error(t, verifyCICFlowProvenance(cert, &PKIAssets{}))
	assert.Error(t, verifyCICFlowProvenance(cert, nil))

	// loaded with the anchor the ee chains to → passes
	assert.NoError(t, verifyCICFlowProvenance(cert, loadedPKIWithSource(t)))

	// loaded with a foreign anchor → fails
	assert.Error(t, verifyCICFlowProvenance(cert, &PKIAssets{SourceRoots: x509.NewCertPool(), SourceLoaded: true}))
}
