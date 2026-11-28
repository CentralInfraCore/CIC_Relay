package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/core/cabinet"
)

// moduleFSWith builds a single-module fstest.MapFS whose project.yaml carries the
// given abi/capability_manifest body, signed with the valid module ee cert so it
// passes PKI verification in loadModule. The module ID is <name>@1.0.0 and the
// manifest carries metadata.buildHash; addEnvelope makes it an authenticated release.
func moduleFSWith(t *testing.T, name, extraYAML string) (fstest.MapFS, []byte) {
	t.Helper()
	eeCert, err := fs.ReadFile(getSubFS(t, "pki/signers/sources/module"), "ee.pem")
	require.NoError(t, err)

	wasmCode := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	sum := sha256.Sum256(wasmCode)
	yaml := fmt.Sprintf(`apiVersion: relay.cic.com/v1
kind: Module
metadata:
  name: %s
  version: "1.0.0"
  buildHash: %s
wasmDigest: %q
wasmSize: %d
%s`, name, hex.EncodeToString(sum[:]), hex.EncodeToString(sum[:]), len(wasmCode), extraYAML)

	m := fstest.MapFS{
		"m.yaml": {Data: []byte(yaml)},
		"m.wasm": {Data: wasmCode},
		"m.pem":  {Data: eeCert},
	}
	return m, []byte(yaml)
}

func newCabinetForTest(t *testing.T) cabinet.Cabinet {
	t.Helper()
	svc, err := cabinet.NewCabinetService(context.Background(),
		cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(),
		cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1})
	require.NoError(t, err)
	return svc
}

// loadedPKIWithSource returns a loaded trust store whose CICSourceCA provenance
// anchor is the same pool (the test module ee chains to it), so verifyCICFlowProvenance passes.
func loadedPKIWithSource(t *testing.T) *PKIAssets {
	a := buildLoadedPKIAssets(t)
	a.SourceRoots = a.Roots
	a.SourceLoaded = true
	return testModuleSigner(t).authorize(a) // the test module signer holds module:sign (#162 V2b)
}

const ociFlowManifest = `abi:
  imports:
    - module: cic-flow
      functions: [sign, actuate]
capability_manifest:
  egress:
    - host: "*.oraclecloud.com"
  secrets:
    - handle: "oci/*"
      ops: [sign]
`

// An authenticated module (#162: envelope, authorised signer, digests, provenance) that declares cic-flow imports
// and a matching manifest has its capability bound at load under (module_id, envelope_digest), and the grant becomes
// active once the module is stored.
func TestLoadModule_BindsCICFlowCapability(t *testing.T) {
	moduleFS, yamlContent := moduleFSWith(t, "oci.mod", ociFlowManifest)
	signer := testModuleSigner(t)
	envDigest := addEnvelope(t, moduleFS, "m.yaml", signer, nil)

	reg := newCICFlowRegistry(nil, &stubDoer{})
	defer hooks(reg)()

	err := loadModule(context.Background(), newCabinetForTest(t), moduleFS, "m.yaml", yamlContent,
		signer.authorize(loadedPKIWithSource(t)), int64(16*1024*1024), newDiscardLogger())
	require.NoError(t, err)

	deps, ok := reg.lookup("oci.mod@1.0.0")
	require.True(t, ok, "an authenticated module's cic-flow capability must be bound")
	require.Len(t, deps.egress, 1)
	assert.Equal(t, "*.oraclecloud.com", deps.egress[0].Host)
	assert.Equal(t, envDigest, reg.bindingFor("oci.mod@1.0.0"), "the capability is bound to the envelope identity")
	assert.True(t, reg.deps["oci.mod@1.0.0"].active)
}

// Without the CICSourceCA provenance anchor an enveloped cic-flow module is not authenticated. A verified envelope
// is an authenticated attempt: when its provenance then fails, the module is REJECTED (PROVENANCE_UNVERIFIED) in dev
// and in prod alike, not downgraded to transitional (D-1; review F2 of #233). The envelope-less transitional path is
// a different case (TestAdmission_AC07_MissingSignatureInProd).
func TestLoadModule_CICFlow_FailClosedWithoutSourceCA(t *testing.T) {
	for _, mode := range []string{TrustModeDev, TrustModeProd} {
		t.Run(mode, func(t *testing.T) {
			moduleFS, yamlContent := moduleFSWith(t, "oci.mod", ociFlowManifest)
			signer := testModuleSigner(t)
			addEnvelope(t, moduleFS, "m.yaml", signer, nil)

			reg := newCICFlowRegistry(nil, &stubDoer{})
			defer hooks(reg)()

			// buildLoadedPKIAssets has no SourceRoots → provenance not verified.
			assets := signer.authorize(buildLoadedPKIAssets(t))
			assets.TrustMode = mode
			err := loadModule(context.Background(), newCabinetForTest(t), moduleFS, "m.yaml", yamlContent,
				assets, int64(16*1024*1024), newDiscardLogger())
			require.Error(t, err)
			assert.Equal(t, codeProvenanceUnverified, admissionCode(err), err.Error())
			_, ok := reg.lookup("oci.mod@1.0.0")
			assert.False(t, ok, "no SourceCA → cic-flow capability must not be bound (fail-closed)")
		})
	}
}

// An authenticated module that over-declares its cic-flow imports (imports sign but declares no sign secret) is
// rejected at load — fail-closed.
func TestLoadModule_RejectsOverDeclaredCICFlow(t *testing.T) {
	const overDeclared = `abi:
  imports:
    - module: cic-flow
      functions: [sign]
capability_manifest:
  egress:
    - host: "*.oraclecloud.com"
`
	moduleFS, yamlContent := moduleFSWith(t, "bad.mod", overDeclared)
	signer := testModuleSigner(t)
	addEnvelope(t, moduleFS, "m.yaml", signer, nil)

	reg := newCICFlowRegistry(nil, &stubDoer{})
	defer hooks(reg)()

	err := loadModule(context.Background(), newCabinetForTest(t), moduleFS, "m.yaml", yamlContent,
		signer.authorize(loadedPKIWithSource(t)), int64(16*1024*1024), newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capability binding failed")
	_, ok := reg.lookup("bad.mod@1.0.0")
	assert.False(t, ok)
}
