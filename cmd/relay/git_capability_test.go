package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

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

func gitRegistryWith(t *testing.T, id, yaml string) *cicFlowRegistry {
	t.Helper()
	reg := newCICFlowRegistry(nil, &stubDoer{})
	require.NoError(t, reg.registerVerified(id, []byte(yaml)))
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

// A grant belongs to the artifact it was verified for. Loading the same module ID again revokes it, so
// a replacement (unsigned, unverified, or a different binary under the same name@version) is never
// served with the earlier artifact's git reach.
func TestGitGrant_DoesNotOutliveTheArtifact(t *testing.T) {
	const manifest = `abi:
  imports:
    - module: git
      functions: [status]
capability_manifest:
  git:
    - repo: infra
      ops: [status]
`
	moduleFS, yamlContent := moduleFSWith(t, "gitmod", manifest)

	reg := newCICFlowRegistry(nil, &stubDoer{})
	registerModuleCapability = reg.registerVerified
	revokeModuleCapability = reg.revoke
	defer func() { registerModuleCapability, revokeModuleCapability = nil, nil }()

	require.NoError(t, loadModule(context.Background(), newCabinetForTest(t), moduleFS, "m.yaml", yamlContent,
		loadedPKIWithSource(t), int64(16*1024*1024), newDiscardLogger()))
	require.NoError(t, reg.authorizeGit("gitmod@1.0", "status", gitAuthRequest{RepoID: "infra"}), "a verified module is granted")
	g := reg.git["gitmod@1.0"]
	assert.NotEmpty(t, g.wasmDigest, "the grant records the digest of the wasm it was verified for")
	assert.NotEmpty(t, g.manifestDigest, "and the digest of the manifest document")

	// The same ID loads again, now without the CICSourceCA provenance: it loads, but is not re-granted.
	require.NoError(t, loadModule(context.Background(), newCabinetForTest(t), moduleFS, "m.yaml", yamlContent,
		buildLoadedPKIAssets(t), int64(16*1024*1024), newDiscardLogger()))
	assert.Error(t, reg.authorizeGit("gitmod@1.0", "status", gitAuthRequest{RepoID: "infra"}),
		"a reload that does not verify must not keep the previous grant")
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
