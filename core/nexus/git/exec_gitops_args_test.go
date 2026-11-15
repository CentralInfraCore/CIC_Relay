package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	t.Run("remote must be configured, and its URL allowed", func(t *testing.T) {
		resetMarker(marker)
		// A directory inside the repository that is shaped like a repository: git would read the name as a path.
		sub := filepath.Join(work, "sub")
		out, err := exec.Command("git", "init", "--bare", "-b", "main", sub).CombinedOutput()
		require.NoError(t, err, string(out))
		for _, name := range []string{"sub", "nosuch", "upstream"} {
			err := ops.Push(ctx, "r", name, "main")
			require.Error(t, err, name)
			assert.ErrorIs(t, err, ErrInvalidArgument, name)
			assert.Contains(t, err.Error(), "not configured", name)
			err = ops.Fetch(ctx, "r", name)
			assert.ErrorIs(t, err, ErrInvalidArgument, name)
			assert.Contains(t, err.Error(), "not configured", name)
		}
		// A configured remote whose URL uses a transport that is not allowed is refused too.
		for name, u := range map[string]string{"evil": "ext::sh -c " + script, "path": "../elsewhere", "hostless": "https:///x"} {
			out, err := exec.Command("git", "-C", work, "remote", "add", name, u).CombinedOutput()
			require.NoError(t, err, string(out))
			strict := NewExecGitOps(ops.registry)
			err = strict.Fetch(ctx, "r", name)
			require.Error(t, err, name)
			assert.ErrorIs(t, err, ErrInvalidArgument, name)
			assert.Contains(t, err.Error(), "not allowed", name)
		}
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

// Local sources are an explicit, narrow form, not the fallthrough for anything that fails to parse.
func TestLocalSourcePolicy(t *testing.T) {
	ok := []string{"/srv/repos/a", "file:///srv/repos/a"}
	bad := []string{
		"relative/path", "../up", "./x", "/srv/../etc/x", "/srv//x", "/srv/x/", "file://host/srv/x", "file:relative",
		"%zz", "ext::sh -c id", "-u/srv/x", "", "/srv/x\n", "https://", "ftp://host/x", "host:path", "git@host:org/repo.git",
	}
	for _, v := range ok {
		assert.NoError(t, validateCloneURL(v, true), v)
		assert.ErrorIs(t, validateCloneURL(v, false), ErrInvalidArgument, "%q must be refused when local sources are off", v)
	}
	for _, v := range bad {
		assert.ErrorIs(t, validateCloneURL(v, true), ErrInvalidArgument, "%q must be refused even with local sources on", v)
		assert.ErrorIs(t, validateCloneURL(v, false), ErrInvalidArgument, "%q", v)
	}
}

// URLs can carry credentials. They are refused when a password is present, and masked wherever text
// that may contain a URL leaves the package.
func TestURLCredentialsNeverLeak(t *testing.T) {
	ctx := context.Background()
	reg := NewRepoRegistry()
	reg.Register("r", filepath.Join(t.TempDir(), "w"))
	ops := NewExecGitOps(reg)

	err := ops.Clone(ctx, "r", "https://alice:hunter2@example.invalid/x.git", "main")
	require.ErrorIs(t, err, ErrInvalidArgument)
	assert.NotContains(t, err.Error(), "hunter2")
	assert.NotContains(t, err.Error(), "alice")

	// A URL without a password passes validation; the clone fails (no network here), and the failure
	// text carries neither the user part nor anything git echoed.
	err = ops.Clone(ctx, "r", "https://alice@example.invalid/x.git", "main")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalidArgument)
	assert.NotContains(t, err.Error(), "alice@")

	assert.Equal(t, "fatal: unable to access 'https://***@h/x': failed",
		scrubURLs("fatal: unable to access 'https://tok:en@h/x': failed"))
	assert.Equal(t, "ssh://***@h/x", scrubURLs("ssh://git@h/x"))
	assert.Equal(t, "no url here", scrubURLs("no url here"))
}

// F6: the commit holds exactly the paths the request named. Something staged earlier, outside this
// request, must neither be committed nor lost from the index.
func TestCommitScopeIsExactlyTheRequestedPaths(t *testing.T) {
	ctx := context.Background()
	ops, work, _ := argTestOps(t)
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", work}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	require.NoError(t, os.WriteFile(filepath.Join(work, "mine.txt"), []byte("1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(work, "other.txt"), []byte("2"), 0o644))
	git("add", "other.txt") // staged by someone else before the request

	hash, err := ops.Commit(ctx, "r", "only mine", []string{"mine.txt"})
	require.NoError(t, err)
	assert.Equal(t, "mine.txt", git("show", "--name-only", "--format=", hash), "the commit contains exactly the requested path")
	assert.Equal(t, "other.txt", git("diff", "--cached", "--name-only"), "the earlier staged change is untouched and still staged")

	// A deletion is a path too.
	require.NoError(t, os.Remove(filepath.Join(work, "relay.yaml")))
	hash, err = ops.Commit(ctx, "r", "remove relay.yaml", []string{"relay.yaml"})
	require.NoError(t, err)
	assert.Equal(t, "relay.yaml", git("show", "--name-only", "--format=", hash))
	assert.Equal(t, "other.txt", git("diff", "--cached", "--name-only"), "still untouched after the second commit")
}

// F5: every ref the validator accepts must also be accepted by git's own rules. The validator may be
// stricter than git, never looser. Deterministic corpus built from tokens that exercise the rules.
func TestRefValidatorIsNeverLooserThanGit(t *testing.T) {
	tokens := []string{"a", "b", "1", ".", "..", "/", "//", "-", "_", "@", "@{", "{", ".lock", "x.lock", "+", "~", "^", ":", "?", "*", "[", "\\", " ", "refs", "heads", "HEAD", "release", ".hidden"}
	seed := uint64(0x9E3779B97F4A7C15)
	next := func() uint64 { seed ^= seed << 13; seed ^= seed >> 7; seed ^= seed << 17; return seed }
	cases := []string{"main", "release/1.2", "feature/a-b_c", "refs/heads/x", "refs/tags/v1", "a.lock", "a.lock/b", "a/.b", ".a", "a/", "/a", "a//b", "a..b", "@", "a@{b", "a.", "x@y"}
	for len(cases) < 4000 {
		var b strings.Builder
		for n := int(next()%6) + 1; n > 0; n-- {
			b.WriteString(tokens[next()%uint64(len(tokens))])
		}
		cases = append(cases, b.String())
	}
	accepted := 0
	for _, c := range cases {
		if validateRef(c) != nil {
			continue
		}
		accepted++
		out, err := exec.Command("git", "check-ref-format", "--allow-onelevel", c).CombinedOutput()
		assert.NoError(t, err, "validator accepted %q but git refuses it: %s", c, out)
	}
	assert.Greater(t, accepted, 20, "the corpus must contain accepted refs, or the check proves nothing")
}

func TestBranchRefsAreBranchesOrTags(t *testing.T) {
	for _, ok := range []string{"main", "release/1", "refs/heads/main", "refs/tags/v1"} {
		assert.NoError(t, validateBranchRef(ok), ok)
	}
	for _, bad := range []string{"HEAD", "FETCH_HEAD", "refs/remotes/origin/main", "refs/notes/x", "refs/stash", "refs/replace/abc", "refs/x", ".x", "a.lock", "x/.y"} {
		assert.ErrorIs(t, validateBranchRef(bad), ErrInvalidArgument, bad)
	}
	assert.ErrorIs(t, validateRefspec("main:HEAD"), ErrInvalidArgument)
	assert.ErrorIs(t, validateRefspec("main:refs/notes/commits"), ErrInvalidArgument)
	assert.NoError(t, validateRefspec("main:refs/heads/main"))
}
