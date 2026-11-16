package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"centralrelay/core/cabinet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sharedTrustModuleID = "pki.test@1.0"

// outcome is what a load path did with a module: whether it is registered, and whether the
// cic-flow capability binding hook was invoked for it.
type outcome struct {
	registered bool
	bound      []string
}

// loadVia runs one module through the embedded or the installed loader against the same
// trust context, and reports the outcome. Both loaders are fed the identical module files.
func loadVia(t *testing.T, path string, files fstest.MapFS, assets *PKIAssets) outcome {
	t.Helper()
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(),
		cabinet.NewMemWorkflowStore(), cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1})
	require.NoError(t, err)

	var bound []string
	prev := registerModuleCapability
	registerModuleCapability = func(moduleID string, _ []byte, _ string) error {
		bound = append(bound, moduleID)
		return nil
	}
	t.Cleanup(func() { registerModuleCapability = prev })

	maxWasm := int64(16 * 1024 * 1024)
	switch path {
	case "embedded":
		// Errors are the embedded loader's fail-fast; what matters for parity is the outcome.
		_ = loadEmbeddedComponents(context.Background(), svc, fstest.MapFS{}, files, assets, maxWasm, newDiscardLogger())
	case "installed":
		dir := t.TempDir()
		for name, f := range files {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), f.Data, 0o600))
		}
		_ = loadInstalledComponents(context.Background(), svc, dir, assets, maxWasm, newDiscardLogger())
	default:
		t.Fatalf("unknown path %q", path)
	}
	_, getErr := svc.GetModule(sharedTrustModuleID)
	return outcome{registered: getErr == nil, bound: bound}
}

// sharedAssets is the test trust store with the cic-flow provenance anchor set to the same
// roots, so a verified module also reaches the capability binding.
func sharedAssets(t *testing.T) *PKIAssets {
	a := buildLoadedPKIAssets(t)
	a.SourceRoots = a.Roots
	a.SourceLoaded = true
	return a
}

// Acceptance 1: the same module and certificate, loaded from the embedded and from the
// installed path, gives the same accept/reject and the same capability result.
func TestEmbeddedAndInstalled_SameVerdictAndCapability(t *testing.T) {
	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	validCert, err := fs.ReadFile(getSubFS(t, "pki/signers/sources/module"), "ee.pem")
	require.NoError(t, err)
	wrongEKU, err := fs.ReadFile(getSubFS(t, "pki/signers/negatives/client-only"), "ee.pem")
	require.NoError(t, err)

	cases := []struct {
		name         string
		cert         []byte
		wantAccepted bool
	}{
		{"valid signer", validCert, true},
		{"wrong EKU", wrongEKU, false},
		{"untrusted chain", generateForeignLeafPEM(t), false},
		{"garbage signature file", []byte("not a pem"), false}, // a bad signature is refused on both paths (#155)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files, _ := buildModuleFS(t, wasm, c.cert)
			emb := loadVia(t, "embedded", files, sharedAssets(t))
			ins := loadVia(t, "installed", files, sharedAssets(t))

			assert.Equal(t, ins, emb, "the embedded and the installed path must reach the same verdict and capability result")
			assert.Equal(t, c.wantAccepted, emb.registered, "embedded verdict")
			if c.name == "valid signer" {
				assert.Equal(t, []string{sharedTrustModuleID}, emb.bound, "a verified module gets its capability on the embedded path too")
			} else {
				assert.Empty(t, emb.bound, "no capability without verified provenance")
			}
		})
	}
}

// trustCtxCA builds a root CA and a code-signing leaf signed by it; returns both as PEM.
func trustCtxCA(t *testing.T, cn string) (rootPEM, leafPEM []byte) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	rootTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	require.NoError(t, err)
	root, err := x509.ParseCertificate(rootDER)
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: cn + " module signer"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, root, &leafKey.PublicKey, rootKey)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
}

// Acceptance 2: in prod the trust context is the operator's CIC_PLATFORM_CA_BUNDLE. A module
// signed only by a CA that is NOT in that bundle (here: the test PKI, standing in for the CAs
// embedded in the binary) is rejected on both paths; one signed by the operator's CA is
// accepted on both. Being embedded does not widen trust.
func TestProd_ModuleSignedOutsideOperatorBundle_RejectedOnBothPaths(t *testing.T) {
	operatorRoot, operatorLeaf := trustCtxCA(t, "Operator Root")
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	require.NoError(t, os.WriteFile(bundle, operatorRoot, 0o600))
	t.Setenv("CIC_PLATFORM_CA_BUNDLE", bundle)
	t.Setenv("CIC_CRL_REQUIRED_ISSUERS", "none") // the CRL issuer policy is mandatory in prod; not what is tested here

	newProdAssets := func() *PKIAssets {
		a, err := bootstrapPKI(TrustModeProd, embeddedFS, newDiscardLogger())
		require.NoError(t, err)
		a.SourceRoots = a.Roots
		a.SourceLoaded = true
		return a
	}

	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	embeddedCAsSigner, err := fs.ReadFile(getSubFS(t, "pki/signers/sources/module"), "ee.pem")
	require.NoError(t, err)

	t.Run("signed by a CA outside the operator bundle", func(t *testing.T) {
		files, _ := buildModuleFS(t, wasm, embeddedCAsSigner)
		for _, path := range []string{"embedded", "installed"} {
			got := loadVia(t, path, files, newProdAssets())
			assert.False(t, got.registered, "%s path: a module signed outside the operator bundle must be rejected", path)
			assert.Empty(t, got.bound, "%s path: and must not receive a capability", path)
		}
	})
	t.Run("signed by the operator's CA", func(t *testing.T) {
		files, _ := buildModuleFS(t, wasm, operatorLeaf)
		emb := loadVia(t, "embedded", files, newProdAssets())
		ins := loadVia(t, "installed", files, newProdAssets())
		assert.True(t, emb.registered, "embedded path")
		assert.Equal(t, ins, emb)
		assert.Equal(t, []string{sharedTrustModuleID}, emb.bound)
	})
}
