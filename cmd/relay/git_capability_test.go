package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
      functions: [status, fetch, checkout]
capability_manifest:
  git:
    - repo: infra
      ops: [status, fetch, checkout]
      remotes: [origin]
      refs: ["main", "release/*"]
`

// gitRegistryWith registers a verified module and runs it: the grant is bound to artifact "art-1" and
// an instance named id is running that artifact, as after a normal load and instantiation.
// inst is the identity the WasmManager would attach to an instance of module name built from artifact.
func inst(name, artifact string) cabinet.InstanceIdentity {
	return instUnder(name, artifact, gitManifestYAML)
}

// instUnder is inst for an instance that started while the manifest document manifestYAML was in force.
func instUnder(name, artifact, manifestYAML string) cabinet.InstanceIdentity {
	sum := sha256.Sum256([]byte(manifestYAML))
	return cabinet.InstanceIdentity{Name: name, Artifact: artifact, Serial: 1, Binding: hex.EncodeToString(sum[:])}
}

// invokeAs runs a host function the way the runtime does: with the instance identity on the context.
// A nil identity is a call that does not come from a managed instance.
func invokeAs(fn gitHostFn, mod *minMod, id *cabinet.InstanceIdentity, req interface{}) map[string]interface{} {
	const outOff = 4096
	ctx := context.Background()
	if id != nil {
		ctx = cabinet.WithInstanceIdentity(ctx, *id)
	}
	reqLen := putJSON(mod, 0, req)
	n := fn(ctx, mod, 0, reqLen, outOff, 2048)
	out := map[string]interface{}{}
	if n > 0 {
		_ = json.Unmarshal(mod.mem.buf[outOff:outOff+uint32(n)], &out)
	}
	return out
}

func gitRegistryWith(t *testing.T, id, yaml string) *cicFlowRegistry {
	t.Helper()
	reg := newCICFlowRegistry(nil, &stubDoer{})
	require.NoError(t, reg.publishVerified(id, []byte(yaml), "art-1"))
	return reg
}

func TestGitManifest_Validation(t *testing.T) {
	bad := map[string]string{
		"unknown op":            "ops: [rebase]\n      remotes: [origin]",
		"no ops":                "ops: []",
		"fetch without remotes": "ops: [fetch]",
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
			assert.Error(t, reg.authorizeGit(inst("m@1", "art-1"), "status", gitAuthRequest{RepoID: "infra"}), "and nothing may be granted")
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
		assert.NoError(t, reg.authorizeGit(inst("mod@1", "art-1"), op, r), "%s %+v", op, r)
	}
	no := func(op string, r gitAuthRequest) {
		assert.Error(t, reg.authorizeGit(inst("mod@1", "art-1"), op, r), "%s %+v", op, r)
	}

	ok("status", gitAuthRequest{RepoID: "infra"})
	ok("fetch", gitAuthRequest{RepoID: "infra", Remote: "origin"})
	ok("checkout", gitAuthRequest{RepoID: "infra", Ref: "main"})
	ok("checkout", gitAuthRequest{RepoID: "infra", Ref: "release/1.2"})

	no("status", gitAuthRequest{RepoID: "iac-config"})                               // other repository
	no("fetch", gitAuthRequest{RepoID: "infra", Remote: "upstream"})                 // other remote
	no("fetch", gitAuthRequest{RepoID: "infra", Remote: ""})                         // nothing named
	no("checkout", gitAuthRequest{RepoID: "infra", Ref: "dev"})                      // other ref
	no("checkout", gitAuthRequest{RepoID: "infra", Ref: "release/"})                 // pattern needs a segment
	no("checkout", gitAuthRequest{RepoID: "infra", Ref: "releasex/1"})               // prefix is a segment boundary
	no("commit", gitAuthRequest{RepoID: "infra"})                                    // op not granted
	no("clone", gitAuthRequest{RepoID: "infra", URL: "https://x/y", Branch: "main"}) // op not granted
	assert.Error(t, reg.authorizeGit(inst("someone-else@1", "art-1"), "status", gitAuthRequest{RepoID: "infra"}), "an unregistered module has no capability")
	assert.Error(t, newCICFlowRegistry(nil, nil).authorizeGit(inst("mod@1", "art-1"), "status", gitAuthRequest{RepoID: "infra"}), "an empty registry denies")
}

// The caller's identity is the one the runtime attached to the call context for that instance. Nothing in
// the request can name another module, and a call without an identity is not served.
func TestGitHostFunction_IdentityComesFromTheRuntime(t *testing.T) {
	reg := gitRegistryWith(t, "granted@1", gitManifestYAML)
	called := 0
	inner := func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 { called++; return 0 }
	fn := authorized(reg, "status", inner)
	grantedID := inst("granted@1", "art-1")

	granted := newMod(8192)
	granted.name = "granted@1"
	out := invokeAs(fn, granted, &grantedID, map[string]interface{}{"repo_id": "infra"})
	assert.Nil(t, out["error"], "the granted instance may call status: %v", out)
	assert.Equal(t, 1, called)

	other := newMod(8192)
	other.name = "other@1"
	otherID := inst("other@1", "art-1")
	for _, claim := range []string{"module", "module_id", "caller", "name", "artifact", "serial"} {
		out = invokeAs(fn, other, &otherID, map[string]interface{}{"repo_id": "infra", claim: "granted@1"})
		assert.NotEmpty(t, out["error"], "a request field %q must not lend another module's identity", claim)
	}
	assert.Equal(t, 1, called, "the denied calls never reached the operation")

	out = invokeAs(fn, granted, nil, map[string]interface{}{"repo_id": "infra"})
	assert.Contains(t, out["error"], "not an identified module instance", "no identity on the context: refused")
	// The identity of one module on the call of another instance name: refused (they must agree).
	out = invokeAs(fn, other, &grantedID, map[string]interface{}{"repo_id": "infra"})
	assert.Contains(t, out["error"], "not an identified module instance")
	// An identity with no artifact or no serial is not an identity.
	for _, bad := range []cabinet.InstanceIdentity{{Name: "granted@1", Artifact: "", Serial: 1}, {Name: "granted@1", Artifact: "art-1", Serial: 0}} {
		bad := bad
		out = invokeAs(fn, granted, &bad, map[string]interface{}{"repo_id": "infra"})
		assert.NotEmpty(t, out["error"], "%+v", bad)
	}
	assert.Equal(t, 1, called)

	nilAuthz := authorized(nil, "status", inner)
	out = invokeAs(nilAuthz, granted, &grantedID, map[string]interface{}{"repo_id": "infra"})
	assert.NotEmpty(t, out["error"], "no authorizer: deny")
}

// wasmVariant returns a distinct, valid wasm module (an empty function type in the type section).
var (
	wasmBase    = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	wasmVariant = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00}
)

func gitModuleFS(t *testing.T, wasm []byte) (fstest.MapFS, []byte) {
	t.Helper()
	return gitModuleFSWith(t, wasm, `abi:
  imports:
    - module: git
      functions: [status]
capability_manifest:
  git:
    - repo: infra
      ops: [status]
`)
}

// gitModuleFSWith is gitModuleFS with the module's manifest given: the same wasm can be loaded with
// different capability manifests.
func gitModuleFSWith(t *testing.T, wasm []byte, manifest string) (fstest.MapFS, []byte) {
	t.Helper()
	fsys, yamlContent := moduleFSWith(t, "gitmod", manifest)
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
	// A released module: the envelope and its signature by the test module signer (#162), so that a load with
	// loadedPKIWithSource authenticates it and binds its capability.
	addEnvelope(t, fsys, "m.yaml", testModuleSigner(t), nil)
	return fsys, yamlContent
}

func hexOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func hooks(reg *cicFlowRegistry) func() {
	registerModuleCapability = reg.stageVerified
	commitModuleCapability = reg.commitGrant
	revokeModuleCapability = reg.revoke
	return func() { registerModuleCapability, commitModuleCapability, revokeModuleCapability = nil, nil, nil }
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
	const id = "gitmod@1.0.0"
	status := gitAuthRequest{RepoID: "infra"}

	fs1, y1 := gitModuleFS(t, nil)
	loadGit(t, svc, fs1, y1, loadedPKIWithSource(t))
	d1, err := svc.GetModule(id)
	require.NoError(t, err)
	art1 := d1.Checksum()

	assert.Error(t, reg.authorizeGit(instOf(t, id, "never-stored", fs1), "status", status), "an instance of some other artifact: denied")
	assert.NoError(t, reg.authorizeGit(instOf(t, id, art1, fs1), "status", status), "an instance of the verified artifact: allowed")
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
	assert.Error(t, reg.authorizeGit(instOf(t, id, art1, fs1), "status", status),
		"the instance that is still running the old artifact must not use the new artifact's grant")
	assert.NoError(t, reg.authorizeGit(instOf(t, id, d2.Checksum(), fs2), "status", status), "a new instance of the new artifact is granted")

	// The same ID loads again without the provenance anchor: it loads, but is granted nothing.
	loadGit(t, svc, fs1, y1, loadedPKIWithoutSource(t))
	assert.Error(t, reg.authorizeGit(instOf(t, id, d2.Checksum(), fs2), "status", status), "an unverified reload must not keep the earlier grant")
	d3, err := svc.GetModule(id)
	require.NoError(t, err)
	assert.Error(t, reg.authorizeGit(instOf(t, id, d3.Checksum(), fs1), "status", status),
		"the unverified artifact must not inherit the earlier grant even once it is stored and running")
}

// While a module is being reloaded over and over, an instance that is running a stale artifact is never
// authorized, whatever the interleaving. (Run with -race.)
func TestGitGrant_ConcurrentReloadNeverAuthorizesAStaleInstance(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	const id = "m@1"
	stale := inst(id, "stale-artifact")

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
					if reg.authorizeGit(stale, "status", gitAuthRequest{RepoID: "infra"}) == nil {
						violations.Add(1)
					}
				}
			}
		}()
	}
	for n := 0; n < 300; n++ {
		reg.revoke(id)
		require.NoError(t, reg.publishVerified(id, []byte(gitManifestYAML), fmt.Sprintf("new-artifact-%d", n)))
	}
	close(stop)
	wg.Wait()
	assert.Zero(t, violations.Load(), "a stale instance was authorized during a reload")
}

func TestGitGrant_UnboundOrUnknownInstanceIsDenied(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	require.NoError(t, reg.publishVerified("m@1", []byte(gitManifestYAML), ""))
	req := gitAuthRequest{RepoID: "infra"}
	assert.Error(t, reg.authorizeGit(inst("m@1", "x"), "status", req), "granted but the grant is not bound to a stored artifact")
	require.NoError(t, reg.publishVerified("m@1", []byte(gitManifestYAML), "y"))
	assert.Error(t, reg.authorizeGit(inst("m@1", "x"), "status", req), "instance of x, granted for y")
	require.NoError(t, reg.publishVerified("m@1", []byte(gitManifestYAML), "x"))
	assert.NoError(t, reg.authorizeGit(inst("m@1", "x"), "status", req))
	assert.Error(t, reg.authorizeGit(inst("m@1", ""), "status", req), "an instance with no artifact")
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
	gid := inst("granted@1", "art-1")
	out := invokeAs(statusFn, granted, &gid, map[string]interface{}{"repo_id": "r"})
	assert.NotEmpty(t, out["error"], "repo r is not in the manifest (only infra): denied before the operation")

	stranger := newMod(8192)
	stranger.name = "stranger@1"
	sid := inst("stranger@1", "art-1")
	out = invokeAs(statusFn, stranger, &sid, map[string]interface{}{"repo_id": "r"})
	assert.NotEmpty(t, out["error"])
}

var _ = fmt.Sprint
var _ fs.FS = fstest.MapFS{}
var _ = os.Stat
var _ = exec.Command
var _ = filepath.Join
var _ nexusgit.GitOps

// A manifest can grant branch and tag names only: not HEAD, and nothing under refs/ except heads/ and tags/.
func TestGitManifest_RefsAreBranchesAndTagsOnly(t *testing.T) {
	for _, ref := range []string{"refs/notes/*", "refs/notes/commits", "refs/replace/abc", "refs/remotes/origin/main", "refs/stash", "refs/*", "refs", "HEAD", "FETCH_HEAD"} {
		t.Run(ref, func(t *testing.T) {
			yaml := "abi:\n  imports: []\ncapability_manifest:\n  git:\n    - repo: infra\n      ops: [checkout]\n      refs: [\"" + ref + "\"]\n"
			reg := newCICFlowRegistry(nil, &stubDoer{})
			err := reg.registerVerified("m@1", []byte(yaml))
			require.Error(t, err, "ref pattern %q must be refused at load", ref)
			assert.Error(t, reg.authorizeGit(inst("m@1", "art-1"), "checkout", gitAuthRequest{RepoID: "infra", Ref: ref}))
		})
	}
	for _, ref := range []string{"main", "release/*", "refs/heads/main", "refs/heads/rel/*", "refs/tags/*", "v1"} {
		yaml := "abi:\n  imports: []\ncapability_manifest:\n  git:\n    - repo: infra\n      ops: [checkout]\n      refs: [\"" + ref + "\"]\n"
		assert.NoError(t, newCICFlowRegistry(nil, &stubDoer{}).registerVerified("m@1", []byte(yaml)), ref)
	}
}
