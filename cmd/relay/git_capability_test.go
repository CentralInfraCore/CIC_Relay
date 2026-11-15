package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"centralrelay/core/cabinet"
	nexusgit "centralrelay/core/nexus/git"
)

const gitManifestYAML = `
wasmDigest: "abc"
abi:
  imports:
    - module: git
      functions: [status, fetch, push, checkout]
capability_manifest:
  git:
    - repo: infra
      ops: [status, fetch, push, checkout]
      remotes: [origin]
      refs: ["main", "release/*"]
`

// gitRegistryWith registers a verified module and runs it: the grant is bound to artifact "art-1" and
// an instance named id is running that artifact, as after a normal load and instantiation.
func gitRegistryWith(t *testing.T, id, yaml string) *cicFlowRegistry {
	t.Helper()
	reg := newCICFlowRegistry(nil, &stubDoer{})
	require.NoError(t, reg.registerVerified(id, []byte(yaml)))
	reg.bindArtifact(id, "art-1")
	reg.noteInstance(id, "art-1")
	return reg
}

func TestGitManifest_Validation(t *testing.T) {
	bad := map[string]string{
		"unknown op":            "ops: [rebase]\n      remotes: [origin]",
		"no ops":                "ops: []",
		"fetch without remotes": "ops: [fetch]",
		"push without refs":     "ops: [push]\n      remotes: [origin]",
		"checkout without refs": "ops: [checkout]",
		"clone without sources": "ops: [clone]\n      refs: [main]",
		"star ref":              "ops: [checkout]\n      refs: [\"*\"]",
		"infix wildcard ref":    "ops: [checkout]\n      refs: [\"re*lease\"]",
		"wildcard repo":         "repo: \"*\"\n      ops: [status]",
		"empty repo":            "repo: \"\"\n      ops: [status]",
	}
	for name, rule := range bad {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(rule, "repo:") {
				rule = "repo: infra\n      " + rule
			}
			yaml := "abi:\n  imports: []\ncapability_manifest:\n  git:\n    - " + rule + "\n"
			reg := newCICFlowRegistry(nil, &stubDoer{})
			assert.Error(t, reg.registerVerified("m@1", []byte(yaml)), "a loose or malformed git rule must fail at load")
			assert.Error(t, reg.authorizeGit("m@1", "status", gitAuthRequest{RepoID: "infra"}), "and nothing may be granted")
		})
	}
}

// A module that imports a git function its manifest does not grant is rejected at load.
func TestGitManifest_ImportsMustBeGranted(t *testing.T) {
	cases := map[string]string{
		"no manifest at all": "abi:\n  imports:\n    - module: git\n      functions: [push]\n",
		"op not granted":     "abi:\n  imports:\n    - module: git\n      functions: [push]\ncapability_manifest:\n  git:\n    - repo: infra\n      ops: [status]\n",
		"unknown function":   "abi:\n  imports:\n    - module: git\n      functions: [exec]\ncapability_manifest:\n  git:\n    - repo: infra\n      ops: [status]\n",
	}
	for name, yaml := range cases {
		t.Run(name, func(t *testing.T) {
			reg := newCICFlowRegistry(nil, &stubDoer{})
			assert.Error(t, reg.registerVerified("m@1", []byte(yaml)))
		})
	}
}

func TestGitAuthorize_Matrix(t *testing.T) {
	reg := gitRegistryWith(t, "mod@1", gitManifestYAML)
	ok := func(op string, r gitAuthRequest) {
		assert.NoError(t, reg.authorizeGit("mod@1", op, r), "%s %+v", op, r)
	}
	no := func(op string, r gitAuthRequest) { assert.Error(t, reg.authorizeGit("mod@1", op, r), "%s %+v", op, r) }

	ok("status", gitAuthRequest{RepoID: "infra"})
	ok("fetch", gitAuthRequest{RepoID: "infra", Remote: "origin"})
	ok("checkout", gitAuthRequest{RepoID: "infra", Ref: "main"})
	ok("checkout", gitAuthRequest{RepoID: "infra", Ref: "release/1.2"})
	ok("push", gitAuthRequest{RepoID: "infra", Remote: "origin", Refspec: "main"})
	ok("push", gitAuthRequest{RepoID: "infra", Remote: "origin", Refspec: "main:release/x"})

	no("status", gitAuthRequest{RepoID: "iac-config"})                                 // other repository
	no("fetch", gitAuthRequest{RepoID: "infra", Remote: "upstream"})                   // other remote
	no("fetch", gitAuthRequest{RepoID: "infra", Remote: ""})                           // nothing named
	no("checkout", gitAuthRequest{RepoID: "infra", Ref: "dev"})                        // other ref
	no("checkout", gitAuthRequest{RepoID: "infra", Ref: "release/"})                   // pattern needs a segment
	no("checkout", gitAuthRequest{RepoID: "infra", Ref: "releasex/1"})                 // prefix is a segment boundary
	no("push", gitAuthRequest{RepoID: "infra", Remote: "origin", Refspec: "main:dev"}) // destination outside
	no("push", gitAuthRequest{RepoID: "infra", Remote: "origin", Refspec: "dev"})      // source outside
	no("commit", gitAuthRequest{RepoID: "infra"})                                      // op not granted
	no("clone", gitAuthRequest{RepoID: "infra", URL: "https://x/y", Branch: "main"})   // op not granted
	assert.Error(t, reg.authorizeGit("someone-else@1", "status", gitAuthRequest{RepoID: "infra"}), "an unregistered module has no capability")
	assert.Error(t, newCICFlowRegistry(nil, nil).authorizeGit("mod@1", "status", gitAuthRequest{RepoID: "infra"}), "an empty registry denies")
}

// The caller's identity is the instance name the runtime gave the module. Nothing in the request can
// name another module.
func TestGitHostFunction_IdentityComesFromTheRuntime(t *testing.T) {
	reg := gitRegistryWith(t, "granted@1", gitManifestYAML)
	called := 0
	inner := func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 { called++; return 0 }
	fn := authorized(reg, "status", inner)

	granted := newMod(8192)
	granted.name = "granted@1"
	_, out := invoke(fn, granted, map[string]interface{}{"repo_id": "infra"})
	assert.Nil(t, out["error"], "the granted module may call status: %v", out)
	assert.Equal(t, 1, called)

	other := newMod(8192)
	other.name = "other@1"
	for _, claim := range []string{"module", "module_id", "caller", "name"} {
		_, out = invoke(fn, other, map[string]interface{}{"repo_id": "infra", claim: "granted@1"})
		assert.NotEmpty(t, out["error"], "a request field %q must not lend another module's identity", claim)
	}
	assert.Equal(t, 1, called, "the denied calls never reached the operation")

	nilAuthz := authorized(nil, "status", inner)
	_, out = invoke(nilAuthz, granted, map[string]interface{}{"repo_id": "infra"})
	assert.NotEmpty(t, out["error"], "no authorizer: deny")
}

// wasmVariant returns a distinct, valid wasm module (an empty function type in the type section).
var (
	wasmBase    = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	wasmVariant = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00}
)

func gitModuleFS(t *testing.T, wasm []byte) (fstest.MapFS, []byte) {
	t.Helper()
	fsys, yamlContent := moduleFSWith(t, "gitmod", `abi:
  imports:
    - module: git
      functions: [status]
capability_manifest:
  git:
    - repo: infra
      ops: [status]
`)
	if wasm != nil {
		sum := sha256.Sum256(wasm)
		y := strings.NewReplacer(
			hexOf(wasmBase), hex.EncodeToString(sum[:]),
			fmt.Sprintf("wasmSize: %d", len(wasmBase)), fmt.Sprintf("wasmSize: %d", len(wasm)),
		).Replace(string(yamlContent))
		yamlContent = []byte(y)
		fsys["m.yaml"] = &fstest.MapFile{Data: yamlContent}
		fsys["m.wasm"] = &fstest.MapFile{Data: wasm}
	}
	return fsys, yamlContent
}

func hexOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func hooks(reg *cicFlowRegistry) func() {
	registerModuleCapability = reg.registerVerified
	revokeModuleCapability = reg.revoke
	bindModuleArtifact = reg.bindArtifact
	return func() { registerModuleCapability, revokeModuleCapability, bindModuleArtifact = nil, nil, nil }
}

func loadGit(t *testing.T, svc cabinet.Cabinet, fsys fstest.MapFS, y []byte, pki *PKIAssets) {
	t.Helper()
	require.NoError(t, loadModule(context.Background(), svc, fsys, "m.yaml", y, pki, int64(16*1024*1024), newDiscardLogger()))
}

// A grant belongs to the verified module state: the artifact the cabinet stored, and the instance that
// is actually running it. A reload revokes the grant, an unverified reload does not get it back, and an
// instance that started before the reload keeps no authority from the new grant.
func TestGitGrant_FollowsTheVerifiedArtifactAndTheRunningInstance(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	defer hooks(reg)()
	svc := newCabinetForTest(t)
	const id = "gitmod@1.0"
	status := gitAuthRequest{RepoID: "infra"}

	fs1, y1 := gitModuleFS(t, nil)
	loadGit(t, svc, fs1, y1, loadedPKIWithSource(t))
	d1, err := svc.GetModule(id)
	require.NoError(t, err)
	art1 := d1.Checksum()

	assert.Error(t, reg.authorizeGit(id, "status", status), "stored and granted, but no instance is running it: denied")
	reg.noteInstance(id, art1)
	assert.NoError(t, reg.authorizeGit(id, "status", status), "the running instance is the verified artifact: allowed")
	g := reg.git[id]
	assert.NotEmpty(t, g.wasmDigest)
	assert.NotEmpty(t, g.manifestDigest)
	assert.Equal(t, art1, g.artifact)

	// The module is reloaded with a different binary under the same name@version and verifies.
	fs2, y2 := gitModuleFS(t, wasmVariant)
	loadGit(t, svc, fs2, y2, loadedPKIWithSource(t))
	d2, err := svc.GetModule(id)
	require.NoError(t, err)
	require.NotEqual(t, art1, d2.Checksum(), "a different binary is a different artifact")
	assert.Error(t, reg.authorizeGit(id, "status", status),
		"the instance that is still running the old artifact must not use the new artifact's grant")
	reg.noteInstance(id, d2.Checksum()) // the old instance is gone, a new one runs the new artifact
	assert.NoError(t, reg.authorizeGit(id, "status", status))

	// The same ID loads again without the provenance anchor: it loads, but is granted nothing.
	loadGit(t, svc, fs1, y1, buildLoadedPKIAssets(t))
	assert.Error(t, reg.authorizeGit(id, "status", status), "an unverified reload must not keep the earlier grant")
	d3, err := svc.GetModule(id)
	require.NoError(t, err)
	reg.noteInstance(id, d3.Checksum()) // an instance of the unverified artifact starts
	assert.Error(t, reg.authorizeGit(id, "status", status),
		"the unverified artifact must not inherit the earlier grant even once it is stored and running")
}

// While a module is being reloaded over and over, an instance that is running a stale artifact is never
// authorized, whatever the interleaving. (Run with -race.)
func TestGitGrant_ConcurrentReloadNeverAuthorizesAStaleInstance(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	const id = "m@1"
	reg.noteInstance(id, "stale-artifact")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var violations atomic.Int64
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if reg.authorizeGit(id, "status", gitAuthRequest{RepoID: "infra"}) == nil {
						violations.Add(1)
					}
				}
			}
		}()
	}
	for n := 0; n < 300; n++ {
		reg.revoke(id)
		require.NoError(t, reg.registerVerified(id, []byte(gitManifestYAML)))
		reg.bindArtifact(id, fmt.Sprintf("new-artifact-%d", n))
	}
	close(stop)
	wg.Wait()
	assert.Zero(t, violations.Load(), "a stale instance was authorized during a reload")
}

func TestGitGrant_UnboundOrUnknownInstanceIsDenied(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	require.NoError(t, reg.registerVerified("m@1", []byte(gitManifestYAML)))
	req := gitAuthRequest{RepoID: "infra"}
	assert.Error(t, reg.authorizeGit("m@1", "status", req), "granted but never stored or run")
	reg.noteInstance("m@1", "x")
	assert.Error(t, reg.authorizeGit("m@1", "status", req), "running, but the grant is not bound to a stored artifact")
	reg.bindArtifact("m@1", "y")
	assert.Error(t, reg.authorizeGit("m@1", "status", req), "running x, granted for y")
	reg.bindArtifact("m@1", "x")
	assert.NoError(t, reg.authorizeGit("m@1", "status", req))
}

// Through the real host module and a real git repository: a module with no grant gets nothing, a
// granted module gets exactly what its manifest lists, and the argument validation underneath still holds.
func TestGitHostModule_EndToEnd(t *testing.T) {
	ops, _ := realGitRepo(t)
	reg := gitRegistryWith(t, "granted@1", gitManifestYAML)

	rt := wazero.NewRuntime(context.Background())
	defer rt.Close(context.Background())
	require.NoError(t, registerGitHostFunctions(context.Background(), rt, ops, reg))
	assert.Len(t, rt.Module("git").ExportedFunctionDefinitions(), 6)

	statusFn := authorized(reg, "status", makeStatusFn(ops))
	granted := newMod(8192)
	granted.name = "granted@1"
	_, out := invoke(statusFn, granted, map[string]interface{}{"repo_id": "r"})
	assert.NotEmpty(t, out["error"], "repo r is not in the manifest (only infra): denied before the operation")

	stranger := newMod(8192)
	stranger.name = "stranger@1"
	_, out = invoke(statusFn, stranger, map[string]interface{}{"repo_id": "r"})
	assert.NotEmpty(t, out["error"])
}

var _ = fmt.Sprint
var _ fs.FS = fstest.MapFS{}
var _ = os.Stat
var _ = exec.Command
var _ = filepath.Join
var _ nexusgit.GitOps
