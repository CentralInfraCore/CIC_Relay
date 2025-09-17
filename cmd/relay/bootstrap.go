package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"centralrelay/core/cabinet"
	"gopkg.in/yaml.v3"
)

// ComponentHeader defines the common fields for identifying a component type.
type ComponentHeader struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// SchemaFile defines the structure of a Schema YAML file.
type SchemaFile struct {
	Metadata cabinet.BaseBlock `yaml:"metadata"`
	Spec     cabinet.SchemaDef `yaml:"spec"`
}

// WorkflowFile defines the structure of a Workflow YAML file.
type WorkflowFile struct {
	Metadata cabinet.BaseBlock `yaml:"metadata"`
	Spec     struct {
		Steps []string `yaml:"steps"`
	} `yaml:"spec"`
}

// ModuleFile defines the structure of a Module YAML file.
type ModuleFile struct {
	Metadata   cabinet.BaseBlock `yaml:"metadata"`
	Type       string            `yaml:"type"`
	Accepts    []string          `yaml:"accepts"`
	OutputKeys []string          `yaml:"outputKeys"`
	WasmDigest string            `yaml:"wasmDigest"`
	WasmSize   int64             `yaml:"wasmSize"`
}

// loadEmbeddedComponents loads components from the embedded filesystem.
// It fails hard on any error, as embedded components are considered critical.
func loadEmbeddedComponents(ctx context.Context, svc cabinet.Cabinet, schemas embed.FS, workflows embed.FS, maxWasmModuleSize int64) error {
	if err := loadComponentsFromFS(ctx, svc, schemas, ".", true, maxWasmModuleSize); err != nil {
		return fmt.Errorf("failed to load embedded schemas: %w", err)
	}
	if err := loadComponentsFromFS(ctx, svc, workflows, ".", true, maxWasmModuleSize); err != nil {
		return fmt.Errorf("failed to load embedded workflows: %w", err)
	}
	log.Println("Successfully loaded all embedded components.")
	return nil
}

// loadInstalledComponents loads components from a given filesystem path.
// It logs errors for individual files but does not fail hard.
func loadInstalledComponents(ctx context.Context, svc cabinet.Cabinet, path string, maxWasmModuleSize int64) error {
	// Check if the directory exists. If not, it's not a fatal error.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		log.Printf("Installed components directory not found at %s, skipping.", path)
		return nil
	}

	installedFS := os.DirFS(path)
	if err := loadComponentsFromFS(ctx, svc, installedFS, ".", false, maxWasmModuleSize); err != nil {
		// This would only be a filesystem-level error, which should be fatal.
		return fmt.Errorf("failed to load installed components from %s: %w", path, err)
	}
	log.Printf("Successfully loaded installed components from %s.", path)
	return nil
}

// loadComponentsFromFS walks a filesystem and loads all valid components.
// It can be configured to either fail hard on parsing errors or just log them.
func loadComponentsFromFS(ctx context.Context, svc cabinet.Cabinet, fsys fs.FS, root string, failOnError bool, maxWasmModuleSize int64) error {
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // Propagate filesystem errors
		}
		if d.IsDir() {
			return nil // Skip directories
		}

		// Skip non-YAML files, especially .wasm files, as they are handled by loadModule
		if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
			return nil // Skip non-YAML files
		}

		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			log.Printf("WARN: Could not read file %s: %v", path, err)
			return nil // Continue with the next file
		}

		var header ComponentHeader
		if err := yaml.Unmarshal(content, &header); err != nil {
			log.Printf("WARN: Could not unmarshal component header from %s: %v", path, err)
			return nil // Continue with the next file
		}

		var loadErr error
		switch header.Kind {
		case "Schema":
			loadErr = loadSchema(ctx, svc, content)
		case "Workflow":
			loadErr = loadWorkflow(ctx, svc, content)
		case "Module":
			loadErr = loadModule(ctx, svc, fsys, path, content, maxWasmModuleSize)
		default:
			log.Printf("WARN: Unknown component kind '%s' in file %s", header.Kind, path)
		}

		if loadErr != nil {
			formattedErr := fmt.Errorf("error loading component from %s: %w", path, loadErr)
			if failOnError {
				return formattedErr // Fail hard
			}
			log.Printf("WARN: %v", formattedErr) // Log and continue
		}

		return nil
	})
}

// loadSchema unmarshals and loads a single schema component.
func loadSchema(ctx context.Context, svc cabinet.Cabinet, content []byte) error {
	var file SchemaFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return fmt.Errorf("unmarshal schema file: %w", err)
	}

	schema, err := cabinet.NewSchema(ctx, file.Metadata, file.Spec)
	if err != nil {
		return fmt.Errorf("schema validation: %w", err)
	}

	if _, err := svc.GetSchema(schema.ID()); err == nil {
		log.Printf("INFO: Overwriting existing schema with ID: %s", schema.ID())
	}

	return svc.PutSchema(schema)
}

// loadWorkflow unmarshals and loads a single workflow component.
func loadWorkflow(ctx context.Context, svc cabinet.Cabinet, content []byte) error {
	var file WorkflowFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return fmt.Errorf("unmarshal workflow file: %w", err)
	}

	workflow, err := cabinet.NewWorkflow(ctx, file.Metadata, file.Spec.Steps)
	if err != nil {
		return fmt.Errorf("workflow validation: %w", err)
	}

	if _, err := svc.GetWorkflow(workflow.ID()); err == nil {
		log.Printf("INFO: Overwriting existing workflow with ID: %s", workflow.ID())
	}

	return svc.PutWorkflow(workflow)
}

// loadModule unmarshals a module descriptor, reads its associated WASM file,
// and loads the module into the cabinet.
func loadModule(ctx context.Context, svc cabinet.Cabinet, fsys fs.FS, yamlPath string, yamlContent []byte, maxWasmModuleSize int64) error {
	var file ModuleFile
	if err := yaml.Unmarshal(yamlContent, &file); err != nil {
		return fmt.Errorf("unmarshal module file: %w", err)
	}

	// Enforce that wasmDigest and wasmSize are provided.
	if file.WasmDigest == "" {
		return fmt.Errorf("wasmDigest is a required field for module %q", file.Metadata.Name+"@"+file.Metadata.Version)
	}
	if file.WasmSize == 0 {
		return fmt.Errorf("wasmSize is a required field for module %q", file.Metadata.Name+"@"+file.Metadata.Version)
	}

	// Construct the WASM file path based on the YAML file path.
	// Assumes WASM file has the same base name but with .wasm extension.
	wasmPath := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath)) + ".wasm"

	// Get file info to check size before reading.
	fileInfo, err := fs.Stat(fsys, wasmPath)
	if err != nil {
		return fmt.Errorf("stat WASM file %q: %w", wasmPath, err)
	}

	if fileInfo.Size() > maxWasmModuleSize {
		return fmt.Errorf("WASM module %q exceeds maximum allowed size of %d bytes (got %d)", wasmPath, maxWasmModuleSize, fileInfo.Size())
	}

	// Verify WASM size from YAML against actual file size.
	if file.WasmSize != fileInfo.Size() {
		return fmt.Errorf("WASM size mismatch for %q (expected: %d, got: %d)", wasmPath, file.WasmSize, fileInfo.Size())
	}

	wasmCode, err := fs.ReadFile(fsys, wasmPath)
	if err != nil {
		return fmt.Errorf("read WASM file %q: %w", wasmPath, err)
	}

	// Calculate SHA-256 hash of the WASM code.
	hasher := sha256.New()
	hasher.Write(wasmCode)
	calculatedDigest := hex.EncodeToString(hasher.Sum(nil))

	// Verify WASM digest from YAML against actual content hash.
	if file.WasmDigest != calculatedDigest {
		return fmt.Errorf("WASM digest mismatch for %q (expected: %s, got: %s)", wasmPath, file.WasmDigest, calculatedDigest)
	}

	moduleDesc, err := cabinet.NewModuleDescriptor(ctx, file.Metadata, file.Type, file.Accepts, file.OutputKeys, wasmCode)
	if err != nil {
		return fmt.Errorf("module validation: %w", err)
	}

	if _, err := svc.GetModule(moduleDesc.ID()); err == nil {
		log.Printf("INFO: Overwriting existing module with ID: %s", moduleDesc.ID())
	}

	return svc.PutModule(moduleDesc)
}
