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

// F3: configuration that can name a program or rewrite a URL is refused before git acts on it. The
// fsmonitor case is a real execution: git would run the program on `git status`.
func TestRepoConfigThatCanRunProgramsIsRefused(t *testing.T) {
	ctx := context.Background()
	script, marker := markerProgram(t)
	dangerous := map[string]string{
		"core.fsmonitor":               script,
		"core.sshCommand":              script,
		"core.hooksPath":               "/tmp/hooks",
		"core.pager":                   script,
		"core.editor":                  script,
		"core.askPass":                 script,
		"remote.origin.receivepack":    script,
		"remote.origin.uploadpack":     script,
		"remote.origin.proxy":          "http://proxy.invalid",
		"url.https://x/.insteadOf":     "https://y/",
		"url.https://x/.pushInsteadOf": "https://y/",
		"include.path":                 "/tmp/other.cfg",
		"alias.status":                 "!" + script,
		"credential.helper":            script,
		"diff.external":                script,
		"filter.x.clean":               script,
		"merge.x.driver":               script,
		"gpg.program":                  script,
		"protocol.ext.allow":           "always",
		"uploadpack.packObjectsHook":   script,
		"extensions.worktreeConfig":    "true",
	}
	for key, val := range dangerous {
		t.Run(key, func(t *testing.T) {
			ops, work, _ := argTestOps(t)
			resetMarker(marker)
			out, err := exec.Command("git", "-C", work, "config", "--local", key, val).CombinedOutput()
			require.NoError(t, err, string(out))

			_, err = ops.Status(ctx, "r")
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidArgument)
			assert.Contains(t, err.Error(), "not permitted")
			assert.ErrorIs(t, ops.Checkout(ctx, "r", "main"), ErrInvalidArgument)
			assert.ErrorIs(t, ops.Fetch(ctx, "r", "origin"), ErrInvalidArgument)
			assert.ErrorIs(t, ops.Push(ctx, "r", "origin", "main"), ErrInvalidArgument)
			_, err = ops.Commit(ctx, "r", "m", []string{"relay.yaml"})
			assert.ErrorIs(t, err, ErrInvalidArgument)
			requireNoMarker(t, marker)
		})
	}
	t.Run("a repository with only the keys git clone writes passes", func(t *testing.T) {
		ops, _, _ := argTestOps(t)
		_, err := ops.Status(ctx, "r")
		assert.NoError(t, err)
	})
}

// A push URL is a URL: a remote whose fetch URL is fine but whose push URL is not must be refused.
func TestPushURLIsValidatedToo(t *testing.T) {
	ops, work, _ := argTestOps(t)
	out, err := exec.Command("git", "-C", work, "remote", "set-url", "--push", "origin", "ext::sh -c id").CombinedOutput()
	require.NoError(t, err, string(out))
	err = ops.Push(context.Background(), "r", "origin", "main")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	assert.Contains(t, err.Error(), "not allowed")
}

// No global or XDG configuration is read: a hostile or merely different ~/.gitconfig has no effect.
// The test asks git itself, with the exact environment and options the relay uses.
func TestGlobalConfigurationIsNotRead(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[alias]\n\tleak = status\n[user]\n\tsigningkey = leak-global\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".config", "git", "config"), []byte("[user]\n\tname = leak-xdg\n"), 0o644))

	cmd := exec.Command("git", "config", "--list", "--show-origin")
	cmd.Dir = t.TempDir() // outside any repository, so only global-level configuration can show up
	cmd.Env = gitEnv([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}, false, "")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.NotContains(t, string(out), "leak")

	// An operator-designated file is read, and only that.
	cfg := filepath.Join(t.TempDir(), "operator.gitconfig")
	require.NoError(t, os.WriteFile(cfg, []byte("[user]\n\tname = operator-chosen\n"), 0o644))
	cmd = exec.Command("git", "config", "--list")
	cmd.Dir = t.TempDir()
	cmd.Env = gitEnv([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}, false, cfg)
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "operator-chosen")
	assert.NotContains(t, string(out), "leak")
}

func TestOperatorGlobalConfigMustBeSafe(t *testing.T) {
	dir := t.TempDir()
	loose := filepath.Join(dir, "loose")
	require.NoError(t, os.WriteFile(loose, []byte(""), 0o666))
	require.NoError(t, os.Chmod(loose, 0o666))
	tight := filepath.Join(dir, "tight")
	require.NoError(t, os.WriteFile(tight, []byte(""), 0o644))

	assert.NoError(t, checkGlobalConfigFile(""))
	assert.NoError(t, checkGlobalConfigFile(tight))
	assert.Error(t, checkGlobalConfigFile(loose), "writable by others")
	assert.Error(t, checkGlobalConfigFile(filepath.Join(dir, "missing")))
	assert.Error(t, checkGlobalConfigFile(dir), "a directory is not a config file")
	assert.Error(t, checkGlobalConfigFile("relative/path"))

	ops, _, _ := argTestOps(t)
	ops.globalConfig = loose
	_, err := ops.Status(context.Background(), "r")
	assert.Error(t, err, "a configured but unsafe global file disables git operations")
}

// Dropping the global configuration must not break a repository owned by another account:
// safe.directory is given on the command line for the registered path. Needs root to create the case.
func TestSafeDirectoryStillWorksForForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to hand the repository to another uid")
	}
	ops, work, _ := argTestOps(t)
	out, err := exec.Command("chown", "-R", "65534:65534", work).CombinedOutput()
	require.NoError(t, err, string(out))

	// Without the option git refuses ("dubious ownership"); that is the case being handled.
	raw := exec.Command("git", "-C", work, "status")
	raw.Env = gitEnv([]string{"PATH=" + os.Getenv("PATH")}, false, "")
	_, rawErr := raw.CombinedOutput()
	require.Error(t, rawErr, "precondition: git must refuse a repository owned by another uid")

	_, err = ops.Status(context.Background(), "r")
	assert.NoError(t, err)
}

// A remote may have several URLs, and a push goes to all of them. The first being fine proves nothing
// about the rest, so each one is validated, for fetch URLs and for push URLs alike. Each case names the
// operations that must refuse it; the others are not affected by that URL.
func TestEveryURLOfARemoteIsValidated(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		refuses []string
		args    [][]string
	}{
		{"second fetch url (no push url, so a push uses it too)", []string{"fetch", "push"}, [][]string{{"config", "--add", "remote.origin.url", "ext::sh -c id"}}},
		{"second push url", []string{"push"}, [][]string{{"remote", "set-url", "--push", "origin", "https://example.invalid/p1.git"}, {"remote", "set-url", "--push", "--add", "origin", "https://user:pw@example.invalid/p2.git"}}},
		{"second push url, other transport", []string{"push"}, [][]string{{"remote", "set-url", "--push", "origin", "https://example.invalid/p1.git"}, {"remote", "set-url", "--push", "--add", "origin", "file:///tmp/elsewhere"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops, work, _ := argTestOps(t)
			strict := NewExecGitOps(ops.registry)
			// The fixture's origin is a local path, which strict mode refuses on its own; start from a
			// URL that passes, so that only the added URL can be the reason for a refusal.
			out, err := exec.Command("git", "-C", work, "remote", "set-url", "origin", "https://example.invalid/a.git").CombinedOutput()
			require.NoError(t, err, string(out))
			for _, op := range []string{"fetch", "push"} {
				require.NoError(t, strict.requireConfiguredRemote(ctx, work, "origin", op), "precondition: a single https URL passes for %s", op)
			}
			for _, a := range tc.args {
				out, err := exec.Command("git", append([]string{"-C", work}, a...)...).CombinedOutput()
				require.NoError(t, err, string(out))
			}
			for _, op := range tc.refuses {
				err := strict.requireConfiguredRemote(ctx, work, "origin", op)
				require.Error(t, err, op)
				assert.ErrorIs(t, err, ErrInvalidArgument, op)
				assert.Contains(t, err.Error(), "not allowed", op)
				assert.NotContains(t, err.Error(), "pw@", op)
			}
			// And through the public operations.
			if len(tc.refuses) == 2 {
				assert.ErrorIs(t, strict.Fetch(ctx, "r", "origin"), ErrInvalidArgument)
			}
			assert.ErrorIs(t, strict.Push(ctx, "r", "origin", "main"), ErrInvalidArgument)
		})
	}
}

// Fetch and push have different targets, and each is validated for the operation that uses it: a bad
// push URL must not block a fetch, and a good push URL must not excuse a bad fetch URL for the fetch.
// All push URLs count, and no URL at all is a refusal.
func TestRemoteTargetsAreValidatedPerOperation(t *testing.T) {
	ctx := context.Background()
	const good, bad = "https://example.invalid/ok.git", "ext::sh -c id"
	setup := func(t *testing.T, cmds ...[]string) (*ExecGitOps, string) {
		ops, work, _ := argTestOps(t)
		strict := NewExecGitOps(ops.registry)
		for _, c := range cmds {
			out, err := exec.Command("git", append([]string{"-C", work}, c...)...).CombinedOutput()
			require.NoError(t, err, string(out))
		}
		return strict, work
	}
	fetchOK := func(s *ExecGitOps, w string) error { return s.requireConfiguredRemote(ctx, w, "origin", "fetch") }
	pushOK := func(s *ExecGitOps, w string) error { return s.requireConfiguredRemote(ctx, w, "origin", "push") }

	t.Run("good fetch URL, bad push URL: fetch passes, push refused", func(t *testing.T) {
		s, w := setup(t, []string{"remote", "set-url", "origin", good}, []string{"remote", "set-url", "--push", "origin", bad})
		assert.NoError(t, fetchOK(s, w))
		err := pushOK(s, w)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidArgument)
		assert.Contains(t, err.Error(), "push URL that is not allowed")
	})
	t.Run("bad fetch URL, good push URL: push passes, fetch refused", func(t *testing.T) {
		s, w := setup(t, []string{"remote", "set-url", "origin", bad}, []string{"remote", "set-url", "--push", "origin", good})
		assert.NoError(t, pushOK(s, w))
		err := fetchOK(s, w)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch URL that is not allowed")
	})
	t.Run("several push URLs: every one must pass", func(t *testing.T) {
		s, w := setup(t, []string{"remote", "set-url", "origin", good},
			[]string{"remote", "set-url", "--push", "origin", good},
			[]string{"remote", "set-url", "--push", "--add", "origin", "https://example.invalid/two.git"},
			[]string{"remote", "set-url", "--push", "--add", "origin", bad})
		assert.NoError(t, fetchOK(s, w))
		assert.Error(t, pushOK(s, w), "the third push URL is not allowed")
	})
	t.Run("no push URL: a push uses the fetch URL(s), all of them", func(t *testing.T) {
		s, w := setup(t, []string{"remote", "set-url", "origin", good}, []string{"remote", "set-url", "--add", "origin", bad})
		assert.Error(t, pushOK(s, w))
		assert.Error(t, fetchOK(s, w))
	})
	t.Run("an unknown operation is refused", func(t *testing.T) {
		s, w := setup(t, []string{"remote", "set-url", "origin", good})
		assert.ErrorIs(t, s.requireConfiguredRemote(ctx, w, "origin", "clone"), ErrInvalidArgument)
	})
}

// A remote with no URL at all is reported by git as its own name, which git would then read as a path.
// The URL policy refuses that, with local sources on or off. (The "no URL" branch in the validator is
// therefore defence in depth: git never prints an empty list for a configured remote.)
func TestRemoteWithoutURLIsRefused(t *testing.T) {
	ctx := context.Background()
	ops, work, _ := argTestOps(t)
	out, err := exec.Command("git", "-C", work, "config", "remote.nourl.fetch", "+refs/heads/*:refs/remotes/nourl/*").CombinedOutput()
	require.NoError(t, err, string(out))
	for _, o := range []*ExecGitOps{ops, NewExecGitOps(ops.registry)} {
		for _, op := range []string{"fetch", "push"} {
			err := o.requireConfiguredRemote(ctx, work, "nourl", op)
			require.Error(t, err, op)
			assert.ErrorIs(t, err, ErrInvalidArgument, op)
			assert.Contains(t, err.Error(), "not allowed", op)
		}
	}
}
