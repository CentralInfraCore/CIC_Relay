package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/canonicaljson"
	"centralrelay/pkg/merkle"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// failingReadFileFS is a mock file system that fails on reading specific files.
type failingReadFileFS struct {
	fs.FS
	failOnRead map[string]bool
}

// failingFile is a wrapper around fs.File that can be configured to fail on Read.
type failingFile struct {
	fs.File
	failRead bool
}

func (f *failingFile) Read(b []byte) (int, error) {
	if f.failRead {
		return 0, os.ErrPermission
	}
	return f.File.Read(b)
}

func (fsys *failingReadFileFS) Open(name string) (fs.File, error) {
	file, err := fsys.FS.Open(name)
	if err != nil {
		return nil, err
	}
	if fsys.failOnRead[name] {
		return &failingFile{File: file, failRead: true}, nil
	}
	return file, nil
}

// failingReadDirFS is a mock file system that fails on ReadDir.
type failingReadDirFS struct {
	fs.FS
}

func (f *failingReadDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return nil, os.ErrPermission
}

// failingStatFS is a mock file system that fails on Stat for specific files.
type failingStatFS struct {
	fs.FS
	failOnStat map[string]bool
}

// Stat returns an error for specific files.
func (fsys *failingStatFS) Stat(name string) (fs.FileInfo, error) {
	if fsys.failOnStat[name] {
		return nil, os.ErrPermission
	}
	// fs.Stat is not available on the interface, so we need to open the file to stat it.
	f, err := fsys.FS.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

//go:embed testdata
var embeddedTestdata embed.FS

// getSubFS returns an fs.FS rooted at the given subdirectory within the embedded testdata.
func getSubFS(t *testing.T, subdir string) fs.FS {
	sub, err := fs.Sub(embeddedTestdata, "testdata/"+subdir)
	require.NoError(t, err, "failed to get sub FS for %s", subdir)
	return sub
}

func TestLoadPKIAssets(t *testing.T) {
	logger := newDiscardLogger()

	t.Run("successful load", func(t *testing.T) {
		// Create a virtual FS with root and intermediate certs in the same directory
		rootCert, err := fs.ReadFile(getSubFS(t, "pki/trust"), "root-ca.pem")
		require.NoError(t, err)
		intermediateCert, err := fs.ReadFile(getSubFS(t, "pki/trust/companies"), "company-ca.pem")
		require.NoError(t, err)

		mockFS := fstest.MapFS{
			"root.pem":         {Data: rootCert},
			"intermediate.pem": {Data: intermediateCert},
		}

		assets, err := loadPKIAssets(mockFS, ".", logger)
		require.NoError(t, err)
		assert.NotEqual(t, x509.NewCertPool(), assets.Roots, "should load root CAs")
		assert.NotEqual(t, x509.NewCertPool(), assets.Intermediates, "should load intermediate CAs")
		assert.True(t, len(assets.CAFingerprint) > 7 && assets.CAFingerprint[:7] == "sha256:",
			"CAFingerprint must be set after loading root CA")
		// M36: CertChainHash must also be computed when trust store is loaded.
		assert.True(t, len(assets.CertChainHash) > 7 && assets.CertChainHash[:7] == "sha256:",
			"CertChainHash must be set after loading CA certs")
		assert.NotEqual(t, assets.CAFingerprint, assets.CertChainHash,
			"CertChainHash covers all certs; CAFingerprint covers only the first root")
	})

	t.Run("empty path", func(t *testing.T) {
		pkiFS := getSubFS(t, "pki")
		assets, err := loadPKIAssets(pkiFS, "", logger)
		require.NoError(t, err)
		assert.Equal(t, x509.NewCertPool(), assets.Roots)
	})

	t.Run("non-existent path", func(t *testing.T) {
		pkiFS := getSubFS(t, "pki")
		var logBuf bytes.Buffer
		logger.SetOutput(&logBuf)
		defer logger.SetOutput(io.Discard)

		assets, err := loadPKIAssets(pkiFS, "non-existent-dir", logger)
		require.NoError(t, err)
		assert.Equal(t, x509.NewCertPool(), assets.Roots)
		assert.Contains(t, logBuf.String(), "PKI directory not found")
	})

	t.Run("read dir fails", func(t *testing.T) {
		failingFS := &failingReadDirFS{FS: getSubFS(t, "pki")}
		_, err := loadPKIAssets(failingFS, ".", logger)
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrPermission)
	})

	t.Run("read file fails", func(t *testing.T) {
		failingFS := &failingReadFileFS{
			FS:         getSubFS(t, "pki/trust"),
			failOnRead: map[string]bool{"root-ca.pem": true},
		}
		var logBuf bytes.Buffer
		logger.SetOutput(&logBuf)
		defer logger.SetOutput(io.Discard)

		_, err := loadPKIAssets(failingFS, ".", logger)
		require.NoError(t, err)
		assert.Contains(t, logBuf.String(), "Could not read PKI file")
	})

	t.Run("skip directories", func(t *testing.T) {
		pkiFS := getSubFS(t, "pki") // This contains subdirectories
		_, err := loadPKIAssets(pkiFS, ".", logger)
		require.NoError(t, err)
	})

	t.Run("non-pem file", func(t *testing.T) {
		tempDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "not-a-pem.txt"), []byte("hello world"), 0644))
		var logBuf bytes.Buffer
		logger.SetOutput(&logBuf)
		defer logger.SetOutput(io.Discard)

		_, err := loadPKIAssets(os.DirFS(tempDir), ".", logger)
		require.NoError(t, err)
		assert.Contains(t, logBuf.String(), "No PEM data found")
	})

	t.Run("invalid certificate", func(t *testing.T) {
		tempDir := t.TempDir()
		// This is a valid base64 string, but not a valid certificate.
		invalidCertContent := "-----BEGIN CERTIFICATE-----\nYWJjZGVmZw==\n-----END CERTIFICATE-----"
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "invalid.crt"), []byte(invalidCertContent), 0644))
		var logBuf bytes.Buffer
		logger.SetOutput(&logBuf)
		defer logger.SetOutput(io.Discard)

		_, err := loadPKIAssets(os.DirFS(tempDir), ".", logger)
		require.NoError(t, err)
		assert.Contains(t, logBuf.String(), "Failed to parse certificate")
	})

	t.Run("invalid crl", func(t *testing.T) {
		tempDir := t.TempDir()
		// This is a valid base64 string, but not a valid CRL.
		invalidCRLContent := "-----BEGIN X509 CRL-----\nYWJjZGVmZw==\n-----END X509 CRL-----"
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "invalid.crl"), []byte(invalidCRLContent), 0644))
		var logBuf bytes.Buffer
		logger.SetOutput(&logBuf)
		defer logger.SetOutput(io.Discard)

		_, err := loadPKIAssets(os.DirFS(tempDir), ".", logger)
		require.NoError(t, err)
		assert.Contains(t, logBuf.String(), "Failed to parse CRL")
	})

	t.Run("non-ca certificate", func(t *testing.T) {
		// Use a signer cert which is not a CA
		pkiFS := getSubFS(t, "pki/signers/sources/module")
		var logBuf bytes.Buffer
		logger.SetOutput(&logBuf)
		defer logger.SetOutput(io.Discard)

		assets, err := loadPKIAssets(pkiFS, ".", logger)
		require.NoError(t, err)
		assert.Equal(t, x509.NewCertPool(), assets.Roots, "non-CA cert should not be added to roots")
		assert.Equal(t, x509.NewCertPool(), assets.Intermediates, "non-CA cert should not be added to intermediates")
	})
}

func TestLoadSchema(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)

	t.Run("unmarshal error", func(t *testing.T) {
		content := []byte("kind: Schema\nmetadata: \"not a map\"")
		err := loadSchema(context.Background(), svc, content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal schema file")
	})

	t.Run("validation error", func(t *testing.T) {
		// cabinet.NewSchema requires name and version, so this should fail validation
		content := []byte("kind: Schema\nmetadata: {}\nspec: {}")
		err := loadSchema(context.Background(), svc, content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "schema validation")
	})

	t.Run("overwrite existing schema", func(t *testing.T) {
		content := []byte(`
kind: Schema
metadata:
  name: test.schema
  version: "1.0"
spec:
  type: object
`)
		// Load once
		err := loadSchema(context.Background(), svc, content)
		require.NoError(t, err)

		var logBuf bytes.Buffer
		originalOutput := logrus.StandardLogger().Out
		logrus.SetOutput(&logBuf)
		defer logrus.SetOutput(originalOutput)

		// Load again
		err = loadSchema(context.Background(), svc, content)
		require.NoError(t, err)
		assert.Contains(t, logBuf.String(), "Overwriting existing schema")
	})
}

func TestLoadWorkflow(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)

	t.Run("unmarshal error", func(t *testing.T) {
		content := []byte("kind: Workflow\nspec: \"not an object\"")
		err := loadWorkflow(context.Background(), svc, content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal workflow file")
	})

	t.Run("validation error", func(t *testing.T) {
		// cabinet.NewWorkflow requires at least one step
		content := []byte(`
kind: Workflow
metadata:
  name: test.flow
  version: "1.0"
spec:
  steps: []
`)
		err := loadWorkflow(context.Background(), svc, content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "workflow validation")
	})

	t.Run("overwrite existing workflow", func(t *testing.T) {
		content := []byte(`
kind: Workflow
metadata:
  name: test.flow
  version: "1.0"
spec:
  steps: ["step1"]
`)
		// Load once
		err := loadWorkflow(context.Background(), svc, content)
		require.NoError(t, err)

		var logBuf bytes.Buffer
		originalOutput := logrus.StandardLogger().Out
		logrus.SetOutput(&logBuf)
		defer logrus.SetOutput(originalOutput)

		// Load again
		err = loadWorkflow(context.Background(), svc, content)
		require.NoError(t, err)
		assert.Contains(t, logBuf.String(), "Overwriting existing workflow")
	})
}

func TestLoadEmbeddedComponents(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)
	maxWasmSize := int64(16 * 1024 * 1024)
	logger := newDiscardLogger()

	schemasFS := getSubFS(t, "schemas")
	workflowsFS := getSubFS(t, "workflows")
	pkiFS := getSubFS(t, "pki")

	err = loadEmbeddedComponents(context.Background(), svc, schemasFS, workflowsFS, pkiFS, ".", maxWasmSize, logger)
	require.NoError(t, err, "loadEmbeddedComponents should not return an error")

	_, err = svc.GetSchema("test.schema@1.0")
	assert.NoError(t, err, "expected schema 'test.schema@1.0' to be loaded")

	_, err = svc.GetWorkflow("test.flow@1.0")
	assert.NoError(t, err, "expected workflow 'test.flow@1.0' to be loaded")
}

func TestLoadEmbeddedComponents_Error(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)
	maxWasmSize := int64(16 * 1024 * 1024)
	logger := newDiscardLogger()
	pkiFS := getSubFS(t, "pki")

	t.Run("invalid schema", func(t *testing.T) {
		invalidSchemaFS := getSubFS(t, "invalid")
		validWorkflowsFS := getSubFS(t, "workflows")
		err := loadEmbeddedComponents(context.Background(), svc, invalidSchemaFS, validWorkflowsFS, pkiFS, ".", maxWasmSize, logger)
		assert.Error(t, err, "expected an error when loading invalid embedded schema components")
		assert.Contains(t, err.Error(), "failed to load embedded schemas")
	})

	t.Run("pki asset loading error", func(t *testing.T) {
		failingPKIFS := &failingReadDirFS{FS: getSubFS(t, "pki")}
		schemasFS := getSubFS(t, "schemas")
		workflowsFS := getSubFS(t, "workflows")
		err := loadEmbeddedComponents(context.Background(), svc, schemasFS, workflowsFS, failingPKIFS, ".", maxWasmSize, logger)
		assert.Error(t, err, "expected an error when loading pki assets fails")
		assert.Contains(t, err.Error(), "failed to load embedded PKI assets")
		assert.ErrorIs(t, err, os.ErrPermission)
	})

	t.Run("invalid workflow", func(t *testing.T) {
		validSchemaFS := getSubFS(t, "schemas")
		invalidWorkflowsFS := getSubFS(t, "invalid")
		err := loadEmbeddedComponents(context.Background(), svc, validSchemaFS, invalidWorkflowsFS, pkiFS, ".", maxWasmSize, logger)
		assert.Error(t, err, "expected an error when loading invalid embedded workflow components")
		assert.Contains(t, err.Error(), "failed to load embedded workflows")
	})
}

func TestLoadComponentsFromFS(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)
	maxWasmSize := int64(16 * 1024 * 1024)
	logger := newDiscardLogger()
	pkiFS := getSubFS(t, "pki")
	pkiAssets, err := loadPKIAssets(pkiFS, ".", logger)
	require.NoError(t, err)

	t.Run("unmarshal component header error", func(t *testing.T) {
		// Create a temporary FS with a malformed YAML file
		tempDir := t.TempDir()
		malformedYamlPath := "malformed.yaml"
		malformedYamlContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata: "this should be a map"
`)
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, malformedYamlPath), malformedYamlContent, 0644))

		tempFS := os.DirFS(tempDir)

		var logBuf bytes.Buffer
		logger.SetOutput(&logBuf)
		defer logger.SetOutput(io.Discard)

		err := loadComponentsFromFS(context.Background(), svc, tempFS, ".", false, pkiAssets, maxWasmSize, logger)
		assert.NoError(t, err, "loadComponentsFromFS should not return a fatal error for unmarshal errors")
		assert.Contains(t, logBuf.String(), "unmarshal module file", "log should contain unmarshal error")
	})
}

func TestLoadModule(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)
	maxWasmSize := int64(16 * 1024 * 1024)
	logger := newDiscardLogger()
	modulesFS := getSubFS(t, "modules")
	pkiFS := getSubFS(t, "pki")
	pkiAssets, err := loadPKIAssets(pkiFS, ".", logger)
	require.NoError(t, err)

	expectedWasmCode, err := fs.ReadFile(modulesFS, "test_module.wasm")
	require.NoError(t, err)

	t.Run("successful module load", func(t *testing.T) {
		yamlContent, err := fs.ReadFile(modulesFS, "test_module.yaml")
		require.NoError(t, err)

		err = loadModule(context.Background(), svc, modulesFS, "test_module.yaml", yamlContent, pkiAssets, maxWasmSize, logger)
		assert.NoError(t, err, "loadModule should not return an error for a valid module")

		loadedModule, err := svc.GetModule("test.module@1.0")
		assert.NoError(t, err, "Module should be loaded into the cabinet")
		assert.Equal(t, expectedWasmCode, loadedModule.WasmCode, "Loaded module should contain the correct WASM code")
		assert.True(t, loadedModule.Meta.Valid, "Loaded module should be valid")
	})

	t.Run("missing wasm digest", func(t *testing.T) {
		missingDigestYaml := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: missing.digest
  version: "1.0"
wasmSize: 123
`)
		err = loadModule(context.Background(), svc, modulesFS, "missing_digest.yaml", missingDigestYaml, pkiAssets, maxWasmSize, logger)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "wasmDigest is a required field")
	})

	t.Run("missing wasm size", func(t *testing.T) {
		missingSizeYaml := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: missing.size
  version: "1.0"
wasmDigest: "some-digest"
`)
		err = loadModule(context.Background(), svc, modulesFS, "missing_size.yaml", missingSizeYaml, pkiAssets, maxWasmSize, logger)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "wasmSize is a required field")
	})

	t.Run("missing wasm file", func(t *testing.T) {
		tempFS := os.DirFS(t.TempDir())
		missingWasmYamlPath := "missing_wasm.yaml"
		missingWasmYamlContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: missing.wasm
  version: "1.0"
wasmDigest: "dummy-digest"
wasmSize: 123
`)
		require.NoError(t, os.WriteFile(filepath.Join(t.TempDir(), missingWasmYamlPath), missingWasmYamlContent, 0644))

		err = loadModule(context.Background(), svc, tempFS, missingWasmYamlPath, missingWasmYamlContent, pkiAssets, maxWasmSize, logger)
		assert.Error(t, err, "loadModule should return an error if WASM file is missing")
		assert.Contains(t, err.Error(), "stat WASM file")
		assert.Contains(t, err.Error(), "no such file or directory")
	})

	t.Run("invalid yaml content", func(t *testing.T) {
		invalidYamlContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata: "this should be a map"
`)
		err = loadModule(context.Background(), svc, modulesFS, "invalid.yaml", invalidYamlContent, pkiAssets, maxWasmSize, logger)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal module file")
	})

	t.Run("wasm file too large", func(t *testing.T) {
		tempDir := t.TempDir()
		yamlContent := []byte(fmt.Sprintf(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: large.module
  version: "1.0"
wasmDigest: "dummy-digest"
wasmSize: %d
`, maxWasmSize+1))
		yamlPath := "large_module.yaml"
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, yamlPath), yamlContent, 0644))

		largeWasmContent := make([]byte, maxWasmSize+1)
		wasmPath := "large_module.wasm"
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, wasmPath), largeWasmContent, 0644))

		tempFS := os.DirFS(tempDir)
		err = loadModule(context.Background(), svc, tempFS, yamlPath, yamlContent, pkiAssets, maxWasmSize, logger)

		assert.Error(t, err, "loadModule should return an error for a wasm file that is too large")
		assert.Contains(t, err.Error(), "exceeds maximum allowed size")
	})

	t.Run("read wasm file error", func(t *testing.T) {
		failingFS := &failingReadFileFS{
			FS:         modulesFS,
			failOnRead: map[string]bool{"test_module.wasm": true},
		}

		yamlContent, err := fs.ReadFile(modulesFS, "test_module.yaml")
		require.NoError(t, err)

		err = loadModule(context.Background(), svc, failingFS, "test_module.yaml", yamlContent, pkiAssets, maxWasmSize, logger)

		assert.Error(t, err, "loadModule should return an error if wasm file cannot be read")
		assert.Contains(t, err.Error(), "read WASM file")
		assert.ErrorIs(t, err, os.ErrPermission)
	})

	t.Run("wasm digest mismatch", func(t *testing.T) {
		tempDir := t.TempDir()
		wrongDigestYamlPath := "wrong_digest.yaml"
		wrongDigestYamlContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: wrong.digest
  version: "1.0"
wasmDigest: "badhash"
wasmSize: 8
`)
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, wrongDigestYamlPath), wrongDigestYamlContent, 0644))
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "wrong_digest.wasm"), []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x02, 0x03, 0x04}, 0644))

		tempFS := os.DirFS(tempDir)
		err = loadModule(context.Background(), svc, tempFS, wrongDigestYamlPath, wrongDigestYamlContent, pkiAssets, maxWasmSize, logger)

		assert.Error(t, err, "loadModule should return an error for WASM digest mismatch")
		assert.Contains(t, err.Error(), "WASM digest mismatch")
	})

	t.Run("wasm size mismatch", func(t *testing.T) {
		tempDir := t.TempDir()
		wrongSizeYamlPath := "wrong_size.yaml"
		wrongSizeYamlContent := []byte(fmt.Sprintf(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: wrong.size
  version: "1.0"
wasmDigest: "%s"
wasmSize: 999
`, "292ed65ec666d77aa17f84777f83e6e5aca98d61ba75461ca76fe8313a44382c"))
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, wrongSizeYamlPath), wrongSizeYamlContent, 0644))
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "wrong_size.wasm"), []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x02, 0x03, 0x04}, 0644))

		tempFS := os.DirFS(tempDir)
		err = loadModule(context.Background(), svc, tempFS, wrongSizeYamlPath, wrongSizeYamlContent, pkiAssets, maxWasmSize, logger)

		assert.Error(t, err, "loadModule should return an error for WASM size mismatch")
		assert.Contains(t, err.Error(), "WASM size mismatch")
	})

	t.Run("module validation error", func(t *testing.T) {
		tempDir := t.TempDir()
		invalidMetaYamlPath := "invalid_meta.yaml"
		wasmContent := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
		hasher := sha256.New()
		hasher.Write(wasmContent)
		correctDigest := hex.EncodeToString(hasher.Sum(nil))

		invalidMetaYamlContent := []byte(fmt.Sprintf(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  version: "1.0" # Missing name
wasmDigest: "%s"
wasmSize: 8
`, correctDigest))
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, invalidMetaYamlPath), invalidMetaYamlContent, 0644))
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "invalid_meta.wasm"), wasmContent, 0644))

		tempFS := os.DirFS(tempDir)
		err = loadModule(context.Background(), svc, tempFS, invalidMetaYamlPath, invalidMetaYamlContent, pkiAssets, maxWasmSize, logger)

		assert.Error(t, err, "loadModule should return an error for module validation failure")
		assert.Contains(t, err.Error(), "module validation")
		assert.Contains(t, err.Error(), "name and version are required")
	})

	t.Run("stat wasm error", func(t *testing.T) {
		failingFS := &failingStatFS{
			FS:         modulesFS,
			failOnStat: map[string]bool{"test_module.wasm": true},
		}
		yamlContent, err := fs.ReadFile(modulesFS, "test_module.yaml")
		require.NoError(t, err)
		err = loadModule(context.Background(), svc, failingFS, "test_module.yaml", yamlContent, pkiAssets, maxWasmSize, logger)
		assert.Error(t, err)
		assert.ErrorIs(t, err, os.ErrPermission)
		assert.Contains(t, err.Error(), "stat WASM file")
	})

	t.Run("read signature error", func(t *testing.T) {
		failingFS := &failingReadFileFS{
			FS:         modulesFS,
			failOnRead: map[string]bool{"signed_module.pem": true},
		}
		yamlContent, err := fs.ReadFile(modulesFS, "signed_module.yaml")
		require.NoError(t, err)
		var logBuf bytes.Buffer
		logger := logrus.New() // Directly initialize a new logger
		logger.SetOutput(&logBuf)
		logger.SetLevel(logrus.ErrorLevel) // Ensure error level is enabled
		err = loadModule(context.Background(), svc, failingFS, "signed_module.yaml", yamlContent, pkiAssets, maxWasmSize, logger)
		assert.NoError(t, err)
		assert.Contains(t, logBuf.String(), "Failed to read signature file")
	})

	t.Run("put module error", func(t *testing.T) {
		mockSvc := new(mockCabinetService)
		yamlContent, err := fs.ReadFile(modulesFS, "test_module.yaml")
		require.NoError(t, err)
		expectedErr := fmt.Errorf("storage is full")
		mockSvc.On("PutModule", mock.Anything).Return(expectedErr)
		mockSvc.On("GetModule", mock.Anything).Return(nil, cabinet.ErrNotFound)
		err = loadModule(context.Background(), mockSvc, modulesFS, "test_module.yaml", yamlContent, pkiAssets, maxWasmSize, logger)
		assert.Error(t, err)
		assert.ErrorIs(t, err, expectedErr)
		mockSvc.AssertExpectations(t)
	})
}

func TestLoadComponentsFromFS_WalkDirError(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)
	maxWasmSize := int64(16 * 1024 * 1024) // Default max WASM size for tests

	// Create a logrus logger that discards output for tests
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	// Capture logrus logger output
	var logBuf bytes.Buffer
	originalLoggerOutput := logger.Out
	logger.SetOutput(&logBuf)
	defer func() {
		logger.SetOutput(originalLoggerOutput)
	}()

	pkiFS := getSubFS(t, "pki")
	pkiAssets, err := loadPKIAssets(pkiFS, ".", logger)
	require.NoError(t, err)

	failingFS := &failingReadDirFS{FS: getSubFS(t, "modules")}

	err = loadComponentsFromFS(context.Background(), svc, failingFS, ".", false, pkiAssets, maxWasmSize, logger)

	assert.Error(t, err, "loadComponentsFromFS should propagate errors from fs.WalkDir")
	assert.ErrorIs(t, err, os.ErrPermission, "the specific error from the mock fs should be propagated")
}

func TestLoadInstalledComponents_Success(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)
	maxWasmSize := int64(16 * 1024 * 1024)
	logger := logrus.New()
	var logBuf bytes.Buffer
	logger.SetOutput(&logBuf)
	logger.SetLevel(logrus.DebugLevel) // Ensure all levels are logged

	// Use absolute paths to avoid issues with test working directory.
	componentsDir, err := filepath.Abs("testdata/minimal")
	require.NoError(t, err)

	// No PKI verification needed for this test — use empty trust store.
	err = loadInstalledComponents(context.Background(), svc, componentsDir, emptyPKIAssets(), maxWasmSize, logger)
	logOutput := logBuf.String() // Capture log output
	require.NoError(t, err, "loadInstalledComponents failed. Logs:\n%s", logOutput)

	// Check if components from the 'minimal' set were loaded.
	_, err = svc.GetSchema("user.schema@1.0")
	require.NoError(t, err, "expected schema 'user.schema@1.0' to be loaded. Logs:\n%s", logOutput)

	_, err = svc.GetWorkflow("order.workflow@1.0")
	require.NoError(t, err, "expected workflow 'order.workflow@1.0' to be loaded. Logs:\n%s", logOutput)

	// Check for the final success log message.
	assert.Contains(t, logOutput, "Successfully loaded installed components.")
}

func TestLoadInstalledComponents_PathErrors(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)
	maxWasmSize := int64(16 * 1024 * 1024) // Default max WASM size for tests

	t.Run("non-existent path", func(t *testing.T) {
		logger := logrus.New()
		var logBuf bytes.Buffer
		logger.SetOutput(&logBuf)

		err := loadInstalledComponents(context.Background(), svc, "/path/to/non/existent/dir", emptyPKIAssets(), maxWasmSize, logger)
		require.NoError(t, err)
		assert.Contains(t, logBuf.String(), "Installed components directory not found, skipping.")
	})

	t.Run("path is a file, not a directory", func(t *testing.T) {
		logger := newDiscardLogger()
		tempFile, err := os.CreateTemp(t.TempDir(), "not_a_dir")
		require.NoError(t, err)
		tempFile.Close()

		loadErr := loadInstalledComponents(context.Background(), svc, tempFile.Name(), emptyPKIAssets(), maxWasmSize, logger)
		require.Error(t, loadErr)
		assert.Contains(t, loadErr.Error(), "path is a file, not a directory")
	})
}

// Helper function to create a logrus logger that discards output
func newDiscardLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return logger
}

// emptyPKIAssets returns a PKIAssets with empty pools (TrustStoreLoaded=false).
// Use this in tests that focus on component loading, not PKI verification.
func emptyPKIAssets() *PKIAssets {
	return &PKIAssets{
		Roots:         x509.NewCertPool(),
		Intermediates: x509.NewCertPool(),
		CRLs:          []*x509.RevocationList{},
	}
}

// TestRegisterNativeComponents verifies that the native components are correctly registered in the cabinet.
func TestRegisterNativeComponents(t *testing.T) {
	mockCabinet := new(mockCabinetService)

	// Collect all registered modules in order.
	var registeredModules []*cabinet.ModuleDescriptor
	mockCabinet.On("PutModule", mock.Anything).Run(func(args mock.Arguments) {
		registeredModules = append(registeredModules, args.Get(0).(*cabinet.ModuleDescriptor))
	}).Return(nil).Times(10) // core.test + ci.build + cic.source.assert + cic.schema.build + cic.artifact.sign + cert.selfsigned + 4 pipeline steps

	var registeredWorkflows []*cabinet.Workflow
	mockCabinet.On("PutWorkflow", mock.Anything).Run(func(args mock.Arguments) {
		registeredWorkflows = append(registeredWorkflows, args.Get(0).(*cabinet.Workflow))
	}).Return(nil).Times(3) // cic.schema.compile + cert.selfsigned + cic.schemas.pipeline

	err := registerNativeComponents(context.Background(), mockCabinet, &PKIAssets{}, merkle.BuildContext{}, nil, &config{})
	require.NoError(t, err, "registerNativeComponents should not return an error")
	mockCabinet.AssertExpectations(t)

	require.Len(t, registeredModules, 10, "expected ten native modules to be registered")
	require.Len(t, registeredWorkflows, 3, "expected three workflows to be registered")
	assert.Equal(t, "cic.schema.compile@1.0", registeredWorkflows[0].ID())
	assert.Equal(t, "cert.selfsigned@1.0", registeredWorkflows[1].ID())
	assert.Equal(t, "cic.schemas.pipeline@1.0", registeredWorkflows[2].ID())

	// First: core.test
	coreTest := registeredModules[0]
	assert.Equal(t, "core.test@1.0", coreTest.ID())
	require.NotNil(t, coreTest.NativeImpl)
	_, ok := coreTest.NativeImpl["echo"]
	assert.True(t, ok, "core.test should expose 'echo' function")

	// Second: ci.build
	ciBuild := registeredModules[1]
	assert.Equal(t, "ci.build@1.0", ciBuild.ID())
	require.NotNil(t, ciBuild.NativeImpl)
	_, ok = ciBuild.NativeImpl["build"]
	assert.True(t, ok, "ci.build should expose 'build' function")

	// Third: cic.source.assert
	sourceAssert := registeredModules[2]
	assert.Equal(t, "cic.source.assert@1.0", sourceAssert.ID())
	require.NotNil(t, sourceAssert.NativeImpl)
	_, ok = sourceAssert.NativeImpl["assert"]
	assert.True(t, ok, "cic.source.assert should expose 'assert' function")

	// Fourth: cic.schema.build
	schemaBuild := registeredModules[3]
	assert.Equal(t, "cic.schema.build@1.0", schemaBuild.ID())
	require.NotNil(t, schemaBuild.NativeImpl)
	_, ok = schemaBuild.NativeImpl["build"]
	assert.True(t, ok, "cic.schema.build should expose 'build' function")

	// Fifth: cic.artifact.sign
	artifactSign := registeredModules[4]
	assert.Equal(t, "cic.artifact.sign@1.0", artifactSign.ID())
	require.NotNil(t, artifactSign.NativeImpl)
	_, ok = artifactSign.NativeImpl["sign"]
	assert.True(t, ok, "cic.artifact.sign should expose 'sign' function")

	// Sixth: cert.selfsigned
	certSelfSigned := registeredModules[5]
	assert.Equal(t, "cert.selfsigned@1.0", certSelfSigned.ID())
	require.NotNil(t, certSelfSigned.NativeImpl)
	_, ok = certSelfSigned.NativeImpl["generate"]
	assert.True(t, ok, "cert.selfsigned should expose 'generate' function")

	// Pipeline steps 7–10: start, test, validate, release
	pipelineSteps := []struct {
		idx  int
		id   string
		fn   string
	}{
		{6, "cic.pipeline.start@1.0", "start"},
		{7, "cic.pipeline.test@1.0", "test"},
		{8, "cic.pipeline.validate@1.0", "validate"},
		{9, "cic.pipeline.release@1.0", "release"},
	}
	for _, ps := range pipelineSteps {
		mod := registeredModules[ps.idx]
		assert.Equal(t, ps.id, mod.ID())
		require.NotNil(t, mod.NativeImpl)
		_, ok = mod.NativeImpl[ps.fn]
		assert.True(t, ok, "%s should expose %q function", ps.id, ps.fn)
	}
}

func TestRegisterNativeComponents_Errors(t *testing.T) {
	t.Run("NewModuleDescriptor fails", func(t *testing.T) {
		// Mock the canonicaljson.ToJSON function to return an error
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			return nil, errors.New("json marshal error")
		})
		t.Cleanup(func() {
			canonicaljson.SetToJSONMock(nil) // Restore original function
		})

		mockCabinet := new(mockCabinetService)
		err := registerNativeComponents(context.Background(), mockCabinet, &PKIAssets{}, merkle.BuildContext{}, nil, &config{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create core.test module descriptor")
	})

	t.Run("PutModule fails", func(t *testing.T) {
		mockCabinet := new(mockCabinetService)
		expectedErr := errors.New("db is full")

		// Expect PutModule to be called and return an error
		mockCabinet.On("PutModule", mock.Anything).Return(expectedErr).Once()

		err := registerNativeComponents(context.Background(), mockCabinet, &PKIAssets{}, merkle.BuildContext{}, nil, &config{})

		require.Error(t, err)
		assert.ErrorIs(t, err, expectedErr)
		assert.Contains(t, err.Error(), "failed to register core.test module")
		mockCabinet.AssertExpectations(t)
	})
}

// buildLoadedPKIAssets creates PKIAssets with TrustStoreLoaded=true using the
// test root-ca and company-ca from testdata/pki/trust.
func buildLoadedPKIAssets(t *testing.T) *PKIAssets {
	t.Helper()
	logger := newDiscardLogger()
	rootPEM, err := fs.ReadFile(getSubFS(t, "pki/trust"), "root-ca.pem")
	require.NoError(t, err)
	interPEM, err := fs.ReadFile(getSubFS(t, "pki/trust/companies"), "company-ca.pem")
	require.NoError(t, err)
	mockFS := fstest.MapFS{
		"root.pem":  {Data: rootPEM},
		"inter.pem": {Data: interPEM},
	}
	assets, err := loadPKIAssets(mockFS, ".", logger)
	require.NoError(t, err)
	require.True(t, assets.TrustStoreLoaded, "test PKI trust store must be loaded")
	return assets
}

// buildModuleFS builds a fstest.MapFS for a single module with an optional PEM cert.
func buildModuleFS(t *testing.T, wasmCode []byte, pemData []byte) (fstest.MapFS, string) {
	t.Helper()
	hasher := sha256.New()
	hasher.Write(wasmCode)
	digest := hex.EncodeToString(hasher.Sum(nil))
	yamlContent := fmt.Sprintf(`apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: pki.test
  version: "1.0"
wasmDigest: %q
wasmSize: %d
`, digest, len(wasmCode))

	m := fstest.MapFS{
		"pki_test.yaml": {Data: []byte(yamlContent)},
		"pki_test.wasm": {Data: wasmCode},
	}
	if pemData != nil {
		m["pki_test.pem"] = &fstest.MapFile{Data: pemData}
	}
	return m, "pki_test.yaml"
}

func TestLoadModule_PKIVerification_Valid(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)

	eeCert, err := fs.ReadFile(getSubFS(t, "pki/signers/sources/module"), "ee.pem")
	require.NoError(t, err)

	wasmCode := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	moduleFS, yamlPath := buildModuleFS(t, wasmCode, eeCert)
	yamlContent := moduleFS[yamlPath].Data
	pkiAssets := buildLoadedPKIAssets(t)

	var logBuf bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logBuf)

	err = loadModule(context.Background(), svc, moduleFS, yamlPath, yamlContent, pkiAssets, int64(16*1024*1024), logger)
	require.NoError(t, err, "valid cert should pass PKI verification")
	assert.Contains(t, logBuf.String(), "verified", "should log verified outcome")
}

func TestLoadModule_PKIVerification_WrongEKU(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)

	wrongEKUCert, err := fs.ReadFile(getSubFS(t, "pki/signers/negatives/client-only"), "ee.pem")
	require.NoError(t, err)

	wasmCode := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	moduleFS, yamlPath := buildModuleFS(t, wasmCode, wrongEKUCert)
	yamlContent := moduleFS[yamlPath].Data
	pkiAssets := buildLoadedPKIAssets(t)
	logger := newDiscardLogger()

	err = loadModule(context.Background(), svc, moduleFS, yamlPath, yamlContent, pkiAssets, int64(16*1024*1024), logger)
	require.Error(t, err, "wrong EKU cert should be rejected")
	assert.Contains(t, err.Error(), "PKI verification failed")
}

// generateForeignLeafPEM creates a leaf cert signed by a freshly-generated root CA
// that is NOT registered in any PKIAssets trust store — simulating an untrusted chain.
func generateForeignLeafPEM(t *testing.T) []byte {
	t.Helper()

	// Generate foreign root CA key + cert.
	foreignKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	foreignRootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Foreign Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	foreignRootDER, err := x509.CreateCertificate(rand.Reader, foreignRootTmpl, foreignRootTmpl, &foreignKey.PublicKey, foreignKey)
	require.NoError(t, err)
	foreignRoot, err := x509.ParseCertificate(foreignRootDER)
	require.NoError(t, err)

	// Generate leaf key + cert signed by foreign root with CodeSigning EKU.
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	leafTmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(2),
		Subject:         pkix.Name{CommonName: "Foreign Signer"},
		NotBefore:       time.Now().Add(-time.Hour),
		NotAfter:        time.Now().Add(24 * time.Hour),
		ExtKeyUsage:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		KeyUsage:        x509.KeyUsageDigitalSignature,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, foreignRoot, &leafKey.PublicKey, foreignKey)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
}

// TestLoadModule_PKIVerification_WrongChain verifies that a cert signed by an unknown
// root CA is rejected even if the EKU is correct.
func TestLoadModule_PKIVerification_WrongChain(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)

	foreignLeafPEM := generateForeignLeafPEM(t)
	wasmCode := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	moduleFS, yamlPath := buildModuleFS(t, wasmCode, foreignLeafPEM)
	yamlContent := moduleFS[yamlPath].Data

	// Trust store loaded with the project's own root CA — foreign root is NOT in it.
	pkiAssets := buildLoadedPKIAssets(t)
	logger := newDiscardLogger()

	err = loadModule(context.Background(), svc, moduleFS, yamlPath, yamlContent, pkiAssets, int64(16*1024*1024), logger)
	require.Error(t, err, "cert signed by foreign root should be rejected")
	assert.Contains(t, err.Error(), "PKI verification failed")
}

func TestLoadModule_PKIVerification_TrustStoreNotLoaded_Skips(t *testing.T) {
	config := cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(), cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(), config)
	require.NoError(t, err)

	eeCert, err := fs.ReadFile(getSubFS(t, "pki/signers/sources/module"), "ee.pem")
	require.NoError(t, err)

	wasmCode := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	moduleFS, yamlPath := buildModuleFS(t, wasmCode, eeCert)
	yamlContent := moduleFS[yamlPath].Data

	emptyPKI := &PKIAssets{
		Roots:         x509.NewCertPool(),
		Intermediates: x509.NewCertPool(),
	}

	var logBuf bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logBuf)

	err = loadModule(context.Background(), svc, moduleFS, yamlPath, yamlContent, emptyPKI, int64(16*1024*1024), logger)
	require.NoError(t, err, "should load without error when trust store is not loaded")
	assert.Contains(t, logBuf.String(), "skipped", "should log skipped outcome")
}
