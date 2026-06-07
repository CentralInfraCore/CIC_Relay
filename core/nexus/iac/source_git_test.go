package iac

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initTestRepo creates a local git repo in dir with one YAML file and returns
// the branch name used (always "main").
func initTestRepo(t *testing.T, dir string) string {
	t.Helper()
	branch := "main"

	mustGit := func(repoDir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0",
			"GIT_ASKPASS=/bin/false",
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.local",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)
	}

	mustGit(dir, "-c", "core.hooksPath=/dev/null", "init", "-b", branch, dir)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "relay.yaml"),
		[]byte("kind: Relay\nname: test-relay\n"),
		0644,
	))
	mustGit(dir, "-c", "core.hooksPath=/dev/null", "add", ".")
	mustGit(dir, "-c", "core.hooksPath=/dev/null",
		"-c", "user.email=test@test.local",
		"-c", "user.name=Test",
		"commit", "-m", "init",
	)
	return branch
}

// TestGitSource_CloneFresh tests cloning a fresh repo and reading YAML files.
func TestGitSource_CloneFresh(t *testing.T) {
	srcDir := t.TempDir()
	branch := initTestRepo(t, srcDir)

	cloneDir := filepath.Join(t.TempDir(), "clone")

	src := NewGitSource(GitSourceConfig{
		URL:       srcDir,
		Branch:    branch,
		LocalPath: cloneDir,
	})

	files, err := src.FetchConfig(context.Background())
	require.NoError(t, err)
	assert.Contains(t, files, "relay.yaml")
	assert.Contains(t, string(files["relay.yaml"]), "test-relay")
}

// TestGitSource_FetchExisting tests that calling FetchConfig on an already-cloned
// repo performs a fetch+checkout without error.
func TestGitSource_FetchExisting(t *testing.T) {
	srcDir := t.TempDir()
	branch := initTestRepo(t, srcDir)

	cloneDir := filepath.Join(t.TempDir(), "clone")

	src := NewGitSource(GitSourceConfig{
		URL:       srcDir,
		Branch:    branch,
		LocalPath: cloneDir,
	})

	// First call — clone.
	_, err := src.FetchConfig(context.Background())
	require.NoError(t, err)

	// Second call — fetch round-trip (cloneDir exists now).
	files, err := src.FetchConfig(context.Background())
	require.NoError(t, err)
	assert.Contains(t, files, "relay.yaml")
}

// TestGitSource_InvalidURL tests that an invalid URL returns a wrapped error.
func TestGitSource_InvalidURL(t *testing.T) {
	src := NewGitSource(GitSourceConfig{
		URL:       "file:///nonexistent/path/to/repo",
		Branch:    "main",
		LocalPath: filepath.Join(t.TempDir(), "clone"),
	})

	_, err := src.FetchConfig(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git clone failed")
}

// TestGitSource_Timeout tests that a very short timeout causes an error.
func TestGitSource_Timeout(t *testing.T) {
	srcDir := t.TempDir()
	branch := initTestRepo(t, srcDir)

	src := NewGitSource(GitSourceConfig{
		URL:       srcDir,
		Branch:    branch,
		LocalPath: filepath.Join(t.TempDir(), "clone"),
		Timeout:   1 * time.Millisecond,
	})

	_, err := src.FetchConfig(context.Background())
	require.Error(t, err)
}

// TestGitSource_Defaults tests that NewGitSource applies default Branch and Timeout.
func TestGitSource_Defaults(t *testing.T) {
	src := NewGitSource(GitSourceConfig{URL: "u", LocalPath: "/p"})
	assert.Equal(t, "main", src.cfg.Branch)
	assert.Equal(t, 30*time.Second, src.cfg.Timeout)
}
