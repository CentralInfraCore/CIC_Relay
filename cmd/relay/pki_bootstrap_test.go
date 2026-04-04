package main

import (
	"testing"

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

func TestBootstrapPKI_ProdMode_Scaffold(t *testing.T) {
	assets, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
	require.NoError(t, err, "prod scaffold must not return error")
	assert.False(t, assets.TrustStoreLoaded, "prod scaffold must leave trust store empty")
}

func TestBootstrapPKI_UnknownMode_Error(t *testing.T) {
	_, err := bootstrapPKI("staging", embeddedFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown CIC_TRUST_MODE")
	assert.Contains(t, err.Error(), "staging")
}
