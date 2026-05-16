package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/obs"
)

// initBareTestRepo creates a non-bare git repo with one commit and returns its path.
func initBareTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitEnv := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.local",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.local",
	}
	mustGit := func(repoDir string, args ...string) {
		t.Helper()
		fullArgs := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
		cmd := exec.Command("git", fullArgs...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), gitEnv...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	mustGit(dir, "init", "-b", "main", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "relay.yaml"), []byte("kind: Relay\n"), 0644))
	mustGit(dir, "add", ".")
	mustGit(dir, "commit", "-m", "init")
	return dir
}

// TestExecGitOps_CloneAndStatus clones a local repo and checks that Status returns
// an empty list (clean working tree).
func TestExecGitOps_CloneAndStatus(t *testing.T) {
	srcDir := initBareTestRepo(t)
	cloneDir := filepath.Join(t.TempDir(), "clone")

	reg := NewRepoRegistry()
	reg.Register("test-repo", cloneDir)
	ops := NewExecGitOps(reg)

	// Clone the source into cloneDir.
	require.NoError(t, ops.Clone(context.Background(), "test-repo", srcDir, "main"))

	// Status on a freshly cloned repo should be clean.
	entries, err := ops.Status(context.Background(), "test-repo")
	require.NoError(t, err)
	assert.Empty(t, entries, "freshly cloned repo should have no uncommitted changes")
}

// TestExecGitOps_UnknownRepo verifies that any operation with an unregistered repoID fails.
func TestExecGitOps_UnknownRepo(t *testing.T) {
	reg := NewRepoRegistry()
	ops := NewExecGitOps(reg)

	_, err := ops.Status(context.Background(), "ghost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown repoID: "ghost"`)
}

// TestHardenedEnv verifies that git commands are executed with the required
// hardened environment variables.
func TestHardenedEnv(t *testing.T) {
	// Create a fake "git" binary that writes its environment to a file.
	binDir := t.TempDir()
	envFile := filepath.Join(binDir, "env.txt")
	scriptContent := fmt.Sprintf("#!/bin/sh\nprintenv > %s\nexit 0\n", envFile)
	scriptPath := filepath.Join(binDir, "git")
	require.NoError(t, os.WriteFile(scriptPath, []byte(scriptContent), 0755))

	// Temporarily prepend binDir to PATH so our fake git is found first.
	t.Setenv("PATH", binDir+string(filepath.ListSeparator)+os.Getenv("PATH"))

	repoDir := t.TempDir()
	reg := NewRepoRegistry()
	reg.Register("env-test", repoDir)
	ops := NewExecGitOps(reg)

	// The fake git exits 0, so Status should succeed (empty result).
	_, _ = ops.Status(context.Background(), "env-test")

	captured, err := os.ReadFile(envFile)
	require.NoError(t, err, "fake git script did not write env file")

	env := string(captured)
	assert.True(t, strings.Contains(env, "GIT_CONFIG_NOSYSTEM=1"),
		"GIT_CONFIG_NOSYSTEM=1 must be set; got:\n%s", env)
	assert.True(t, strings.Contains(env, "GIT_TERMINAL_PROMPT=0"),
		"GIT_TERMINAL_PROMPT=0 must be set")
	assert.True(t, strings.Contains(env, "GIT_ASKPASS=/bin/false"),
		"GIT_ASKPASS=/bin/false must be set")
}

// TestParsePortcelainStatus validates the porcelain output parser.
func TestParsePortcelainStatus(t *testing.T) {
	input := " M modified.go\n?? untracked.txt\nR  old.go -> new.go\n"
	entries := parsePortcelainStatus(input)
	require.Len(t, entries, 3)
	assert.Equal(t, " M", entries[0].State)
	assert.Equal(t, "modified.go", entries[0].Path)
	assert.Equal(t, "??", entries[1].State)
	assert.Equal(t, "untracked.txt", entries[1].Path)
	assert.Equal(t, "new.go", entries[2].Path)
}

// TestExecGitOps_SetObsSinks_MetricEmission verifies that after SetObsSinks,
// a successful git op emits a relay.git.op_duration_ms histogram. (M31)
func TestExecGitOps_SetObsSinks_MetricEmission(t *testing.T) {
	srcDir := initBareTestRepo(t)
	cloneDir := filepath.Join(t.TempDir(), "clone")

	reg := NewRepoRegistry()
	reg.Register("obs-repo", cloneDir)
	ops := NewExecGitOps(reg)

	sink := &obs.CaptureSink{}
	ops.SetObsSinks(sink, sink)

	require.NoError(t, ops.Clone(context.Background(), "obs-repo", srcDir, "main"))

	require.NotEmpty(t, sink.Histos, "expected at least one histogram after clone")
	found := false
	for _, h := range sink.Histos {
		if h.Name == "relay.git.op_duration_ms" && h.Attrs["op"] == "clone" {
			found = true
			assert.Equal(t, int64(1), h.Count)
			assert.Equal(t, "nexus/git", h.Resource.Component)
		}
	}
	assert.True(t, found, "relay.git.op_duration_ms{op=clone} not found in histograms")
}

// TestExecGitOps_SetObsSinks_ErrorLog verifies that a failed git op emits a WARN log. (M31)
func TestExecGitOps_SetObsSinks_ErrorLog(t *testing.T) {
	reg := NewRepoRegistry()
	reg.Register("fail-repo", t.TempDir()) // valid path but not a git repo
	ops := NewExecGitOps(reg)

	sink := &obs.CaptureSink{}
	ops.SetObsSinks(sink, sink)

	_, _ = ops.Status(context.Background(), "fail-repo") // will fail — not a git repo

	require.NotEmpty(t, sink.Logs, "expected WARN log on failure")
	assert.Equal(t, "WARN", sink.Logs[0].Level)
	assert.Equal(t, "git op failed", sink.Logs[0].Message)
	assert.Equal(t, "status", sink.Logs[0].Attrs["op"])
}

// TestExecGitOps_SetObsSinks_NilSafe verifies that nil sinks don't cause panics. (M31)
func TestExecGitOps_SetObsSinks_NilSafe(t *testing.T) {
	reg := NewRepoRegistry()
	reg.Register("nil-repo", t.TempDir())
	ops := NewExecGitOps(reg)
	ops.SetObsSinks(nil, nil)

	assert.NotPanics(t, func() {
		_, _ = ops.Status(context.Background(), "nil-repo")
	})
}

func TestExecGitOps_Fetch_UnknownRepo(t *testing.T) {
	err := NewExecGitOps(NewRepoRegistry()).Fetch(context.Background(), "ghost", "origin")
	require.Error(t, err)
}

func TestExecGitOps_Checkout_UnknownRepo(t *testing.T) {
	err := NewExecGitOps(NewRepoRegistry()).Checkout(context.Background(), "ghost", "main")
	require.Error(t, err)
}

func TestExecGitOps_Commit_UnknownRepo(t *testing.T) {
	_, err := NewExecGitOps(NewRepoRegistry()).Commit(context.Background(), "ghost", "msg", nil)
	require.Error(t, err)
}

func TestExecGitOps_Push_UnknownRepo(t *testing.T) {
	err := NewExecGitOps(NewRepoRegistry()).Push(context.Background(), "ghost", "origin", "main")
	require.Error(t, err)
}

func TestExecGitOps_Fetch_Success(t *testing.T) {
	srcDir := initBareTestRepo(t)
	cloneDir := filepath.Join(t.TempDir(), "clone")
	reg := NewRepoRegistry()
	reg.Register("repo", cloneDir)
	ops := NewExecGitOps(reg)
	require.NoError(t, ops.Clone(context.Background(), "repo", srcDir, "main"))
	require.NoError(t, ops.Fetch(context.Background(), "repo", "origin"))
}

func TestExecGitOps_Checkout_Success(t *testing.T) {
	srcDir := initBareTestRepo(t)
	cloneDir := filepath.Join(t.TempDir(), "clone")
	reg := NewRepoRegistry()
	reg.Register("repo", cloneDir)
	ops := NewExecGitOps(reg)
	require.NoError(t, ops.Clone(context.Background(), "repo", srcDir, "main"))
	require.NoError(t, ops.Checkout(context.Background(), "repo", "main"))
}

func TestExecGitOps_Commit_Success(t *testing.T) {
	srcDir := initBareTestRepo(t)
	cloneDir := filepath.Join(t.TempDir(), "clone")
	reg := NewRepoRegistry()
	reg.Register("repo", cloneDir)
	ops := NewExecGitOps(reg)
	require.NoError(t, ops.Clone(context.Background(), "repo", srcDir, "main"))

	require.NoError(t, os.WriteFile(filepath.Join(cloneDir, "new.txt"), []byte("hello"), 0644))
	hash, err := ops.Commit(context.Background(), "repo", "add new.txt", []string{"new.txt"})
	require.NoError(t, err)
	assert.Len(t, hash, 40, "commit hash should be a 40-char SHA-1 hex")
}

func TestExecGitOps_Push_Success(t *testing.T) {
	gitEnv := []string{
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.local",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.local",
	}
	mustGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), gitEnv...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	// Remote: non-bare repo that allows pushes to its current branch.
	remoteDir := t.TempDir()
	mustGit(remoteDir, "init", "-b", "main", remoteDir)
	require.NoError(t, os.WriteFile(filepath.Join(remoteDir, "f.txt"), []byte("init"), 0644))
	mustGit(remoteDir, "add", ".")
	mustGit(remoteDir, "commit", "-m", "init")
	mustGit(remoteDir, "config", "receive.denyCurrentBranch", "warn")

	cloneDir := filepath.Join(t.TempDir(), "clone")
	reg := NewRepoRegistry()
	reg.Register("repo", cloneDir)
	ops := NewExecGitOps(reg)
	require.NoError(t, ops.Clone(context.Background(), "repo", remoteDir, "main"))

	require.NoError(t, os.WriteFile(filepath.Join(cloneDir, "new.txt"), []byte("new"), 0644))
	_, err := ops.Commit(context.Background(), "repo", "add new", []string{"new.txt"})
	require.NoError(t, err)

	require.NoError(t, ops.Push(context.Background(), "repo", "origin", "main"))
}
