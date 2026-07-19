package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildOCIKeyResolver_Unconfigured(t *testing.T) {
	os.Unsetenv("CIC_OCI_SIGN_MOUNT")
	os.Unsetenv("CIC_OCI_SIGN_KEY")
	assert.Nil(t, buildOCIKeyResolver())
}

func TestBuildOCIKeyResolver_Configured(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]string{"signature": "vault:v1:QUJD"},
		})
	}))
	defer srv.Close()

	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_TOKEN", "test")
	t.Setenv("CIC_OCI_SIGN_MOUNT", "transit")
	t.Setenv("CIC_OCI_SIGN_KEY", "oci-key")

	resolve := buildOCIKeyResolver()
	require.NotNil(t, resolve)

	// unsupported handle → error
	_, err := resolve("kv/other")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported secret handle")

	// oci/ handle → a SignOCI-delegating signer
	sg, err := resolve("oci/prod")
	require.NoError(t, err)
	require.NotNil(t, sg)

	sig, err := sg.Sign(context.Background(), []byte("canonical"))
	require.NoError(t, err)
	assert.Equal(t, "QUJD", sig) // vault:v1: prefix stripped
	assert.Equal(t, "pkcs1v15", gotBody["signature_algorithm"])
	assert.Equal(t, "sha2-256", gotBody["hash_algorithm"])
}
