package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"

	nexusgit "centralrelay/core/nexus/git"
)

// realGitRepo makes a repository with one commit, a configured remote "origin", and registers it as "r"
// in a real (production-constructed) ExecGitOps, so the host functions are exercised end to end.
func realGitRepo(t *testing.T) (nexusgit.GitOps, string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run(root, "init", "--bare", "-b", "main", bare)
	run(root, "init", "-b", "main", work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "a.txt"), []byte("a"), 0o644))
	run(work, "add", ".")
	run(work, "commit", "-m", "init")
	run(work, "remote", "add", "origin", bare)

	reg := nexusgit.NewRepoRegistry()
	reg.Register("r", work)
	return nexusgit.NewExecGitOps(reg), work
}

// Every host function the "git" module exports, with a payload that smuggles an option or a transport
// helper into a guest-controlled field. None may run a guest-chosen program, and each must answer with
// an error. The export list is asserted too: a new git.* function fails this test until it is covered.
func TestGitHostFunctions_GuestOptionsNeverRun(t *testing.T) {
	ops, _ := realGitRepo(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.marker")
	script := filepath.Join(dir, "prog.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho ran > '"+marker+"'\nexit 1\n"), 0o755))

	rt := wazero.NewRuntime(context.Background())
	defer rt.Close(context.Background())
	require.NoError(t, registerGitHostFunctions(context.Background(), rt, ops, nil))

	cases := map[string]struct {
		fn  hostFn
		req map[string]interface{}
	}{
		"clone":        {makeCloneFn(ops), map[string]interface{}{"repo_id": "r", "url": "--upload-pack=" + script, "branch": "main"}},
		"clone-ext":    {makeCloneFn(ops), map[string]interface{}{"repo_id": "r", "url": "ext::sh -c " + script, "branch": "main"}},
		"fetch":        {makeFetchFn(ops), map[string]interface{}{"repo_id": "r", "remote": "--upload-pack=" + script}},
		"checkout":     {makeCheckoutFn(ops), map[string]interface{}{"repo_id": "r", "ref": "--orphan=x"}},
		"commit":       {makeCommitFn(ops), map[string]interface{}{"repo_id": "r", "message": "m", "paths": []string{":(top)a.txt"}}},
		"push":         {makePushFn(ops), map[string]interface{}{"repo_id": "r", "remote": "--receive-pack=" + script, "refspec": "main"}},
		"push-refspec": {makePushFn(ops), map[string]interface{}{"repo_id": "r", "remote": "origin", "refspec": "--receive-pack=" + script}},
	}
	for name, c := range cases {
		_ = os.Remove(marker)
		mod := newMod(8192)
		_, out := invoke(c.fn, mod, c.req)
		assert.NotEmpty(t, out["error"], "%s: a hostile value must be refused (got %v)", name, out)
		_, err := os.Stat(marker)
		assert.True(t, os.IsNotExist(err), "%s: a guest-chosen program ran", name)
	}

	// Export list: every git.* function is either covered above or takes no free-form value (status).
	// A new export fails here until someone decides which of the two it is.
	covered := map[string]bool{"clone": true, "fetch": true, "checkout": true, "commit": true, "push": true, "status": true}
	gitMod := rt.Module("git")
	require.NotNil(t, gitMod, "the git host module must be instantiated")
	for name := range gitMod.ExportedFunctionDefinitions() {
		assert.True(t, covered[name], "git.%s is exported but not covered by a hostile-value case", name)
	}
	assert.Len(t, gitMod.ExportedFunctionDefinitions(), len(covered))
}
