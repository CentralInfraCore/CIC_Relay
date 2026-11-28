package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"

	"centralrelay/core/cabinet"
)

// git.push is withdrawn for guests (gitPushDisabled). It is refused at three independent points, and the
// low-level ExecGitOps.Push (with all its hardening, tested in core/nexus/git) stays unreachable from them.

// 1. No manifest can grant it, and no module can import it.
func TestGitPush_NoManifestCanGrantItAndNoModuleCanImportIt(t *testing.T) {
	cases := map[string]string{
		"push op":                             "abi:\n  imports: []\ncapability_manifest:\n  git:\n    - repo: infra\n      ops: [push]\n      remotes: [origin]\n      refs: [main]\n",
		"push among others":                   "abi:\n  imports: []\ncapability_manifest:\n  git:\n    - repo: infra\n      ops: [status, push]\n      remotes: [origin]\n      refs: [main]\n",
		"push import, granted something else": "abi:\n  imports:\n    - module: git\n      functions: [push]\ncapability_manifest:\n  git:\n    - repo: infra\n      ops: [status]\n",
		"push import, no manifest":            "abi:\n  imports:\n    - module: git\n      functions: [push]\n",
	}
	for name, yaml := range cases {
		t.Run(name, func(t *testing.T) {
			reg := newCICFlowRegistry(nil, &stubDoer{})
			err := reg.registerVerified("m@1", []byte(yaml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "git.push", "the refusal names what is withdrawn")
			assert.NotContains(t, err.Error(), "unknown", "it is not 'unknown': it is withdrawn on purpose")
			_, granted := reg.git["m@1"]
			assert.False(t, granted, "nothing was granted")
		})
	}
	t.Run("a verified module that imports it is rejected at load", func(t *testing.T) {
		reg := newCICFlowRegistry(nil, &stubDoer{})
		defer hooks(reg)()
		svc := newCabinetForTest(t)
		fsys, y := gitModuleFSWith(t, nil, "abi:\n  imports:\n    - module: git\n      functions: [push]\ncapability_manifest:\n  git:\n    - repo: infra\n      ops: [status]\n")
		err := loadModule(context.Background(), svc, fsys, "m.yaml", y, loadedPKIWithSource(t), int64(16*1024*1024), newDiscardLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "git.push")
		_, err = svc.GetModule("gitmod@1.0.0")
		assert.Error(t, err, "the module was not stored")
	})
}

// 2. Whatever a registry entry says, push is not authorised: a grant that somehow got past validation (it
// is built here by hand) still authorises nothing for push, and still works for what it legitimately covers.
func TestGitPush_NeverAuthorisedEvenByAForgedGrant(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	reg.git["m@1"] = gitGrant{
		rules:          []gitRule{{Repo: "infra", Ops: []string{"push", "status"}, Remotes: []string{"origin"}, Refs: []string{"main"}}},
		artifact:       "art-1",
		manifestDigest: "d",
		envelopeDigest: "d",
		active:         true,
	}
	id := inst("m@1", "art-1")
	id.Binding = "d"
	err := reg.authorizeGit(id, "push", gitAuthRequest{RepoID: "infra", Remote: "origin", Refspec: "main:main"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git.push is disabled")
	assert.NoError(t, reg.authorizeGit(id, "status", gitAuthRequest{RepoID: "infra"}), "the rest of the grant is unaffected")
}

type pushCounter struct {
	stubGitOps
	pushes int
}

func (c *pushCounter) Push(context.Context, string, string, string) error { c.pushes++; return nil }

type allowAll struct{}

func (allowAll) authorizeGit(cabinet.InstanceIdentity, string, gitAuthRequest) error { return nil }

// 3. The host function never reaches GitOps.Push: even with an authorizer that allows everything, with a
// real guest that calls git.push, with a well-formed request. The module still LINKS (the export exists).
func TestGitPush_HostFunctionNeverReachesTheOperation(t *testing.T) {
	ops := &pushCounter{}
	rt := wazero.NewRuntime(context.Background())
	defer rt.Close(context.Background())
	require.NoError(t, registerGitHostFunctions(context.Background(), rt, ops, allowAll{}))
	assert.NotNil(t, rt.Module("git").ExportedFunctionDefinitions()["push"], "git.push is still exported, so importing modules link")

	gid := inst("anyone@1", "art-1")
	for _, req := range []string{
		`{"repo_id":"infra","remote":"origin","refspec":"main:main"}`,
		`{"repo_id":"infra","remote":"origin","refspec":"main"}`,
		`not json at all`,
		``,
	} {
		out := runGuest(t, rt, "anyone@1", &gid, "push", req)
		errText, _ := out["error"].(string)
		assert.Contains(t, errText, "git.push is disabled", "request %q", req)
	}
	// and with no identity at all
	out := runGuest(t, rt, "anyone@1", nil, "push", `{"repo_id":"infra"}`)
	assert.Contains(t, out["error"], "git.push is disabled")
	assert.Zero(t, ops.pushes, "GitOps.Push was never called")
}
