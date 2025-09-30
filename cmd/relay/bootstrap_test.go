package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"centralrelay/core/cabinet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/sirupsen/logrus"
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

func TestLoadEmbeddedComponents(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	maxWasmSize := int64(16 * 1024 * 1024)
	logger := newDiscardLogger()

	schemasFS := getSubFS(t, "schemas")
	workflowsFS := getSubFS(t, "workflows")
	pkiFS := getSubFS(t, "pki")

	err := loadEmbeddedComponents(context.Background(), svc, schemasFS, workflowsFS, pkiFS, ".", maxWasmSize, logger)
	require.NoError(t, err, "loadEmbeddedComponents should not return an error")

	_, err = svc.GetSchema("test.schema@1.0")
	assert.NoError(t, err, "expected schema 'test.schema@1.0' to be loaded")

	_, err = svc.GetWorkflow("test.flow@1.0")
	assert.NoError(t, err, "expected workflow 'test.flow@1.0' to be loaded")
}

func TestLoadEmbeddedComponents_Error(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
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
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
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
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
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
		wasmContent := []byte{0x00, 0x61, 0x73, 0x6d}
		hasher := sha256.New()
		hasher.Write(wasmContent)
		correctDigest := hex.EncodeToString(hasher.Sum(nil))

		invalidMetaYamlContent := []byte(fmt.Sprintf(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  version: "1.0" # Missing name
wasmDigest: "%s"
wasmSize: 4
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
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
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

func TestLoadInstalledComponents_PathErrors(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
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

		// A non-existent path should be skipped and not return an error.
		err := loadInstalledComponents(context.Background(), svc, "/path/to/non/existent/dir", "", maxWasmSize, logger)
		assert.NoError(t, err)
		// Verify log output
		logOutput := logBuf.String()
		assert.Contains(t, logOutput, "Installed components directory not found, skipping.", "expected log to contain non-existent path warning")


	t.Run("path is a file, not a directory", func(t *testing.T) {
		// Create a logrus logger that discards output for tests
		logger := logrus.New()
		logger.SetOutput(io.Discard)

		// Create a temporary file
		tempFile, err := os.CreateTemp(t.TempDir(), "not_a_dir")
		require.NoError(t, err)
		tempFile.Close()

		// Trying to load from a file path should result in an error.
		loadErr := loadInstalledComponents(context.Background(), svc, tempFile.Name(), "", maxWasmSize, logger)
		assert.Error(t, loadErr, "expected an error when path is a file, not a directory")
		assert.Contains(t, loadErr.Error(), "not a directory")
	})
}

func TestLoadWorkflow_UnmarshalError(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())

	// This content has a valid kind, but the spec is not an object, causing a yaml.Unmarshal error.
	content := []byte("kind: Workflow\nspec: \"not an object\"")

	err := loadWorkflow(context.Background(), svc, content)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal workflow file")
}

// Helper function to create a logrus logger that discards output
func newDiscardLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return logger
}
