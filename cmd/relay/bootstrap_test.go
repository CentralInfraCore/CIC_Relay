package main

import (
	"bytes"
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"centralrelay/core/cabinet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

	// 2. Execute
	err := loadEmbeddedComponents(svc, testSchemas, testWorkflows)
	require.NoError(t, err, "loadEmbeddedComponents should not return an error")

	// 3. Verify
	_, err = svc.GetSchema("test.schema@1.0")
	assert.NoError(t, err, "expected schema 'test.schema@1.0' to be loaded")

	_, err = svc.GetWorkflow("test.flow@1.0")
	assert.NoError(t, err, "expected workflow 'test.flow@1.0' to be loaded")
}

func TestLoadEmbeddedComponents_Error(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())

	t.Run("invalid schema", func(t *testing.T) {
		// Execute with an embed.FS that contains an invalid schema file.
		// The error from loadComponentsFromFS should be propagated up.
		err := loadEmbeddedComponents(svc, invalidSchemaFS, testWorkflows)
		assert.Error(t, err, "expected an error when loading invalid embedded schema components")
		assert.Contains(t, err.Error(), "failed to load embedded schemas", "error message should indicate which part failed")
	})

	t.Run("invalid workflow", func(t *testing.T) {
		// Execute with an embed.FS that contains an invalid workflow file.
		// The error from loadComponentsFromFS should be propagated up.
		err := loadEmbeddedComponents(svc, testSchemas, invalidWorkflowFS)
		assert.Error(t, err, "expected an error when loading invalid embedded workflow components")
		assert.Contains(t, err.Error(), "failed to load embedded workflows", "error message should indicate which part failed")
	})
}

func TestLoadModule(t *testing.T) {
	// 1. Setup
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())

	// Read the expected WASM code directly from the embedded FS for comparison
	expectedWasmCode, err := fs.ReadFile(testModuleFS, "testdata/modules/test_module.wasm")
	require.NoError(t, err)

	t.Run("successful module load", func(t *testing.T) {
		// Load the module YAML and WASM from the embedded FS
		yamlContent, err := fs.ReadFile(testModuleFS, "testdata/modules/test_module.yaml")
		require.NoError(t, err)

		// 2. Execute
		err = loadModule(svc, testModuleFS, "testdata/modules/test_module.yaml", yamlContent)
		require.NoError(t, err, "loadModule should not return an error for a valid module")

		// 3. Verify
		loadedModule, err := svc.GetModule("test.module@1.0")
		assert.NoError(t, err, "Module should be loaded into the cabinet")
		assert.Equal(t, expectedWasmCode, loadedModule.WasmCode, "Loaded module should contain the correct WASM code")
		assert.True(t, loadedModule.Meta.Valid, "Loaded module should be valid")
	})

	t.Run("missing wasm file", func(t *testing.T) {
		// Create a temporary FS that contains only the YAML, but no WASM
		tempFS := os.DirFS(t.TempDir())
		missingWasmYamlPath := "missing_wasm.yaml"
		missingWasmYamlContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: missing.wasm
  version: "1.0"
`)
		require.NoError(t, os.WriteFile(filepath.Join(t.TempDir(), missingWasmYamlPath), missingWasmYamlContent, 0644))

		// Execute loadModule with a FS that doesn't contain the WASM file
		err = loadModule(svc, tempFS, missingWasmYamlPath, missingWasmYamlContent)
		assert.Error(t, err, "loadModule should return an error if WASM file is missing")
		assert.Contains(t, err.Error(), "stat WASM file", "Error message should indicate WASM file stat issue") // Changed from "read WASM file"
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
		err = loadModule(svc, testModuleFS, embeddedYamlPath, modifiedYamlContent)

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
		err := loadModule(svc, testModuleFS, "invalid.yaml", invalidYamlContent)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal module file")
	})

	t.Run("wasm file too large", func(t *testing.T) {
		svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
		tempDir := t.TempDir()
		yamlContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: large.module
  version: "1.0"
`)
		yamlPath := "large_module.yaml"
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, yamlPath), yamlContent, 0644))

		largeWasmContent := make([]byte, maxWasmModuleSize+1)
		wasmPath := "large_module.wasm"
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, wasmPath), largeWasmContent, 0644))

		tempFS := os.DirFS(tempDir)
		err := loadModule(svc, tempFS, yamlPath, yamlContent)

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

		err = loadModule(svc, failingFS, "testdata/modules/test_module.yaml", yamlContent)

		assert.Error(t, err, "loadModule should return an error if wasm file cannot be read")
		assert.Contains(t, err.Error(), "read WASM file")
		assert.ErrorIs(t, err, os.ErrPermission)
	})
}

func TestLoadComponentsFromFS_WalkDirError(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	// Use an FS that will fail on WalkDir's internal call to ReadDir.
	failingFS := &failingReadDirFS{FS: testModuleFS}

	err := loadComponentsFromFS(svc, failingFS, ".", false)

	assert.Error(t, err, "loadComponentsFromFS should propagate errors from fs.WalkDir")
	assert.ErrorIs(t, err, os.ErrPermission, "the specific error from the mock fs should be propagated")
}

func TestLoadInstalledComponents_ErrorHandling(t *testing.T) {
	// 1. Setup
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())
	tempDir := t.TempDir()

	// Capture log output
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer func() {
		log.SetOutput(os.Stderr)
	}()

	var err error // Declare err here once

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
	err = loadInstalledComponents(svc, tempDir)
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

	// Load embedded components first
	require.NoError(t, loadEmbeddedComponents(svc, testSchemas, testWorkflows))

	var err error // Declare err here once

	t.Run("overwrite schema", func(t *testing.T) {
		// Create an installed schema that overwrites the embedded one
		overwriteContent := `
apiVersion: relay.cic.com/v1
kind: Schema
metadata:
  name: test.schema
  version: "1.0" # Same ID
  description: "This is the OVERWRITTEN schema."
spec: {}
`
		path := filepath.Join(tempDir, "overwrite_schema.yaml")
		require.NoError(t, os.WriteFile(path, []byte(overwriteContent), 0644))

		// 2. Execute
		err = loadInstalledComponents(svc, tempDir)
		require.NoError(t, err)

		// 3. Verify
		loadedSchema, err := svc.GetSchema("test.schema@1.0")
		require.NoError(t, err)
		assert.Equal(t, "This is the OVERWRITTEN schema.", loadedSchema.Meta.Description, "The schema should have been overwritten by the installed version")
	})

	t.Run("overwrite workflow", func(t *testing.T) {
		// Create an installed workflow that overwrites the embedded one
		overwriteContent := `
apiVersion: relay.cic.com/v1
kind: Workflow
metadata:
  name: test.flow
  version: "1.0" # Same ID
  description: "This is the OVERWRITTEN workflow."
spec:
  steps: ["new.step"]
`
		path := filepath.Join(tempDir, "overwrite_workflow.yaml")
		require.NoError(t, os.WriteFile(path, []byte(overwriteContent), 0644))

		// 2. Execute
		err = loadInstalledComponents(svc, tempDir)
		require.NoError(t, err)

		// 3. Verify
		loadedWorkflow, err := svc.GetWorkflow("test.flow@1.0")
		require.NoError(t, err)
		assert.Equal(t, "This is the OVERWRITTEN workflow.", loadedWorkflow.Meta.Description, "The workflow should have been overwritten by the installed version")
	})

	t.Run("overwrite module", func(t *testing.T) {
		// Create an installed module that overwrites the embedded one. The checksum is now updated to the correct value.
		overwriteContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: test.module
  version: "1.0"
  description: "This is the OVERWRITTEN module."
  checksum: "8f958d52a119097ff3a8841aade73f92f59abb91f51c3c7ce26f7d87be5057b4"
type: executor
accepts:
  - "test.schema@1.0"
outputKeys:
  - "new.result"
`)
		// Need to also provide the WASM file for the overwrite to work
		wasmContent, err := fs.ReadFile(testModuleFS, "testdata/modules/test_module.wasm")
		require.NoError(t, err)

		// Write the YAML and WASM to the temporary directory
		moduleYamlPath := filepath.Join(tempDir, "overwrite_module.yaml")
		require.NoError(t, os.WriteFile(moduleYamlPath, overwriteContent, 0644))
		moduleWasmPath := filepath.Join(tempDir, "overwrite_module.wasm") // Corrected WASM file name
		require.NoError(t, os.WriteFile(moduleWasmPath, wasmContent, 0644))

		// 2. Execute
		err = loadInstalledComponents(svc, tempDir)
		require.NoError(t, err)

		// 3. Verify
		loadedModule, err := svc.GetModule("test.module@1.0")
		require.NoError(t, err)
		assert.Equal(t, "This is the OVERWRITTEN module.", loadedModule.Meta.Description, "The module should have been overwritten by the installed version")
		assert.Contains(t, loadedModule.OutputKeys, "new.result", "The module output keys should have been overwritten")
	})
}

func TestLoadInstalledComponents_PathErrors(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())

	// Declare err here once
	var err error

	t.Run("non-existent path", func(t *testing.T) {
		// A non-existent path should be skipped and not return an error.
		err = loadInstalledComponents(svc, "/path/to/non/existent/dir")
		assert.NoError(t, err)
	})

	t.Run("path is a file, not a directory", func(t *testing.T) {
		// Create a temporary file
		tempFile, err := os.CreateTemp(t.TempDir(), "not_a_dir")
		require.NoError(t, err)
		tempFile.Close()

		// Trying to load from a file path should result in an error.
		err = loadInstalledComponents(svc, tempFile.Name())
		assert.Error(t, err, "expected an error when path is a file, not a directory")
	})
}

// This is a new, focused test to cover the final missing branch in loadWorkflow.
func TestLoadWorkflow_UnmarshalError(t *testing.T) {
	svc := cabinet.NewCabinetService(cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore())

	// This content has a valid kind, but the spec is not an object, causing a yaml.Unmarshal error.
	content := []byte("kind: Workflow\nspec: \"not an object\"")

	err := loadWorkflow(svc, content)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal workflow file")
}
