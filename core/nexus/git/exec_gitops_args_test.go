package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// markerProgram writes an executable script that creates a marker file when run, and returns the
// script path and the marker path. If a guest-supplied value ever makes git run the script, the
// marker exists afterwards.
func markerProgram(t *testing.T) (script, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "ran.marker")
	script = filepath.Join(dir, "prog.sh")
	body := "#!/bin/sh\necho ran > '" + marker + "'\nexit 1\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	return script, marker
}

func argTestOps(t *testing.T) (*ExecGitOps, string, string) {
	t.Helper()
	src := initBareTestRepo(t)
	work := filepath.Join(t.TempDir(), "work")
	reg := NewRepoRegistry()
	reg.Register("r", work)
	ops := NewExecGitOps(reg)
	ops.allowLocalSources = true // the fixture clones from a local path; guests never set this
	require.NoError(t, ops.Clone(context.Background(), "r", src, "main"))
	return ops, work, src
}

func resetMarker(marker string) { _ = os.Remove(marker) }

func requireNoMarker(t *testing.T, marker string) {
	t.Helper()
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "a guest-chosen program was executed (marker exists)")
}

func TestGuestValuesNeverReachGitAsOptions(t *testing.T) {
	ctx := context.Background()
	ops, work, src := argTestOps(t)
	script, marker := markerProgram(t)

	t.Run("push remote and refspec", func(t *testing.T) {
		resetMarker(marker)
		assert.Error(t, ops.Push(ctx, "r", "--receive-pack="+script, "origin"))
		assert.Error(t, ops.Push(ctx, "r", "origin", "--receive-pack="+script))
		assert.Error(t, ops.Push(ctx, "r", "--exec="+script, "main"))
		assert.Error(t, ops.Push(ctx, "r", src, "main:main"), "a URL or path is not a registered remote name")
		requireNoMarker(t, marker)
	})
	t.Run("fetch remote", func(t *testing.T) {
		resetMarker(marker)
		assert.Error(t, ops.Fetch(ctx, "r", "--upload-pack="+script))
		assert.Error(t, ops.Fetch(ctx, "r", src))
		requireNoMarker(t, marker)
	})
	t.Run("clone url and branch", func(t *testing.T) {
		resetMarker(marker)
		for _, bad := range []string{"--upload-pack=" + script, "ext::sh -c " + script, "file://" + src, src, "-u" + script} {
			strict := NewExecGitOps(ops.registry) // default policy: no local sources
			assert.Error(t, strict.Clone(ctx, "r", bad, "main"), "url %q", bad)
		}
		strict := NewExecGitOps(ops.registry)
		assert.Error(t, strict.Clone(ctx, "r", "https://example.invalid/x.git", "--upload-pack="+script))
		requireNoMarker(t, marker)
	})
	t.Run("checkout ref", func(t *testing.T) {
		resetMarker(marker)
		assert.Error(t, ops.Checkout(ctx, "r", "--orphan=x"))
		assert.Error(t, ops.Checkout(ctx, "r", "-b"))
		assert.NoError(t, ops.Checkout(ctx, "r", "main"), "a normal branch name still works")
	})
	t.Run("commit paths", func(t *testing.T) {
		for _, bad := range []string{"--force", ":(top)x", ":(glob)*", "../outside", "/etc/passwd", ".git/config", "a/../../b"} {
			_, err := ops.Commit(ctx, "r", "m", []string{bad})
			assert.Error(t, err, "path %q", bad)
		}
		require.NoError(t, os.WriteFile(filepath.Join(work, "ok.txt"), []byte("x"), 0o644))
		hash, err := ops.Commit(ctx, "r", "-m looks like an option but is a message", []string{"ok.txt"})
		assert.NoError(t, err, "a message that starts with a dash is just a message")
		assert.Len(t, hash, 40)
	})
	t.Run("legitimate push to a registered remote name still works", func(t *testing.T) {
		bare := filepath.Join(t.TempDir(), "bare.git")
		out, err := exec.Command("git", "init", "--bare", "-b", "main", bare).CombinedOutput()
		require.NoError(t, err, string(out))
		out, err = exec.Command("git", "-C", work, "remote", "add", "up", bare).CombinedOutput()
		require.NoError(t, err, string(out))
		ops.allowLocalSources = true
		assert.NoError(t, ops.Push(ctx, "r", "up", "main:main"))
	})
	requireNoMarker(t, marker)
}
