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
)

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
  checksum: "any"
`)
		require.NoError(t, os.WriteFile(filepath.Join(t.TempDir(), missingWasmYamlPath), missingWasmYamlContent, 0644))

		// Execute loadModule with a FS that doesn't contain the WASM file
		err = loadModule(svc, tempFS, missingWasmYamlPath, missingWasmYamlContent)
		assert.Error(t, err, "loadModule should return an error if WASM file is missing")
		assert.Contains(t, err.Error(), "read WASM file", "Error message should indicate WASM file read issue")
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		// Load the module YAML from the embedded FS
		yamlContent, err := fs.ReadFile(testModuleFS, "testdata/modules/test_module.yaml")
		require.NoError(t, err)

		// Modify the YAML content to have an incorrect checksum
		modifiedYamlContent := strings.Replace(string(yamlContent), "checksum: \"c8244585941d285659e23525cdcd35bf16fee357831e563498759cef4364d00a\"", "checksum: \"badchecksum\"", 1)

                // IMPORTANT: keep the original embedded YAML path so loadModule finds the WASM next to it in testModuleFS
                embeddedYamlPath := "testdata/modules/test_module.yaml"
                err = loadModule(svc, testModuleFS, embeddedYamlPath, []byte(modifiedYamlContent))
		assert.Error(t, err, "loadModule should return an error for checksum mismatch")
		assert.Contains(t, err.Error(), "checksum mismatch", "Error message should indicate checksum mismatch")
	})
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
			content:           "kind: Workflow\nmetadata: { version: '1.0' }", // Missing name\nsteps: []", // Added steps to make it a valid workflow structure for unmarshalling
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
			assert.True(t, strings.Contains(logOutput, tc.expectedLogString), "expected log to contain %q, but it was:\n%s", tc.expectedLogString, logOutput)
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
		assert.Equal(t, []string{"new.step"}, loadedWorkflow.Steps, "The workflow steps should have been overwritten")
	})

	t.Run("overwrite module", func(t *testing.T) {
		// Create an installed module that overwrites the embedded one
		overwriteContent := []byte(`
apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: test.module
  version: "1.0"
  description: "This is the OVERWRITTEN module."
  checksum: "5077136a4499d093d4fc0efecd075f855dfd404835c8e0b61a14d8517dd53808"
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
