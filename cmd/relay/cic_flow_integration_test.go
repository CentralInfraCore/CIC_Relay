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
// passes PKI verification in loadModule.
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
  version: "1.0"
wasmDigest: %q
wasmSize: %d
%s`, name, hex.EncodeToString(sum[:]), len(wasmCode), extraYAML)

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
	return a
}

// A verified module that declares cic-flow imports + a matching manifest has its
// capability bound into the registry at load (the load → hook → register path).
func TestLoadModule_BindsCICFlowCapability(t *testing.T) {
	const manifest = `abi:
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
	moduleFS, yamlContent := moduleFSWith(t, "oci.mod", manifest)

	reg := newCICFlowRegistry(nil, &stubDoer{})
	registerModuleCapability = reg.registerVerified
	defer func() { registerModuleCapability = nil }()

	err := loadModule(context.Background(), newCabinetForTest(t), moduleFS, "m.yaml", yamlContent,
		loadedPKIWithSource(t), int64(16*1024*1024), newDiscardLogger())
	require.NoError(t, err)

	deps, ok := reg.lookup("oci.mod@1.0")
	require.True(t, ok, "a verified module's cic-flow capability must be bound")
	require.Len(t, deps.egress, 1)
	assert.Equal(t, "*.oraclecloud.com", deps.egress[0].Host)
}

// Without the CICSourceCA provenance anchor, a cic-flow module still loads but
// gets no capability — fail-closed (R3b).
func TestLoadModule_CICFlow_FailClosedWithoutSourceCA(t *testing.T) {
	const manifest = `abi:
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
	moduleFS, yamlContent := moduleFSWith(t, "oci.mod", manifest)

	reg := newCICFlowRegistry(nil, &stubDoer{})
	registerModuleCapability = reg.registerVerified
	defer func() { registerModuleCapability = nil }()

	// buildLoadedPKIAssets has no SourceRoots → provenance not verified.
	err := loadModule(context.Background(), newCabinetForTest(t), moduleFS, "m.yaml", yamlContent,
		buildLoadedPKIAssets(t), int64(16*1024*1024), newDiscardLogger())
	require.NoError(t, err) // module loads

	_, ok := reg.lookup("oci.mod@1.0")
	assert.False(t, ok, "no SourceCA → cic-flow capability must not be bound (fail-closed)")
}

// A verified module that over-declares its cic-flow imports (imports sign but
// declares no sign secret) is rejected at load — fail-closed.
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

	reg := newCICFlowRegistry(nil, &stubDoer{})
	registerModuleCapability = reg.registerVerified
	defer func() { registerModuleCapability = nil }()

	err := loadModule(context.Background(), newCabinetForTest(t), moduleFS, "m.yaml", yamlContent,
		loadedPKIWithSource(t), int64(16*1024*1024), newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capability binding failed")
	_, ok := reg.lookup("bad.mod@1.0")
	assert.False(t, ok)
}
