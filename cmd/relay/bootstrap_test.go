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
	"strings"
	"testing"

	"centralrelay/core/cabinet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"github.com/sirupsen/logrus" // Import logrus
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

// --- Test Data for Embedded Files ---

//go:embed testdata/schemas/test_schema.yaml
var testSchemas embed.FS

//go:embed testdata/workflows/test_workflow.yaml
var testWorkflows embed.FS

//go:embed testdata/invalid/schema.yaml
var invalidSchemaFS embed.FS

//go:embed testdata/invalid/workflow.yaml
var invalidWorkflowFS embed.FS

//go:embed testdata/modules/test_module.yaml
//go:embed testdata/modules/test_module.wasm
var testModuleFS embed.FS

func TestLoadEmbeddedComponents(t *testing.T) {
	// 1. Setup
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	maxWasmSize := int64(16 * 1024 * 1024) // Default max WASM size for tests

	// Create a logrus logger that discards output for tests
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	// 2. Execute
	err := loadEmbeddedComponents(context.Background(), svc, testSchemas, testWorkflows, maxWasmSize, logger)
	require.NoError(t, err, "loadEmbeddedComponents should not return an error")

	// 3. Verify
	_, err = svc.GetSchema("test.schema@1.0")
	assert.NoError(t, err, "expected schema 'test.schema@1.0' to be loaded")

	_, err = svc.GetWorkflow("test.flow@1.0")
	assert.NoError(t, err, "expected workflow 'test.flow@1.0' to be loaded")
}

func TestLoadEmbeddedComponents_Error(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	maxWasmSize := int64(16 * 1024 * 1024) // Default max WASM size for tests

	// Create a logrus logger that discards output for tests
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	t.Run("invalid schema", func(t *testing.T) {
		// Execute with an embed.FS that contains an invalid schema file.
		// The error from loadComponentsFromFS should be propagated up.
		err := loadEmbeddedComponents(context.Background(), svc, invalidSchemaFS, testWorkflows, maxWasmSize, logger)
		assert.Error(t, err, "expected an error when loading invalid embedded schema components")
		assert.Contains(t, err.Error(), "failed to load embedded schemas", "error message should indicate which part failed")
	})

	t.Run("invalid workflow", func(t *testing.T) {
		// Execute with an embed.FS that contains an invalid workflow file.
		// The error from loadComponentsFromFS should be propagated up.
		err := loadEmbeddedComponents(context.Background(), svc, testSchemas, invalidWorkflowFS, maxWasmSize, logger)
		assert.Error(t, err, "expected an error when loading invalid embedded workflow components")
		assert.Contains(t, err.Error(), "failed to load embedded workflows", "error message should indicate which part failed")
	})
}

func TestLoadComponentsFromFS(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	maxWasmSize := int64(16 * 1024 * 1024) // Default max WASM size for tests

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

		err := loadComponentsFromFS(context.Background(), svc, tempFS, ".", false, maxWasmSize, logger)
		assert.NoError(t, err, "loadComponentsFromFS should not return a fatal error for unmarshal errors")
		assert.Contains(t, logBuf.String(), "unmarshal module file", "log should contain unmarshal error")
	})
}

func TestLoadModule(t *testing.T) {
	// 1. Setup
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	maxWasmSize := int64(16 * 1024 * 1024) // Default max WASM size for tests

	// Read the expected WASM code directly from the embedded FS for comparison
	expectedWasmCode, err := fs.ReadFile(testModuleFS, "testdata/modules/test_module.wasm")
	require.NoError(t, err)

	t.Run("successful module load", func(t *testing.T) {
		// Load the module YAML and WASM from the embedded FS
		yamlContent, err := fs.ReadFile(testModuleFS, "testdata/modules/test_module.yaml")
		require.NoError(t, err)

		// 2. Execute
		err = loadModule(context.Background(), svc, testModuleFS, "testdata/modules/test_module.yaml", yamlContent, maxWasmSize)
		require.NoError(t, err, "loadModule should not return an error for a valid module")

		// 3. Verify
		loadedModule, err := svc.GetModule("test.module@1.0")
		assert.NoError(t, err, "Module should be loaded into the cabinet")
		assert.Equal(t, expectedWasmCode, loadedModule.WasmCode, "Loaded module should contain the correct WASM code")
		assert.True(t, loadedModule.Meta.Valid, "Loaded module should be valid")
	})

	t.Run("missing wasm digest", func(t *testing.T) {
		// This YAML is missing the mandatory wasmDigest field.
		missingDigestYaml := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: missing.digest
  version: "1.0"
wasmSize: 123
`)
		err = loadModule(context.Background(), svc, testModuleFS, "missing_digest.yaml", missingDigestYaml, maxWasmSize)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "wasmDigest is a required field")
	})

	t.Run("missing wasm size", func(t *testing.T) {
		// This YAML is missing the mandatory wasmSize field.
		missingSizeYaml := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: missing.size
  version: "1.0"
wasmDigest: "some-digest"
`)
		err = loadModule(context.Background(), svc, testModuleFS, "missing_size.yaml", missingSizeYaml, maxWasmSize)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "wasmSize is a required field")
	})

	t.Run("missing wasm file", func(t *testing.T) {
		// Create a temporary FS that contains only the YAML, but no WASM
		tempFS := os.DirFS(t.TempDir())
		missingWasmYamlPath := "missing_wasm.yaml"
		// This YAML is now valid on its own, but the corresponding .wasm file is missing.
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

		// Execute loadModule with a FS that doesn't contain the WASM file
		err = loadModule(context.Background(), svc, tempFS, missingWasmYamlPath, missingWasmYamlContent, maxWasmSize)
		assert.Error(t, err, "loadModule should return an error if WASM file is missing")
		assert.Contains(t, err.Error(), "stat WASM file", "Error message should indicate WASM file stat issue")
		assert.Contains(t, err.Error(), "no such file or directory", "Error message should indicate WASM file not found")
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		// This test is now robust. It unmarshals the YAML, modifies the checksum in the map,
		// and then remarshals it, guaranteeing that the checksum is incorrect.
		yamlContent, err := fs.ReadFile(testModuleFS, "testdata/modules/test_module.yaml")
		require.NoError(t, err)

		// Unmarshal into a generic map to manipulate it
		var data map[string]any
		require.NoError(t, yaml.Unmarshal(yamlContent, &data))

		// Navigate and modify the checksum
		metadata, ok := data["metadata"].(map[string]any)
		require.True(t, ok, "metadata field not found or not a map")
		metadata["checksum"] = "badchecksum"

		// Remarshal to YAML
		modifiedYamlContent, err := yaml.Marshal(data)
		require.NoError(t, err)

		// Execute with the modified content
		embeddedYamlPath := "testdata/modules/test_module.yaml"
		err = loadModule(context.Background(), svc, testModuleFS, embeddedYamlPath, modifiedYamlContent, maxWasmSize)

		// Verify that the error is a checksum mismatch error
		assert.Error(t, err, "loadModule should return an error for checksum mismatch")
		assert.ErrorIs(t, err, cabinet.ErrInvalid, "error should wrap ErrInvalid")
		assert.Contains(t, err.Error(), "checksum mismatch", "error message should indicate checksum mismatch")
	})

	t.Run("invalid yaml content", func(t *testing.T) {
		svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
		invalidYamlContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata: "this should be a map"
`)
		err = loadModule(context.Background(), svc, testModuleFS, "invalid.yaml", invalidYamlContent, maxWasmSize)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal module file")
	})

	t.Run("wasm file too large", func(t *testing.T) {
		svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
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
		err = loadModule(context.Background(), svc, tempFS, yamlPath, yamlContent, maxWasmSize)

		assert.Error(t, err, "loadModule should return an error for a wasm file that is too large")
		assert.Contains(t, err.Error(), "exceeds maximum allowed size")
	})

	t.Run("read wasm file error", func(t *testing.T) {
		svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
		// Use a mock FS that fails on Read for the wasm file.
		failingFS := &failingReadFileFS{
			FS:         testModuleFS,
			failOnRead: map[string]bool{"testdata/modules/test_module.wasm": true},
		}

		yamlContent, err := fs.ReadFile(testModuleFS, "testdata/modules/test_module.yaml")
		require.NoError(t, err)

		err = loadModule(context.Background(), svc, failingFS, "testdata/modules/test_module.yaml", yamlContent, maxWasmSize)

		assert.Error(t, err, "loadModule should return an error if wasm file cannot be read")
		assert.Contains(t, err.Error(), "read WASM file")
		assert.ErrorIs(t, err, os.ErrPermission)
	})

	t.Run("wasm digest mismatch", func(t *testing.T) {
		// Create a temporary FS with a module YAML that has a wrong digest
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
		err = loadModule(context.Background(), svc, tempFS, wrongDigestYamlPath, wrongDigestYamlContent, maxWasmSize)

		assert.Error(t, err, "loadModule should return an error for WASM digest mismatch")
		assert.Contains(t, err.Error(), "WASM digest mismatch")
	})

	t.Run("wasm size mismatch", func(t *testing.T) {
		// Create a temporary FS with a module YAML that has a wrong size
		tempDir := t.TempDir()
		wrongSizeYamlPath := "wrong_size.yaml"
		wrongSizeYamlContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: wrong.size
  version: "1.0"
wasmDigest: "292ed65ec666d77aa17f84777f83e6e5aca98d61ba75461ca76fe8313a44382c"
wasmSize: 999
`)
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, wrongSizeYamlPath), wrongSizeYamlContent, 0644))
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "wrong_size.wasm"), []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x02, 0x03, 0x04}, 0644))

		tempFS := os.DirFS(tempDir)
		err = loadModule(context.Background(), svc, tempFS, wrongSizeYamlPath, wrongSizeYamlContent, maxWasmSize)

		assert.Error(t, err, "loadModule should return an error for WASM size mismatch")
		assert.Contains(t, err.Error(), "WASM size mismatch")
	})

	t.Run("module validation error", func(t *testing.T) {
		// Create a temporary FS with a module YAML that has invalid metadata
		tempDir := t.TempDir()
		invalidMetaYamlPath := "invalid_meta.yaml"

		// Define the dummy WASM content and calculate its digest so that the digest
		// check passes and we can test the subsequent metadata validation.
		wasmContent := []byte{0x00, 0x61, 0x73, 0x6d}
		hasher := sha256.New()
		hasher.Write(wasmContent)
		correctDigest := hex.EncodeToString(hasher.Sum(nil))

		// Create YAML with the correct digest but invalid metadata (missing name).
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
		err = loadModule(context.Background(), svc, tempFS, invalidMetaYamlPath, invalidMetaYamlContent, maxWasmSize)

		assert.Error(t, err, "loadModule should return an error for module validation failure")
		assert.Contains(t, err.Error(), "module validation")
		assert.Contains(t, err.Error(), "name and version are required")
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

	// Use an FS that will fail on WalkDir's internal call to ReadDir.
	failingFS := &failingReadDirFS{FS: testModuleFS}

	// Pass the logger to loadComponentsFromFS
	err := loadComponentsFromFS(context.Background(), svc, failingFS, ".", false, maxWasmSize, logger)

	assert.Error(t, err, "loadComponentsFromFS should propagate errors from fs.WalkDir")
	assert.ErrorIs(t, err, os.ErrPermission, "the specific error from the mock fs should be propagated")
}

func TestLoadInstalledComponents_ErrorHandling(t *testing.T) {
	// 1. Setup
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	tempDir := t.TempDir()
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

	testCases := []struct {
		name              string
		filename          string
		content           string
		expectedLogString string
	}{
		{
			name:              "invalid yaml format",
			filename:          "invalid_yaml.yaml",
			content:           "key: value: another_value",
			expectedLogString: "Could not unmarshal component header",
		},
		{
			name:              "unknown kind value",
			filename:          "unknown_kind.yaml",
			content:           "kind: UnknownComponent",
			expectedLogString: "Unknown component kind",
		},
		{
			name:              "schema validation error",
			filename:          "schema_validation_error.yaml",
			content:           "kind: Schema\nmetadata: { version: '1.0' }", // Missing name
			expectedLogString: "error loading component from",
		},
		{
			name:              "workflow validation error",
			filename:          "workflow_validation_error.yaml",
			content:           "kind: Workflow\nmetadata: { version: '1.0' }\nspec:\n  steps: []", // Corrected structure
			expectedLogString: "error loading component from",
		},
		{
			name:              "schema unmarshal error",
			filename:          "schema_unmarshal_error.yaml",
			content:           "kind: Schema\nmetadata: \"not an object\"",
			expectedLogString: "error loading component from",
		},
		{
			name:              "workflow unmarshal error",
			filename:          "workflow_unmarshal_error.yaml",
			content:           "kind: Workflow\nspec: \"not an object\"",
			expectedLogString: "error loading component from",
		},
	}

	for _, tc := range testCases {
		path := filepath.Join(tempDir, tc.filename)
		require.NoError(t, os.WriteFile(path, []byte(tc.content), 0644))
	}

	// 2. Execute
	err := loadInstalledComponents(context.Background(), svc, tempDir, maxWasmSize, logger)
	require.NoError(t, err, "loadInstalledComponents should not return a fatal error for invalid files")

	// 3. Verify log output
	logOutput := logBuf.String()
	for _, tc := range testCases {
		assert.True(t, strings.Contains(logOutput, tc.expectedLogString), "expected log to contain %q for test %q, but it was:\n%s", tc.expectedLogString, tc.name, logOutput)
	}
}

func TestLoadInstalledComponents_Overwrite(t *testing.T) {
	// 1. Setup
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	tempDir := t.TempDir()
	maxWasmSize := int64(16 * 1024 * 1024) // Default max WASM size for tests

	// Create a logrus logger that discards output for tests
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	// Load embedded components first to have something to overwrite
	require.NoError(t, loadEmbeddedComponents(context.Background(), svc, testSchemas, testWorkflows, maxWasmSize, logger))

	t.Run("overwrite schema", func(t *testing.T) {
		// Dynamically create the schema and its checksum
		meta := cabinet.BaseBlock{
			Name:        "test.schema",
			Version:     "1.0",
			Description: "This is the OVERWRITTEN schema.",
		}
		spec := cabinet.SchemaDef{}

		schema, err := cabinet.NewSchema(context.Background(), meta, spec)
		require.NoError(t, err, "NewSchema should not fail")

		finalYAML := map[string]any{
			"apiVersion": "relay.cic.com/v1",
			"kind":       "Schema",
			"metadata":   schema.Meta,
			"spec":       schema.Spec,
		}
		overwriteContent, err := yaml.Marshal(finalYAML)
		require.NoError(t, err)

		path := filepath.Join(tempDir, "overwrite_schema.yaml")
		require.NoError(t, os.WriteFile(path, overwriteContent, 0644))

		// 2. Execute
		err = loadInstalledComponents(context.Background(), svc, tempDir, maxWasmSize, logger)
		require.NoError(t, err)

		// 3. Verify
		loadedSchema, err := svc.GetSchema("test.schema@1.0")
		require.NoError(t, err)
		assert.Equal(t, "This is the OVERWRITTEN schema.", loadedSchema.Meta.Description, "The schema should have been overwritten")
		assert.Equal(t, schema.Meta.Checksum, loadedSchema.Meta.Checksum, "The checksum of the overwritten schema should match the calculated one")
	})

	t.Run("overwrite workflow", func(t *testing.T) {
		// Dynamically create the workflow and its checksum
		meta := cabinet.BaseBlock{
			Name:        "test.flow",
			Version:     "1.0",
			Description: "This is the OVERWRITTEN workflow.",
		}
		steps := []string{"new.step"}

		workflow, err := cabinet.NewWorkflow(context.Background(), meta, steps)
		require.NoError(t, err, "NewWorkflow should not fail")

		finalYAML := map[string]any{
			"apiVersion": "relay.cic.com/v1",
			"kind":       "Workflow",
			"metadata":   workflow.Meta,
			"spec":       map[string]any{"steps": workflow.Steps},
		}

		overwriteContent, err := yaml.Marshal(finalYAML)
		require.NoError(t, err)

		path := filepath.Join(tempDir, "overwrite_workflow.yaml")
		require.NoError(t, os.WriteFile(path, overwriteContent, 0644))

		// 2. Execute
		err = loadInstalledComponents(context.Background(), svc, tempDir, maxWasmSize, logger)
		require.NoError(t, err)

		// 3. Verify
		loadedWorkflow, err := svc.GetWorkflow("test.flow@1.0")
		require.NoError(t, err)
		assert.Equal(t, "This is the OVERWRITTEN workflow.", loadedWorkflow.Meta.Description, "The workflow should have been overwritten")
		assert.Equal(t, workflow.Meta.Checksum, loadedWorkflow.Meta.Checksum, "The checksum of the overwritten workflow should match the calculated one")
	})

	t.Run("overwrite module", func(t *testing.T) {
		// Dynamically create the module and its checksum
		wasmContent, err := fs.ReadFile(testModuleFS, "testdata/modules/test_module.wasm")
		require.NoError(t, err)

		// Define the module's properties
		meta := cabinet.BaseBlock{
			Name:        "test.module",
			Version:     "1.0",
			Description: "This is the OVERWRITTEN module.",
		}
		moduleType := "executor"
		accepts := []string{"test.schema@1.0"}
		outputKeys := []string{"new.result"}

		// Create a new module descriptor, which calculates the canonical checksum
		module, err := cabinet.NewModuleDescriptor(context.Background(), meta, moduleType, accepts, outputKeys, wasmContent)
		require.NoError(t, err, "NewModuleDescriptor should not fail")

		// For the YAML file, we also need the WASM digest and size for CAS.
		hasher := sha256.New()
		hasher.Write(wasmContent)
		wasmDigest := hex.EncodeToString(hasher.Sum(nil))
		wasmSize := int64(len(wasmContent))

		// Create the structure for the YAML file
		finalYAML := ModuleFile{
			Metadata:   module.Meta, // This now includes the correct checksum
			Type:       module.Type,
			Accepts:    module.Accepts,
			OutputKeys: module.OutputKeys,
			WasmDigest: wasmDigest,
			WasmSize:   wasmSize,
		}

		// Use a map for the final YAML to include apiVersion and kind
		finalYAMLMap := map[string]any{
			"apiVersion": "relay.cic.com/v1",
			"kind":       "Module",
			"metadata":   finalYAML.Metadata,
			"type":       finalYAML.Type,
			"accepts":    finalYAML.Accepts,
			"outputKeys": finalYAML.OutputKeys,
			"wasmDigest": finalYAML.WasmDigest,
			"wasmSize":   finalYAML.WasmSize,
		}

		overwriteContent, err := yaml.Marshal(finalYAMLMap)
		require.NoError(t, err)

		// Write the YAML and WASM to the temporary directory
		moduleYamlPath := filepath.Join(tempDir, "overwrite_module.yaml")
		require.NoError(t, os.WriteFile(moduleYamlPath, overwriteContent, 0644))
		moduleWasmPath := filepath.Join(tempDir, "overwrite_module.wasm")
		require.NoError(t, os.WriteFile(moduleWasmPath, wasmContent, 0644))

		// 2. Execute
		err = loadInstalledComponents(context.Background(), svc, tempDir, maxWasmSize, logger)
		require.NoError(t, err)

		// 3. Verify
		loadedModule, err := svc.GetModule("test.module@1.0")
		require.NoError(t, err)
		assert.Equal(t, "This is the OVERWRITTEN module.", loadedModule.Meta.Description, "The module should have been overwritten")
		assert.Contains(t, loadedModule.OutputKeys, "new.result", "The module output keys should have been overwritten")
		assert.Equal(t, module.Meta.Checksum, loadedModule.Meta.Checksum, "The checksum of the overwritten module should match the calculated one")
	})
}

func TestLoadInstalledComponents_PathErrors(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	maxWasmSize := int64(16 * 1024 * 1024) // Default max WASM size for tests

	t.Run("non-existent path", func(t *testing.T) {
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
		err := loadInstalledComponents(context.Background(), svc, "/path/to/non/existent/dir", maxWasmSize, logger)
		assert.NoError(t, err)
		// Verify log output
		logOutput := logBuf.String()
		assert.Contains(t, logOutput, "Installed components directory not found, skipping.", "expected log to contain non-existent path warning")
	})

	t.Run("path is a file, not a directory", func(t *testing.T) {
		// Create a logrus logger that discards output for tests
		logger := logrus.New()
		logger.SetOutput(io.Discard)

		// Create a temporary file
		tempFile, err := os.CreateTemp(t.TempDir(), "not_a_dir")
		require.NoError(t, err)
		tempFile.Close()

		// Trying to load from a file path should result in an error.
		loadErr := loadInstalledComponents(context.Background(), svc, tempFile.Name(), maxWasmSize, logger)
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
