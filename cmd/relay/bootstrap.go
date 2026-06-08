package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"centralrelay/core/cabinet"
	"centralrelay/core/modules/certselfsigned"
	"centralrelay/core/modules/cibuild"
	"centralrelay/core/modules/schemacompile"
	"centralrelay/core/modules/schemapipeline"
	"centralrelay/pkg/merkle"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// registerNativeComponents creates and registers all built-in Go components in the cabinet.
// pkiAssets must not be nil; pass the result of bootstrapPKI.
// bctx carries build-time metadata for the Merkle-root verification hash (M33).
// CAFingerprint from pkiAssets is merged into bctx if not already set.
// signer is used to sign artifact build_hash in cic.artifact.sign; pass nil if Vault is unavailable.
// cfg carries schema pipeline wiring (SchemaBuildDir, SchemaBuilderContainer).
func registerNativeComponents(ctx context.Context, svc cabinet.Cabinet, pkiAssets *PKIAssets, bctx merkle.BuildContext, signer schemacompile.ArtifactSigner, cfg *config) error {
	if bctx.CAFingerprint == "" {
		bctx.CAFingerprint = pkiAssets.CAFingerprint
	}
	// --- Register core.test module ---
	testFuncs := make(cabinet.NativeFunctions)
	testFuncs["echo"] = cabinet.CoreEcho

	coreTestMeta := cabinet.BaseBlock{Name: "core.test", Version: "1.0", Description: "A native module for testing basic functionality."}
	// Corrected call to NewModuleDescriptor to match the current signature
	coreTestModule, err := cabinet.NewModuleDescriptor(ctx, coreTestMeta, "native/test", nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create core.test module descriptor: %w", err)
	}
	coreTestModule.NativeImpl = testFuncs // Attach the native function map

	if err := svc.PutModule(coreTestModule); err != nil {
		return fmt.Errorf("failed to register core.test module: %w", err)
	}

	// --- Register ci.build module ---
	ciBuildFuncs := make(cabinet.NativeFunctions)
	ciBuildFuncs["build"] = cibuild.Execute

	ciBuildMeta := cabinet.BaseBlock{Name: "ci.build", Version: "1.0", Description: "CI build module: runs a make target and returns the artifact SHA-256 digest."}
	ciBuildModule, err := cabinet.NewModuleDescriptor(ctx, ciBuildMeta, "native/ci", nil, []string{"artifact_path", "artifact_digest", "exit_code"}, nil)
	if err != nil {
		return fmt.Errorf("failed to create ci.build module descriptor: %w", err)
	}
	ciBuildModule.NativeImpl = ciBuildFuncs

	if err := svc.PutModule(ciBuildModule); err != nil {
		return fmt.Errorf("failed to register ci.build module: %w", err)
	}

	// --- Register cic.source.assert module ---
	// L0.3: wire certificate chain verification when PKI trust store is loaded.
	var certVerify schemacompile.CertVerifyFunc
	if pkiAssets.TrustStoreLoaded {
		certVerify = func(cert *x509.Certificate) error {
			opts := cabinet.VerifyOptions{
				Roots:         pkiAssets.Roots,
				Intermediates: pkiAssets.Intermediates,
				KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
				CRLs:          pkiAssets.CRLs,
			}
			_, err := cabinet.VerifyCertificateChain(cert, opts)
			return err
		}
	}
	assertMeta := cabinet.BaseBlock{Name: "cic.source.assert", Version: "1.0", Description: "Source assertion validator (L0.3)."}
	assertModule, err := cabinet.NewModuleDescriptor(ctx, assertMeta, "native/schema", nil, []string{"asserted", "policy_version", "schema", "version", "metadata", "spec", "raw_yaml"}, nil)
	if err != nil {
		return fmt.Errorf("failed to create cic.source.assert module descriptor: %w", err)
	}
	assertModule.NativeImpl = cabinet.NativeFunctions{"assert": schemacompile.NewAssertSourceFunc(certVerify)}
	if err := svc.PutModule(assertModule); err != nil {
		return fmt.Errorf("failed to register cic.source.assert module: %w", err)
	}

	// --- Register cic.schema.build module ---
	buildMeta := cabinet.BaseBlock{Name: "cic.schema.build", Version: "1.0", Description: "Schema build module (stub)."}
	buildModule, err := cabinet.NewModuleDescriptor(ctx, buildMeta, "native/schema", nil, []string{"schema", "version", "raw_artifact", "metadata", "spec"}, nil)
	if err != nil {
		return fmt.Errorf("failed to create cic.schema.build module descriptor: %w", err)
	}
	buildModule.NativeImpl = cabinet.NativeFunctions{"build": schemacompile.BuildSchema}
	if err := svc.PutModule(buildModule); err != nil {
		return fmt.Errorf("failed to register cic.schema.build module: %w", err)
	}

	// --- Register cic.artifact.sign module ---
	signMeta := cabinet.BaseBlock{Name: "cic.artifact.sign", Version: "1.0", Description: "Artifact sign module (stub). Emits verification_root (M33)."}
	signModule, err := cabinet.NewModuleDescriptor(ctx, signMeta, "native/schema", nil,
		[]string{"artifact", "build_hash", "cic_sign", "cic_signed_ca", "verification_root"}, nil)
	if err != nil {
		return fmt.Errorf("failed to create cic.artifact.sign module descriptor: %w", err)
	}
	signModule.NativeImpl = cabinet.NativeFunctions{"sign": schemacompile.NewSignArtifactFunc(bctx, signer)}
	if err := svc.PutModule(signModule); err != nil {
		return fmt.Errorf("failed to register cic.artifact.sign module: %w", err)
	}

	// --- Register cic.schema.compile workflow ---
	compileMeta := cabinet.BaseBlock{Name: "cic.schema.compile", Version: "1.0", Description: "Schema compilation workflow: assert source, build, sign artifact."}
	compileWorkflow, err := cabinet.NewWorkflow(ctx, compileMeta, []string{
		"cic.source.assert@1.0.assert(input) -> assert_result",
		"cic.schema.build@1.0.build(assert_result) -> artifact",
		"cic.artifact.sign@1.0.sign(artifact) -> signed_artifact",
	})
	if err != nil {
		return fmt.Errorf("failed to create cic.schema.compile workflow: %w", err)
	}
	if err := svc.PutWorkflow(compileWorkflow); err != nil {
		return fmt.Errorf("failed to register cic.schema.compile workflow: %w", err)
	}

	// --- Register cert.selfsigned module ---
	certFuncs := make(cabinet.NativeFunctions)
	certFuncs["generate"] = certselfsigned.Generate

	certMeta := cabinet.BaseBlock{Name: "cert.selfsigned", Version: "1.0", Description: "Self-signed ECDSA P-256 certificate generator."}
	certModule, err := cabinet.NewModuleDescriptor(ctx, certMeta, "native/cert", []string{"cert.selfsigned@1.0"}, []string{"cert_pem", "key_pem", "common_name", "not_before", "not_after"}, nil)
	if err != nil {
		return fmt.Errorf("failed to create cert.selfsigned module descriptor: %w", err)
	}
	certModule.NativeImpl = certFuncs
	if err := svc.PutModule(certModule); err != nil {
		return fmt.Errorf("failed to register cert.selfsigned module: %w", err)
	}

	// --- Register cert.selfsigned workflow ---
	certWorkflowMeta := cabinet.BaseBlock{Name: "cert.selfsigned", Version: "1.0", Description: "Self-signed certificate generation workflow (dev level)."}
	certWorkflow, err := cabinet.NewWorkflow(ctx, certWorkflowMeta, []string{
		"cert.selfsigned@1.0.generate(input) -> cert",
	})
	if err != nil {
		return fmt.Errorf("failed to create cert.selfsigned workflow: %w", err)
	}
	if err := svc.PutWorkflow(certWorkflow); err != nil {
		return fmt.Errorf("failed to register cert.selfsigned workflow: %w", err)
	}

	// --- Register schema pipeline modules ---
	pipelineCfg := &schemapipeline.PipelineConfig{
		BuildDir:         cfg.SchemaBuildDir,
		BuilderContainer: cfg.SchemaBuilderContainer,
	}

	pipelineSteps := []struct {
		name string
		fn   func(context.Context, interface{}) (interface{}, error)
		out  []string
	}{
		{
			"cic.pipeline.start",
			pipelineCfg.NewStartFunc(),
			[]string{"source_ref", "source_tree_digest", "builder_image_digest", "step_hash", "build_dir", "builder_container"},
		},
		{
			"cic.pipeline.test",
			pipelineCfg.NewTestFunc(),
			[]string{"test_exit_code", "test_stdout_digest", "step_hash"},
		},
		{
			"cic.pipeline.validate",
			pipelineCfg.NewValidateFunc(),
			[]string{"validate_exit_code", "validate_stdout_digest", "step_hash"},
		},
		{
			"cic.pipeline.release",
			pipelineCfg.NewReleaseFunc(),
			[]string{"artifact_digest", "artifact_size", "release_stdout_digest", "step_hash", "source_assertion"},
		},
	}

	for _, step := range pipelineSteps {
		meta := cabinet.BaseBlock{Name: step.name, Version: "1.0", Description: step.name + " pipeline step."}
		mod, err := cabinet.NewModuleDescriptor(ctx, meta, "native/pipeline", nil, step.out, nil)
		if err != nil {
			return fmt.Errorf("failed to create %s module: %w", step.name, err)
		}
		funcName := step.name[strings.LastIndex(step.name, ".")+1:] // "start", "test", "validate", "release"
		mod.NativeImpl = cabinet.NativeFunctions{funcName: step.fn}
		if err := svc.PutModule(mod); err != nil {
			return fmt.Errorf("failed to register %s module: %w", step.name, err)
		}
	}

	// --- Register cic.schemas.pipeline workflow ---
	pipelineWorkflowMeta := cabinet.BaseBlock{
		Name:        "cic.schemas.pipeline",
		Version:     "1.0",
		Description: "Full CIC-Schemas build pipeline: clone, test, validate, release, assert, build, sign.",
	}
	pipelineWorkflow, err := cabinet.NewWorkflow(ctx, pipelineWorkflowMeta, []string{
		"cic.pipeline.start@1.0.start(input) -> start_result",
		"cic.pipeline.test@1.0.test(start_result) -> test_result",
		"cic.pipeline.validate@1.0.validate(test_result) -> validate_result",
		"cic.pipeline.release@1.0.release(validate_result) -> release_result",
		"cic.source.assert@1.0.assert(release_result) -> assert_result",
		"cic.schema.build@1.0.build(assert_result) -> artifact",
		"cic.artifact.sign@1.0.sign(artifact) -> signed_artifact",
	})
	if err != nil {
		return fmt.Errorf("failed to create cic.schemas.pipeline workflow: %w", err)
	}
	if err := svc.PutWorkflow(pipelineWorkflow); err != nil {
		return fmt.Errorf("failed to register cic.schemas.pipeline workflow: %w", err)
	}

	logrus.Info("Successfully registered all native components.")
	return nil
}

// PKIAssets holds the loaded public key infrastructure components.
type PKIAssets struct {
	Roots            *x509.CertPool
	Intermediates    *x509.CertPool
	CRLs             []*x509.RevocationList
	TrustStoreLoaded bool   // true when at least one root CA was loaded
	CAFingerprint    string // sha256 of the first loaded root CA DER bytes, "sha256:<hex>"
	CertChainHash    string // sha256 of all loaded CA cert DER bytes concatenated, "sha256:<hex>"
}

// ComponentHeader defines the common fields for identifying a component type.
type ComponentHeader struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// SchemaFile defines the structure of a Schema YAML file.
type SchemaFile struct {
	Identity cabinet.SchemaIdentity `yaml:"identity"`
	Config   cabinet.SchemaConfig   `yaml:"config"`
	Signing  cabinet.SchemaSigning  `yaml:"signing"`
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
func loadEmbeddedComponents(ctx context.Context, svc cabinet.Cabinet, schemas fs.FS, workflows fs.FS, pki fs.FS, pkiPath string, maxWasmModuleSize int64, logger *logrus.Logger) error {
	pkiAssets, err := loadPKIAssets(pki, pkiPath, logger)
	if err != nil {
		return fmt.Errorf("failed to load embedded PKI assets: %w", err)
	}
	if err := loadComponentsFromFS(ctx, svc, schemas, ".", true, pkiAssets, maxWasmModuleSize, logger); err != nil {
		return fmt.Errorf("failed to load embedded schemas: %w", err)
	}
	if err := loadComponentsFromFS(ctx, svc, workflows, ".", true, pkiAssets, maxWasmModuleSize, logger); err != nil {
		return fmt.Errorf("failed to load embedded workflows: %w", err)
	}
	logger.Info("Successfully loaded all embedded components.")
	return nil
}

// loadInstalledComponents loads components from a given filesystem path.
// pkiAssets must not be nil — pass the result of bootstrapPKI for trust-verified loading.
func loadInstalledComponents(ctx context.Context, svc cabinet.Cabinet, componentsPath string, pkiAssets *PKIAssets, maxWasmModuleSize int64, logger *logrus.Logger) error {

	fileInfo, err := os.Stat(componentsPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.WithField("path", componentsPath).Warn("Installed components directory not found, skipping.")
			return nil
		}
		return fmt.Errorf("could not stat installed components path %s: %w", componentsPath, err)
	}

	if !fileInfo.IsDir() {
		err := fmt.Errorf("path is a file, not a directory")
		logger.WithField("path", componentsPath).Error(err.Error())
		return err
	}

	installedFS := os.DirFS(componentsPath)
	if err := loadComponentsFromFS(ctx, svc, installedFS, ".", false, pkiAssets, maxWasmModuleSize, logger); err != nil {
		return fmt.Errorf("failed to load installed components from %s: %w", componentsPath, err)
	}
	logger.WithField("path", componentsPath).Info("Successfully loaded installed components.")
	return nil
}

// loadComponentsFromFS walks a filesystem and loads all valid components.
func loadComponentsFromFS(ctx context.Context, svc cabinet.Cabinet, fsys fs.FS, root string, failOnError bool, pkiAssets *PKIAssets, maxWasmModuleSize int64, logger *logrus.Logger) error {
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // Propagate filesystem errors
		}
		if d.IsDir() {
			return nil // Skip directories
		}

		if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
			return nil // Skip non-YAML files
		}

		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			logger.WithFields(logrus.Fields{"path": path, "error": err}).Warn("Could not read file")
			return nil
		}

		var header ComponentHeader
		if err := yaml.Unmarshal(content, &header); err != nil {
			logger.WithFields(logrus.Fields{"path": path, "error": err}).Warn("Could not unmarshal component header")
			return nil
		}

		var loadErr error
		switch header.Kind {
		case "Schema":
			loadErr = loadSchema(ctx, svc, content)
		case "Workflow":
			loadErr = loadWorkflow(ctx, svc, content)
		case "Module":
			loadErr = loadModule(ctx, svc, fsys, path, content, pkiAssets, maxWasmModuleSize, logger)
		default:
			logger.WithFields(logrus.Fields{"kind": header.Kind, "path": path}).Warn("Unknown component kind in file")
		}

		if loadErr != nil {
			formattedErr := fmt.Errorf("error loading component from %s: %w", path, loadErr)
			if failOnError {
				return formattedErr
			}
			logger.WithField("error", formattedErr).Warn("Error loading component")
		}

		return nil
	})
}

func loadSchema(ctx context.Context, svc cabinet.Cabinet, content []byte) error {
	var file SchemaFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return fmt.Errorf("unmarshal schema file: %w", err)
	}
	schema, err := cabinet.NewSchema(ctx, file.Identity, file.Config, file.Signing)
	if err != nil {
		return fmt.Errorf("schema validation: %w", err)
	}
	if _, err := svc.GetSchema(schema.ID()); err == nil {
		logrus.WithField("schema_id", schema.ID()).Info("Overwriting existing schema")
	}
	return svc.PutSchema(schema)
}

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
		logrus.WithField("workflow_id", workflow.ID()).Info("Overwriting existing workflow")
	}
	return svc.PutWorkflow(workflow)
}

func loadModule(ctx context.Context, svc cabinet.Cabinet, fsys fs.FS, yamlPath string, yamlContent []byte, pkiAssets *PKIAssets, maxWasmModuleSize int64, logger *logrus.Logger) error {
	var file ModuleFile
	if err := yaml.Unmarshal(yamlContent, &file); err != nil {
		return fmt.Errorf("unmarshal module file: %w", err)
	}
	if file.WasmDigest == "" {
		return fmt.Errorf("wasmDigest is a required field for module %q", file.Metadata.Name+"@"+file.Metadata.Version)
	}
	if file.WasmSize == 0 {
		return fmt.Errorf("wasmSize is a required field for module %q", file.Metadata.Name+"@"+file.Metadata.Version)
	}
	wasmPath := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath)) + ".wasm"
	fileInfo, err := fs.Stat(fsys, wasmPath)
	if err != nil {
		return fmt.Errorf("stat WASM file %q: %w", wasmPath, err)
	}
	if fileInfo.Size() > maxWasmModuleSize {
		return fmt.Errorf("WASM module %q exceeds maximum allowed size of %d bytes (got %d)", wasmPath, file.WasmSize, fileInfo.Size())
	}
	if file.WasmSize != fileInfo.Size() {
		return fmt.Errorf("WASM size mismatch for %q (expected: %d, got: %d)", wasmPath, file.WasmSize, fileInfo.Size())
	}
	wasmCode, err := fs.ReadFile(fsys, wasmPath)
	if err != nil {
		return fmt.Errorf("read WASM file %q: %w", wasmPath, err)
	}
	hasher := sha256.New()
	hasher.Write(wasmCode)
	calculatedDigest := hex.EncodeToString(hasher.Sum(nil))
	if file.WasmDigest != calculatedDigest {
		return fmt.Errorf("WASM digest mismatch for %q (expected: %s, got: %s)", wasmPath, file.WasmDigest, calculatedDigest)
	}

	moduleID := file.Metadata.Name + "@" + file.Metadata.Version
	auditLogger := logger.WithFields(logrus.Fields{"module_id": moduleID, "checksum": calculatedDigest})

	sigPath := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath)) + ".pem"
	sigData, err := fs.ReadFile(fsys, sigPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			auditLogger.WithField("outcome", "skipped").Warn("No signature file found, module is unsigned.")
		} else {
			auditLogger.WithField("outcome", "failure").WithError(err).Error("Failed to read signature file.")
		}
	} else {
		block, _ := pem.Decode(sigData)
		if block == nil || block.Type != "CERTIFICATE" {
			auditLogger.WithField("outcome", "failure").Error("Invalid PEM format in signature file.")
		} else {
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				auditLogger.WithField("outcome", "failure").WithError(err).Error("Failed to parse certificate from signature file.")
			} else if !pkiAssets.TrustStoreLoaded {
				// Trust ecosystem not bootstrapped — skip verification, log for audit.
				auditLogger.WithFields(logrus.Fields{
					"outcome":        "skipped",
					"reason":         "trust ecosystem not bootstrapped",
					"signer_subject": cert.Subject.String(),
				}).Warn("Signature verification skipped: CA chain not available.")
			} else {
				// M3: full X.509 chain verification — EKU=CodeSigning, CRL + SPKI pinning included.
				verifyOpts := cabinet.VerifyOptions{
					Roots:         pkiAssets.Roots,
					Intermediates: pkiAssets.Intermediates,
					KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
					CRLs:          pkiAssets.CRLs,
				}
				chains, verifyErr := cabinet.VerifyCertificateChain(cert, verifyOpts)
				if verifyErr != nil {
					auditLogger.WithField("outcome", "rejected").WithError(verifyErr).Error("PKI chain verification failed — module rejected.")
					return fmt.Errorf("PKI verification failed for %q: %w", moduleID, verifyErr)
				}
				auditLogger.WithFields(logrus.Fields{
					"outcome":        "verified",
					"signer_subject": cert.Subject.String(),
					"chain_length":   len(chains[0]),
				}).Info("Certificate chain verified.")
			}
		}
	}

	// Corrected call to NewModuleDescriptor to include 'file.Accepts'
	moduleDesc, err := cabinet.NewModuleDescriptor(ctx, file.Metadata, file.Type, file.Accepts, file.OutputKeys, wasmCode)
	if err != nil {
		return fmt.Errorf("module validation: %w", err)
	}
	if _, err := svc.GetModule(moduleDesc.ID()); err == nil {
		logrus.WithField("module_id", moduleDesc.ID()).Info("Overwriting existing module")
	}
	return svc.PutModule(moduleDesc)
}

func loadPKIAssets(fsys fs.FS, path string, logger *logrus.Logger) (*PKIAssets, error) {
	assets := &PKIAssets{
		Roots:         x509.NewCertPool(),
		Intermediates: x509.NewCertPool(),
		CRLs:          []*x509.RevocationList{},
	}
	// chainHasher accumulates all loaded CA cert DER bytes for CertChainHash.
	chainHasher := sha256.New()

	// If path is empty, there are no PKI assets to load from a directory.
	// Return empty assets without trying to read a directory.
	// This check is primarily for embedded filesystems where an empty path might be valid,
	// but for os.DirFS("/"), an empty path can cause issues with fs.ReadDir.
	if path == "" {
		logger.Debug("PKI path is empty, skipping loading PKI assets from directory.")
		return assets, nil
	}

	files, err := fs.ReadDir(fsys, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			logger.WithField("path", path).Warn("PKI directory not found, skipping.")
			return assets, nil
		}
		return nil, fmt.Errorf("could not read PKI directory %s: %w", path, err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		content, err := fs.ReadFile(fsys, filepath.Join(path, file.Name()))
		if err != nil {
			logger.WithFields(logrus.Fields{"path": file.Name(), "error": err}).Warn("Could not read PKI file")
			continue
		}

		rest := content
		anyBlock := false
		for len(rest) > 0 {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			anyBlock = true
			switch block.Type {
			case "CERTIFICATE":
				cert, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					logger.WithFields(logrus.Fields{"path": file.Name(), "error": err}).Warn("Failed to parse certificate")
					continue
				}
				if cert.IsCA {
					chainHasher.Write(cert.Raw)
					if cert.Subject.String() == cert.Issuer.String() { // Self-signed, so it's a root
						assets.Roots.AddCert(cert)
						assets.TrustStoreLoaded = true
						if assets.CAFingerprint == "" {
							sum := sha256.Sum256(cert.Raw)
							assets.CAFingerprint = "sha256:" + hex.EncodeToString(sum[:])
						}
					} else {
						assets.Intermediates.AddCert(cert)
					}
				}
			case "X509 CRL":
				crl, err := x509.ParseRevocationList(block.Bytes)
				if err != nil {
					logger.WithFields(logrus.Fields{"path": file.Name(), "error": err}).Warn("Failed to parse CRL")
					continue
				}
				assets.CRLs = append(assets.CRLs, crl)
			}
		}
		if !anyBlock {
			logger.WithField("path", file.Name()).Warn("No PEM data found in file")
		}
	}
	if assets.TrustStoreLoaded {
		assets.CertChainHash = "sha256:" + hex.EncodeToString(chainHasher.Sum(nil))
	}
	logger.Info("Successfully loaded PKI assets")
	return assets, nil
}
