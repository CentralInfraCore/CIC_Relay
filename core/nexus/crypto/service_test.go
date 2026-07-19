package crypto

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestVault(t *testing.T, handler http.Handler) (*httptest.Server, *VaultCryptoService) {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	svc, err := NewVaultCryptoService("transit", "test-key")
	require.NoError(t, err)
	require.NotNil(t, svc)

	return server, svc
}

func TestNewVaultCryptoService_Errors(t *testing.T) {
	t.Run("missing vault token", func(t *testing.T) {
		t.Setenv("VAULT_ADDR", "http://localhost:8200")
		os.Unsetenv("VAULT_TOKEN")

		_, err := NewVaultCryptoService("transit", "test-key")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "VAULT_TOKEN environment variable is not set")
	})
}

func TestVaultCryptoService_Sign(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPut, r.Method)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]string{"signature": "vault:v1:mocked_signature"},
			})
		})
		_, svc := setupTestVault(t, handler)
		signature, err := svc.Sign(context.Background(), []byte("test-data"))
		require.NoError(t, err)
		assert.Equal(t, "vault:v1:mocked_signature", signature)
	})

	t.Run("vault returns error", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, svc := setupTestVault(t, handler)
		_, err := svc.Sign(context.Background(), []byte("test-data"))
		assert.Error(t, err)
	})
}

func TestVaultCryptoService_SignOCI(t *testing.T) {
	t.Run("success — pkcs1v15/sha2-256, raw base64 (prefix stripped)", func(t *testing.T) {
		var gotBody map[string]interface{}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPut, r.Method)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]string{"signature": "vault:v1:QUJD"},
			})
		})
		_, svc := setupTestVault(t, handler)
		sig, err := svc.SignOCI(context.Background(), []byte("canonical"))
		require.NoError(t, err)
		assert.Equal(t, "QUJD", sig) // "vault:v1:" prefix stripped
		assert.Equal(t, "pkcs1v15", gotBody["signature_algorithm"])
		assert.Equal(t, "sha2-256", gotBody["hash_algorithm"])
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("canonical")), gotBody["input"])
	})

	t.Run("signature without version prefix returned as-is", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]string{"signature": "rawbase64nocolon"},
			})
		})
		_, svc := setupTestVault(t, handler)
		sig, err := svc.SignOCI(context.Background(), []byte("x"))
		require.NoError(t, err)
		assert.Equal(t, "rawbase64nocolon", sig)
	})

	t.Run("vault returns error", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, svc := setupTestVault(t, handler)
		_, err := svc.SignOCI(context.Background(), []byte("x"))
		assert.Error(t, err)
	})

	t.Run("missing signature field", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{}})
		})
		_, svc := setupTestVault(t, handler)
		_, err := svc.SignOCI(context.Background(), []byte("x"))
		assert.Error(t, err)
	})
}

func TestVaultCryptoService_Verify(t *testing.T) {
	t.Run("valid signature", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPut, r.Method)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{"valid": true},
			})
		})
		_, svc := setupTestVault(t, handler)
		ok, err := svc.Verify(context.Background(), []byte("test-data"), "vault:v1:sig")
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("invalid signature", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{"valid": false},
			})
		})
		_, svc := setupTestVault(t, handler)
		ok, err := svc.Verify(context.Background(), []byte("test-data"), "vault:v1:sig")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("vault returns error", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, svc := setupTestVault(t, handler)
		_, err := svc.Verify(context.Background(), []byte("test-data"), "vault:v1:sig")
		assert.Error(t, err)
	})

	t.Run("missing valid field", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{}})
		})
		_, svc := setupTestVault(t, handler)
		_, err := svc.Verify(context.Background(), []byte("test-data"), "vault:v1:sig")
		assert.Error(t, err)
	})
}

func TestVaultCryptoService_Decrypt(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPut, r.Method)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]string{"plaintext": base64.StdEncoding.EncodeToString([]byte("decrypted-data"))},
			})
		})
		_, svc := setupTestVault(t, handler)
		plaintext, err := svc.Decrypt(context.Background(), []byte("vault:v1:encrypted_data"))
		require.NoError(t, err)
		assert.Equal(t, "decrypted-data", string(plaintext))
	})

	t.Run("vault returns error", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, svc := setupTestVault(t, handler)
		_, err := svc.Decrypt(context.Background(), []byte("vault:v1:encrypted_data"))
		assert.Error(t, err)
	})

	t.Run("bad base64 response", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]string{"plaintext": "not-base64"},
			})
		})
		_, svc := setupTestVault(t, handler)
		_, err := svc.Decrypt(context.Background(), []byte("vault:v1:encrypted_data"))
		assert.Error(t, err)
	})
}

func TestVaultCryptoService_HealthCheck(t *testing.T) {
	t.Run("health ok", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		})
		_, svc := setupTestVault(t, handler)
		err := svc.HealthCheck(context.Background())
		assert.NoError(t, err)
	})

	t.Run("health error", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, svc := setupTestVault(t, handler)
		err := svc.HealthCheck(context.Background())
		assert.Error(t, err)
	})
}

func TestVaultCryptoService_Sign_NilData(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{}) // no "data" key → nil data
	})
	_, svc := setupTestVault(t, handler)
	_, err := svc.Sign(context.Background(), []byte("test"))
	assert.Error(t, err)
}

func TestVaultCryptoService_Sign_MissingSignatureField(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]string{"other": "value"},
		})
	})
	_, svc := setupTestVault(t, handler)
	_, err := svc.Sign(context.Background(), []byte("test"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "signature")
}

func TestVaultCryptoService_Decrypt_NilData(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{})
	})
	_, svc := setupTestVault(t, handler)
	_, err := svc.Decrypt(context.Background(), []byte("vault:v1:test"))
	assert.Error(t, err)
}

func TestVaultCryptoService_Decrypt_MissingPlaintextField(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]string{"other": "value"},
		})
	})
	_, svc := setupTestVault(t, handler)
	_, err := svc.Decrypt(context.Background(), []byte("vault:v1:test"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "plaintext")
}
