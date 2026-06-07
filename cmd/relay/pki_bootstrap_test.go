package main

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveTrustMode_Default(t *testing.T) {
	t.Setenv("CIC_TRUST_MODE", "")
	mode := resolveTrustMode(newDiscardLogger())
	assert.Equal(t, TrustModeDev, mode)
}

func TestResolveTrustMode_Dev(t *testing.T) {
	t.Setenv("CIC_TRUST_MODE", "dev")
	mode := resolveTrustMode(newDiscardLogger())
	assert.Equal(t, TrustModeDev, mode)
}

func TestResolveTrustMode_Prod(t *testing.T) {
	t.Setenv("CIC_TRUST_MODE", "prod")
	mode := resolveTrustMode(newDiscardLogger())
	assert.Equal(t, TrustModeProd, mode)
}

func TestBootstrapPKI_DevMode_TrustStoreLoaded(t *testing.T) {
	assets, err := bootstrapPKI(TrustModeDev, embeddedFS, newDiscardLogger())
	require.NoError(t, err)
	assert.True(t, assets.TrustStoreLoaded, "dev mode must load embedded root CA")
	assert.NotNil(t, assets.Roots)
	assert.NotNil(t, assets.Intermediates)
}

func TestBootstrapPKI_ProdMode_NoBundleEnv_Error(t *testing.T) {
	t.Setenv("CIC_PLATFORM_CA_BUNDLE", "")
	_, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CIC_PLATFORM_CA_BUNDLE")
}

func TestBootstrapPKI_ProdMode_WithBundle(t *testing.T) {
	pemData, err := embeddedFS.ReadFile("embedded/pki/rootCA2026.pem")
	require.NoError(t, err)
	bundlePath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(bundlePath, pemData, 0600))

	t.Setenv("CIC_PLATFORM_CA_BUNDLE", bundlePath)
	assets, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
	require.NoError(t, err)
	assert.True(t, assets.TrustStoreLoaded)
	assert.NotNil(t, assets.Roots)
}

func TestBootstrapPKI_ProdMode_InvalidPath_Error(t *testing.T) {
	t.Setenv("CIC_PLATFORM_CA_BUNDLE", "/nonexistent/path/ca.pem")
	_, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pki bootstrap (prod)")
}

func TestBootstrapPKI_UnknownMode_Error(t *testing.T) {
	_, err := bootstrapPKI("staging", embeddedFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown CIC_TRUST_MODE")
	assert.Contains(t, err.Error(), "staging")
}

func TestBootstrapPKI_DevMode_LoadError(t *testing.T) {
	// A file (not directory) at "embedded/pki" → fs.ReadDir returns a non-ErrNotExist error
	// → loadPKIAssets propagates it → bootstrapPKI wraps with "pki bootstrap (dev)"
	badFS := fstest.MapFS{
		"embedded/pki": {Mode: 0644, Data: []byte("not-a-dir")},
	}
	_, err := bootstrapPKI(TrustModeDev, badFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pki bootstrap (dev)")
}
