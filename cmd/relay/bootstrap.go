package main

import (
	"centralrelay/pkg/trustbundle"
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
	"sync"

	"centralrelay/core/cabinet"
	"centralrelay/core/modules/certselfsigned"
	"centralrelay/core/modules/cibuild"
	"centralrelay/core/modules/httpcap"
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
				CRLFreshness:  pkiAssets.CRLFreshness,
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
	compileWorkflow, err := cabinet.NewWorkflow(ctx,
		cabinet.WorkflowIdentity{Version: "1.0"},
		cabinet.WorkflowConfig{
			Name:        "cic.schema.compile",
			Description: "Schema compilation workflow: assert source, build, sign artifact.",
			Steps: []string{
				"cic.source.assert@1.0.assert(input) -> assert_result",
				"cic.schema.build@1.0.build(assert_result) -> artifact",
				"cic.artifact.sign@1.0.sign(artifact) -> signed_artifact",
			},
		},
		cabinet.WorkflowSigning{})
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
	certWorkflow, err := cabinet.NewWorkflow(ctx,
		cabinet.WorkflowIdentity{Version: "1.0"},
		cabinet.WorkflowConfig{
			Name:        "cert.selfsigned",
			Description: "Self-signed certificate generation workflow (dev level).",
			Steps:       []string{"cert.selfsigned@1.0.generate(input) -> cert"},
		},
		cabinet.WorkflowSigning{})
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
		{
			// Generic counterpart of the schema-specific release step: the caller
			// names the make target and the produced file instead of the pipeline
			// assuming source/<schema>.yaml.
			"cic.repo.build",
			pipelineCfg.NewRepoBuildFunc(),
			[]string{"raw_artifact", "artifact_path", "artifact_digest", "artifact_size", "step_hash"},
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
	pipelineWorkflow, err := cabinet.NewWorkflow(ctx,
		cabinet.WorkflowIdentity{Version: "1.0"},
		cabinet.WorkflowConfig{
			Name:        "cic.schemas.pipeline",
			Description: "Full CIC-Schemas build pipeline: clone, test, validate, release, assert, build, sign.",
			Steps: []string{
				"cic.pipeline.start@1.0.start(input) -> start_result",
				"cic.pipeline.test@1.0.test(start_result) -> test_result",
				"cic.pipeline.validate@1.0.validate(test_result) -> validate_result",
				"cic.pipeline.release@1.0.release(validate_result) -> release_result",
				"cic.source.assert@1.0.assert(release_result) -> assert_result",
				"cic.schema.build@1.0.build(assert_result) -> artifact",
				"cic.artifact.sign@1.0.sign(artifact) -> signed_artifact",
			},
		},
		cabinet.WorkflowSigning{})
	if err != nil {
		return fmt.Errorf("failed to create cic.schemas.pipeline workflow: %w", err)
	}
	if err := svc.PutWorkflow(pipelineWorkflow); err != nil {
		return fmt.Errorf("failed to register cic.schemas.pipeline workflow: %w", err)
	}

	// Generic repository CI: clone at a revision, run a make target in the builder
	// container, sign what it produced. No schema/version, no source/<schema>.yaml.
	//
	// cic.source.assert and cic.schema.build are deliberately absent: those exist
	// to validate and re-emit a developer-signed schema artifact, which a generic
	// repository does not have. cic.repo.build hands raw_artifact straight to the
	// signing step, and the provenance carried from the clone still reaches the
	// verification manifest.
	repoCIWorkflow, err := cabinet.NewWorkflow(ctx,
		cabinet.WorkflowIdentity{Version: "1.0"},
		cabinet.WorkflowConfig{
			Name:        "cic.repo.ci",
			Description: "Generic repository CI: clone, build in the builder container, sign the produced artifact.",
			Steps: []string{
				"cic.pipeline.start@1.0.start(input) -> start_result",
				"cic.repo.build@1.0.build(start_result) -> artifact",
				"cic.artifact.sign@1.0.sign(artifact) -> signed_artifact",
			},
		},
		cabinet.WorkflowSigning{})
	if err != nil {
		return fmt.Errorf("failed to create cic.repo.ci workflow: %w", err)
	}
	if err := svc.PutWorkflow(repoCIWorkflow); err != nil {
		return fmt.Errorf("failed to register cic.repo.ci workflow: %w", err)
	}

	logrus.Info("Successfully registered all native components.")

	// --- Register the http.request capability (feature-009) ---
	// Registered last and only when an actuator is wired in — newActuator() is nil
	// in the default build and FFI-backed under `ffi_cgo` (see actuator_stub.go /
	// actuator_cgo.go). registerHTTPCapability no-ops for a nil actuator, so the
	// default relay is unchanged. Returned directly so the default (nil) path has
	// no dead error branch.
	return registerHTTPCapability(ctx, svc, newActuator())
}

// registerHTTPCapability registers the `http@1.0` native capability backed by
// actuator. A nil actuator (a relay without the ffi_cgo bridge) is a no-op — the
// capability is simply absent. Kept separate from the build-tag split so it is
// fully testable in the default build with a mock actuator.
func registerHTTPCapability(ctx context.Context, svc cabinet.Cabinet, actuator httpcap.Actuator) error {
	if actuator == nil {
		return nil
	}
	httpMeta := cabinet.BaseBlock{
		Name:        "http",
		Version:     "1.0",
		Description: "The http.request capability: an authenticated HTTP actuation through the feature-008/009 trust-flow, driven by Setx and folded into the ProofTrace.",
	}
	httpModule, err := cabinet.NewModuleDescriptor(ctx, httpMeta, "native/http", nil,
		[]string{"status", "body_base64", "scope", "audit"}, nil)
	if err != nil {
		return fmt.Errorf("failed to create http module descriptor: %w", err)
	}
	httpModule.NativeImpl = cabinet.NativeFunctions{"request": httpcap.NewRequestFunc(actuator)}
	if err := svc.PutModule(httpModule); err != nil {
		return fmt.Errorf("failed to register http module: %w", err)
	}
	logrus.Info("Registered http.request capability (ffi bridge present).")
	return nil
}

// PKIAssets holds the loaded public key infrastructure components.
type PKIAssets struct {
	Roots            *x509.CertPool
	Intermediates    *x509.CertPool
	CRLs             []*x509.RevocationList
	CRLFreshness     *cabinet.CRLFreshness // set in prod: verification rejects a stale CRL
	TrustStoreLoaded bool                  // true when at least one root CA was loaded
	// AllowUnsignedModules admits a module that has NO signature file. It is set only in dev mode, only by the
	// explicit operator opt-in CIC_ALLOW_UNSIGNED_MODULES=1 (allowUnsignedModulesFromEnv), and never for a module
	// whose signature is present but unreadable, malformed, unverifiable or invalid: those are always refused.
	AllowUnsignedModules bool
	CAFingerprint        string // sha256 of the first loaded root CA DER bytes, "sha256:<hex>"
	CertChainHash        string // sha256 of all loaded CA cert DER bytes concatenated, "sha256:<hex>"

	// R3b (feature-011): the cic-flow provenance/entitlement anchors, delivered by
	// their own detached-companion path — NOT embedded, so the relay binary's hash
	// stays stable (r3-plan.md). SourceRoots = CICSourceCA (build/source
	// provenance), loaded from a companion beside the binary; LicenseRoots =
	// CICLicenseCA (entitlement), from the license.
	SourceRoots   *x509.CertPool
	SourceLoaded  bool
	LicenseRoots  *x509.CertPool
	LicenseLoaded bool

	// #162 V2b: the operator's pinned authority bindings for module signing. A key ID (sha256 of the signer's DER
	// SubjectPublicKeyInfo) is authorised to sign module release envelopes only if it is listed here with the
	// module:sign role (loadModuleSignAuthorities); a key merely present in a verified certificate is not. Nil or
	// empty: no envelope is admitted (SIGNER_NOT_AUTHORIZED).
	ModuleSignAuthorities map[string]bool
	// AcceptedProfiles are the ABI-profile pins (id, digest) of the admitted trust bundle (INT-12). Nil when no bundle is
	// configured. Consuming them for profile admission is INT-05.
	AcceptedProfiles []trustbundle.Profile
	// ProfileSignAuthorities holds, per signer key_id, whether the bundle grants profile:sign (INT-05). Nil: no profile
	// signature is accepted.
	ProfileSignAuthorities map[string]bool
	// TrustMode is the CIC_TRUST_MODE the assets were bootstrapped for (bootstrapTrust). Anything other than
	// TrustModeDev, the empty value included, is treated as prod by the module admission (V6): fail-closed.
	TrustMode string
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
	Identity cabinet.WorkflowIdentity `yaml:"identity"`
	Config   cabinet.WorkflowConfig   `yaml:"config"`
	Signing  cabinet.WorkflowSigning  `yaml:"signing"`
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

// loadEmbeddedComponents loads schemas, workflows and modules from the embedded filesystems and
// registers them with the cabinet service.
//
// Embedded components are verified against the SAME trust context as installed ones
// (pkiAssets, the result of bootstrapTrust): being compiled into the binary grants no extra
// trust. Origin (the signer chain against the configured anchors), authenticity and the
// capability a module receives are decided by the same rules on both load paths. The loader
// used to build a second trust store from the embedded CAs, so in prod an embedded module was
// judged against the development CAs instead of the operator's bundle.
func loadEmbeddedComponents(ctx context.Context, svc cabinet.Cabinet, schemas fs.FS, workflows fs.FS, pkiAssets *PKIAssets, maxWasmModuleSize int64, logger *logrus.Logger) error {
	if pkiAssets == nil {
		return fmt.Errorf("loadEmbeddedComponents: the shared trust context (pkiAssets) is required")
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

// loadSchema unmarshals a Schema from YAML content and registers it with the cabinet service.
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

// loadWorkflow unmarshals a Workflow from YAML content and registers it with the cabinet service.
func loadWorkflow(ctx context.Context, svc cabinet.Cabinet, content []byte) error {
	var file WorkflowFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return fmt.Errorf("unmarshal workflow file: %w", err)
	}
	workflow, err := cabinet.NewWorkflow(ctx, file.Identity, file.Config, file.Signing)
	if err != nil {
		return fmt.Errorf("workflow validation: %w", err)
	}
	if _, err := svc.GetWorkflow(workflow.ID()); err == nil {
		logrus.WithField("workflow_id", workflow.ID()).Info("Overwriting existing workflow")
	}
	return svc.PutWorkflow(workflow)
}

// loadModule unmarshals a Module from YAML content, verifies its WASM digest, validates its PKI signature,
// and registers it with the cabinet service.
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
	// The ONE read of the wasm (#162 V4, AC-162-14): this buffer is hashed, verified against the envelope and the
	// manifest, and is the only input of the module descriptor that gets stored and compiled. Nothing re-reads it.
	wasmCode, err := fs.ReadFile(fsys, wasmPath)
	if err != nil {
		return fmt.Errorf("read WASM file %q: %w", wasmPath, err)
	}
	if int64(len(wasmCode)) != file.WasmSize {
		return fmt.Errorf("WASM size mismatch for %q (expected: %d, read: %d)", wasmPath, file.WasmSize, len(wasmCode))
	}
	hasher := sha256.New()
	hasher.Write(wasmCode)
	calculatedDigest := hex.EncodeToString(hasher.Sum(nil))
	if file.WasmDigest != calculatedDigest {
		return reject(codeWasmDigestMismatch, "WASM digest mismatch for %q (expected: %s, got: %s)", wasmPath, file.WasmDigest, calculatedDigest)
	}

	moduleID := file.Metadata.Name + "@" + file.Metadata.Version

	// Loads of modules are serialised. Revoking the earlier grant, verifying, publishing the new grant and
	// storing the artifact are one unit: two loads of the same module ID can not interleave, so the grant
	// that ends up in the registry and the artifact that ends up in the cabinet always come from the same
	// load.
	moduleLoadMu.Lock()
	defer moduleLoadMu.Unlock()
	if revokeModuleCapability != nil {
		revokeModuleCapability(moduleID)
	}
	var moduleDesc *cabinet.ModuleDescriptor
	var profileBinding *cabinet.ModuleProfileBinding
	var trustStateOfLoad, envelopeDigestOfLoad string
	auditLogger := logger.WithFields(logrus.Fields{"module_id": moduleID, "checksum": calculatedDigest})

	sigPath := strings.TrimSuffix(yamlPath, filepath.Ext(yamlPath)) + ".pem"
	sigData, err := fs.ReadFile(fsys, sigPath)
	switch {
	case err == nil:
		// A signature is present: it MUST verify. Nothing below is bypassed by any opt-in.
		block, _ := pem.Decode(sigData)
		if block == nil || block.Type != "CERTIFICATE" {
			auditLogger.WithField("outcome", "rejected").Error("Invalid PEM format in signature file — module rejected.")
			return fmt.Errorf("module %q: the signature file is not a PEM certificate", moduleID)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			auditLogger.WithField("outcome", "rejected").WithError(err).Error("Failed to parse certificate from signature file — module rejected.")
			return fmt.Errorf("module %q: the signature certificate cannot be parsed: %w", moduleID, err)
		}
		if !pkiAssets.TrustStoreLoaded {
			// A signature that cannot be verified is not a signature.
			auditLogger.WithFields(logrus.Fields{
				"outcome":        "rejected",
				"reason":         "trust ecosystem not bootstrapped",
				"signer_subject": cert.Subject.String(),
			}).Error("Signature cannot be verified: no CA chain is loaded — module rejected.")
			return fmt.Errorf("module %q is signed but no trust store is loaded to verify it", moduleID)
		}
		// M3: full X.509 chain verification — EKU=CodeSigning, CRL + SPKI pinning included.
		verifyOpts := cabinet.VerifyOptions{
			Roots:         pkiAssets.Roots,
			Intermediates: pkiAssets.Intermediates,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
			CRLs:          pkiAssets.CRLs,
			CRLFreshness:  pkiAssets.CRLFreshness,
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

		// #162: the module artifact admission. The descriptor is built ONCE, from the buffer hashed above; its
		// WasmCode is what the cabinet stores and compiles, so it is the executed identity the envelope must name.
		if moduleDesc, err = cabinet.NewModuleDescriptor(ctx, file.Metadata, file.Type, file.Accepts, file.OutputKeys, wasmCode); err != nil {
			return fmt.Errorf("module validation: %w", err)
		}
		trustState, envelopeDigest, admErr := admitModuleArtifact(fsys, yamlPath, moduleID, calculatedDigest, yamlContent, wasmCode, moduleDesc.WasmCode, cert, pkiAssets, auditLogger)
		if admErr != nil {
			auditLogger.WithFields(logrus.Fields{"outcome": "rejected", "code": admissionCode(admErr)}).WithError(admErr).Error("Module artifact admission failed — module rejected.")
			return fmt.Errorf("module %q rejected: %w", moduleID, admErr)
		}
		trustStateOfLoad, envelopeDigestOfLoad = trustState, envelopeDigest
		auditLogger = auditLogger.WithFields(logrus.Fields{"trust_state": trustState, "envelope_digest": envelopeDigest})
		// INT-05: a module that names an ABI profile is admitted only with an authenticated profile binding, before any
		// capability is published. A failure rejects the module; there is no fallback to legacy dispatch.
		project, perr := parseModuleProject(yamlContent)
		if perr != nil {
			return fmt.Errorf("module %q rejected: %w", moduleID, reject(codeEnvelopeInvalid, "%v", perr))
		}
		if profileBinding, err = admitModuleProfile(fsys, yamlPath, project, moduleDesc, envelopeDigest, pkiAssets); err != nil {
			auditLogger.WithFields(logrus.Fields{"outcome": "rejected", "code": admissionCode(err)}).WithError(err).Error("ABI profile admission failed — module rejected.")
			return fmt.Errorf("module %q rejected: %w", moduleID, err)
		}
		// R3a/R3b + #162 V7: a capability is bound only to an authenticated module, and under its authenticated
		// identity (module_id, envelope_digest). A verified module that over-declares is rejected.
		if trustState == trustAuthenticated && registerModuleCapability != nil {
			if err := registerModuleCapability(moduleID, yamlContent, moduleDesc.Checksum(), envelopeDigest); err != nil {
				auditLogger.WithField("outcome", "rejected").WithError(err).Error("Capability binding failed — module rejected.")
				return fmt.Errorf("capability binding failed for %q: %w", moduleID, err)
			}
		}
		auditLogger.WithField("outcome", "admitted").Info("Module admitted.")
	case errors.Is(err, fs.ErrNotExist):
		// No signer certificate. The dev opt-in covers a module WITHOUT any signature material only: a module that
		// carries an envelope or a .sig but no certificate is incomplete, not unsigned.
		if present, statErr := signatureMaterialPresent(fsys, yamlPath); statErr != nil || present {
			auditLogger.WithFields(logrus.Fields{"outcome": "rejected", "code": codeModuleRejected}).Error("Envelope or signature without a signer certificate — module rejected.")
			return fmt.Errorf("module %q rejected: %w", moduleID, reject(codeModuleRejected, "envelope or signature present without a signer certificate (%s)", filepath.Base(sigPath)))
		}
		// No signature file. Refused unless the operator explicitly opted in (dev mode only).
		if !pkiAssets.AllowUnsignedModules {
			auditLogger.WithField("outcome", "rejected").Error("No signature file: unsigned modules are refused.")
			return fmt.Errorf("module %q is unsigned (no %s); unsigned modules are refused (dev mode only: %s=1)", moduleID, filepath.Base(sigPath), allowUnsignedEnv)
		}
		auditLogger.WithFields(logrus.Fields{"outcome": "allowed-unsigned", "trust_state": trustDev, "reason": "explicit operator opt-in " + allowUnsignedEnv + "=1 (dev)"}).
			Warn("Unsigned module admitted by explicit opt-in: it gets no capability.")
	default:
		// The signature file exists but cannot be read: that is not "unsigned", it is a failure.
		auditLogger.WithField("outcome", "rejected").WithError(err).Error("Failed to read signature file — module rejected.")
		return fmt.Errorf("module %q: the signature file cannot be read: %w", moduleID, err)
	}

	// Corrected call to NewModuleDescriptor to include 'file.Accepts'
	if afterCapabilityPublished != nil {
		afterCapabilityPublished(moduleID)
	}
	if moduleDesc == nil {
		moduleDesc, err = cabinet.NewModuleDescriptor(ctx, file.Metadata, file.Type, file.Accepts, file.OutputKeys, wasmCode)
		if err != nil {
			return fmt.Errorf("module validation: %w", err)
		}
	}
	if _, err := svc.GetModule(moduleDesc.ID()); err == nil {
		logrus.WithField("module_id", moduleDesc.ID()).Info("Overwriting existing module")
	}
	if err := svc.PutModule(moduleDesc); err != nil {
		// Not stored: a grant published for this artifact must not outlive the failed store.
		if revokeModuleCapability != nil {
			revokeModuleCapability(moduleID)
		}
		return err
	}
	// The profile binding (nil for a module without a profile: this also clears one from an earlier load).
	if reg, ok := svc.(interface {
		SetModuleProfile(string, *cabinet.ModuleProfileBinding)
	}); ok {
		reg.SetModuleProfile(moduleDesc.ID(), profileBinding)
	}
	// INT-09: the admission record the V2 trace claims as the executed identity: only an authenticated module with an
	// admitted profile binding has one, and a reload without it drops the earlier record.
	if profileBinding != nil && trustStateOfLoad == trustAuthenticated {
		moduleIdentities.set(moduleDesc.ID(), moduleDesc.Checksum(), executedIdentityOf(calculatedDigest, moduleDesc, yamlContent, envelopeDigestOfLoad, profileBinding))
	} else {
		moduleIdentities.forget(moduleDesc.ID())
	}
	// Stored: the staged grant becomes active, for exactly this artifact.
	if commitModuleCapability != nil {
		commitModuleCapability(moduleID, moduleDesc.Checksum())
	}
	return nil
}

// moduleLoadMu serialises loadModule (see there).
var moduleLoadMu sync.Mutex

// afterCapabilityPublished is a test seam: called inside loadModule after the capability grant was
// published and before the artifact is stored, i.e. at the one point where an interleaving could hurt.
// Always nil in the relay.
var afterCapabilityPublished func(moduleID string)

// loadPKIAssets loads X.509 certificates and CRLs from a filesystem path and populates PKIAssets
// with root certificates, intermediates, and revocation lists.
//
// This is the lenient (dev) loader: every CRL still needs a valid signature from a CA
// certificate loaded here, and one that fails is never used, but an unparsable or stale CRL
// is only logged. The strict (prod) path is the CA bundle in bootstrapPKI.
func loadPKIAssets(fsys fs.FS, path string, logger *logrus.Logger) (*PKIAssets, error) {
	policy := crlPolicy{}
	assets := &PKIAssets{
		Roots:         x509.NewCertPool(),
		Intermediates: x509.NewCertPool(),
		CRLs:          []*x509.RevocationList{},
	}
	// chainHasher accumulates all loaded CA cert DER bytes for CertChainHash.
	chainHasher := sha256.New()
	// CRLs are verified after the whole directory is read: a CRL may precede its issuer.
	var caCerts []*x509.Certificate
	var candidateCRLs []*x509.RevocationList

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
					caCerts = append(caCerts, cert)
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
				candidateCRLs = append(candidateCRLs, crl)
			}
		}
		if !anyBlock {
			logger.WithField("path", file.Name()).Warn("No PEM data found in file")
		}
	}
	crls, err := acceptCRLs(candidateCRLs, caCerts, policy, logger)
	if err != nil {
		return nil, err
	}
	assets.CRLs = crls
	assets.CRLFreshness = policy.Freshness
	if assets.TrustStoreLoaded {
		assets.CertChainHash = "sha256:" + hex.EncodeToString(chainHasher.Sum(nil))
	}
	logger.Info("Successfully loaded PKI assets")
	return assets, nil
}
