package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"centralrelay/pkg/obs"
)

const (
	// maxOutputSize limits the amount of git output captured in memory.
	maxOutputSize = 1 << 20 // 1 MB

	// defaultTimeout is applied per git command when the context has no deadline.
	defaultTimeout = 30 * time.Second
)

// ExecGitOps implements GitOps using os/exec("git", ...).
// All commands run with a hardened environment (no system config,
// no interactive prompts, no hooks).
type ExecGitOps struct {
	registry   *RepoRegistry
	logSink    obs.LogSink
	metricSink obs.MetricSink

	allowLocalSources bool
	// globalConfig is an operator-designated git configuration file (CIC_GIT_GLOBAL_CONFIG). Without
	// it git reads no global configuration at all. It is never derived from anything a guest sends.
	globalConfig string
}

// NewExecGitOps creates an ExecGitOps backed by the given registry.
func NewExecGitOps(registry *RepoRegistry) *ExecGitOps {
	return &ExecGitOps{registry: registry, globalConfig: os.Getenv("CIC_GIT_GLOBAL_CONFIG")}
}

// SetObsSinks wires observability sinks into ExecGitOps.
// Both arguments are nil-safe: passing nil disables the corresponding emissions.
func (e *ExecGitOps) SetObsSinks(logSink obs.LogSink, metricSink obs.MetricSink) {
	e.logSink = logSink
	e.metricSink = metricSink
}

// Clone clones url into the local path registered for repoID.
func (e *ExecGitOps) Clone(ctx context.Context, repoID, url, branch string) error {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return err
	}
	if err := validateCloneURL(url, e.allowLocalSources); err != nil {
		return err
	}
	if err := validateBranchRef(branch); err != nil {
		return err
	}
	return e.runGit(ctx, "", "clone", "--branch", branch, "--depth", "1", "--", url, localPath)
}

// Fetch fetches the given remote for the registered repo.
func (e *ExecGitOps) Fetch(ctx context.Context, repoID, remote string) error {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return err
	}
	if err := e.auditRepoConfig(ctx, localPath); err != nil {
		return err
	}
	if err := e.requireConfiguredRemote(ctx, localPath, remote, "fetch"); err != nil {
		return err
	}
	return e.runGit(ctx, localPath, "fetch", "--", remote)
}

// Checkout checks out the given ref in the registered repo.
func (e *ExecGitOps) Checkout(ctx context.Context, repoID, ref string) error {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return err
	}
	if err := validateRef(ref); err != nil {
		return err
	}
	if err := e.auditRepoConfig(ctx, localPath); err != nil {
		return err
	}
	return e.runGit(ctx, localPath, "checkout", ref, "--")
}

// Commit stages paths and creates a commit; returns the new commit hash.
func (e *ExecGitOps) Commit(ctx context.Context, repoID, message string, paths []string) (string, error) {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return "", err
	}

	if err := e.auditRepoConfig(ctx, localPath); err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("commit: no paths")
	}
	for _, p := range paths {
		if err := validatePath(p); err != nil {
			return "", err
		}
	}
	addArgs := append([]string{"add", "--"}, paths...)
	if err := e.runGit(ctx, localPath, addArgs...); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}
	// --only with the explicit paths: the commit holds exactly the paths this request named. Anything
	// staged earlier by someone else stays staged and out of this commit.
	commitArgs := append([]string{"commit", "-m", message, "--only", "--"}, paths...)
	if err := e.runGit(ctx, localPath, commitArgs...); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}

	hash, err := e.runGitOutput(ctx, localPath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(hash), nil
}

// Push pushes refspec to remote for the registered repo.
func (e *ExecGitOps) Push(ctx context.Context, repoID, remote, refspec string) error {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return err
	}
	src, dst, err := CanonicalPushRefspec(refspec)
	if err != nil {
		return err
	}
	if err := e.auditRepoConfig(ctx, localPath); err != nil {
		return err
	}
	if err := e.requireConfiguredRemote(ctx, localPath, remote, "push"); err != nil {
		return err
	}
	// A qualified name is still not an exact reference to git: it falls back to other ref names ("refs/heads/x"
	// is found as the tag "refs/tags/refs/heads/x" when no such branch exists), on both ends of a push. So
	// neither end is handed to git as a name that git may reinterpret:
	//   - the source is resolved HERE, to the exact ref (git show-ref --verify takes no fallbacks) and its
	//     object id; the object id is what is pushed, so what was checked is what is read;
	//   - the destination is checked against the remote's own refs: if the remote holds any other ref that
	//     git could take the destination for, the push is refused.
	oid, err := e.resolveExactRef(ctx, localPath, src)
	if err != nil {
		return err
	}
	if err := e.requireUnambiguousRemoteRef(ctx, localPath, remote, dst); err != nil {
		return err
	}
	return e.runGit(ctx, localPath, "push", "--", remote, oid+":"+dst)
}

// resolveExactRef returns the object id of the local ref named exactly refname ("refs/heads/..." or
// "refs/tags/..."), or an error if there is no such ref. "git show-ref --verify" requires the exact ref path:
// it does not try the other places a short or qualified name could also be looked up.
func (e *ExecGitOps) resolveExactRef(ctx context.Context, localPath, refname string) (string, error) {
	out, err := e.runGitOutput(ctx, localPath, "show-ref", "--verify", "--hash", "--", refname)
	if err != nil {
		return "", fmt.Errorf("%w: source ref %q does not exist exactly as named", ErrInvalidArgument, clip(refname))
	}
	oid := strings.TrimSpace(out)
	if len(oid) != 40 && len(oid) != 64 || strings.Trim(oid, "0123456789abcdef") != "" {
		return "", fmt.Errorf("%w: source ref %q did not resolve to an object id", ErrInvalidArgument, clip(refname))
	}
	return oid, nil
}

// requireUnambiguousRemoteRef refuses a push whose destination refname git could match to a different ref
// of the remote. Given "refs/heads/x", git also accepts the remote's "refs/refs/heads/x",
// "refs/tags/refs/heads/x", "refs/heads/refs/heads/x", "refs/remotes/refs/heads/x" and ".../HEAD" for it, and
// updates that one when it is the only match. Everything that could be taken for the destination is asked
// of the remote; the only acceptable answer is nothing, or exactly the destination.
func (e *ExecGitOps) requireUnambiguousRemoteRef(ctx context.Context, localPath, remote, refname string) error {
	out, err := e.runGitOutput(ctx, localPath, "ls-remote", "--refs", "--", remote, refname, refname+"/HEAD")
	if err != nil {
		return fmt.Errorf("read the remote's refs: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		_, name, ok := strings.Cut(line, "\t")
		if !ok || name != refname {
			return fmt.Errorf("%w: the remote holds another ref that %q could be taken for; refusing", ErrInvalidArgument, clip(refname))
		}
	}
	return nil
}

// Status returns the working-tree status of the registered repo.
func (e *ExecGitOps) Status(ctx context.Context, repoID string) ([]StatusEntry, error) {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return nil, err
	}

	if err := e.auditRepoConfig(ctx, localPath); err != nil {
		return nil, err
	}
	out, err := e.runGitOutput(ctx, localPath, "status", "--porcelain")
	if err != nil {
		return nil, err
	}

	return parsePortcelainStatus(out), nil
}

// parsePortcelainStatus parses the output of `git status --porcelain`.
func parsePortcelainStatus(output string) []StatusEntry {
	var entries []StatusEntry
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 4 {
			continue
		}
		state := line[:2]
		// Porcelain format: "XY path" or "XY orig -> dest" for renames.
		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			parts := strings.SplitN(path, " -> ", 2)
			path = parts[1]
		}
		entries = append(entries, StatusEntry{Path: path, State: state})
	}
	return entries
}

// repoConfigAllowed lists the repository-local configuration keys a registered repository may carry.
// Anything else is refused: git configuration can name programs to run (core.sshCommand, core.fsmonitor,
// remote.<n>.receivepack, filter and diff drivers, credential helpers, aliases, includes) and can rewrite
// URLs (url.<base>.insteadOf), which would undo the transport and URL policy. The list is what
// "git clone" itself writes, plus remote and branch tracking.
var repoConfigAllowed = []*regexp.Regexp{
	regexp.MustCompile(`^core\.(repositoryformatversion|filemode|bare|logallrefupdates|ignorecase|precomposeunicode|symlinks)$`),
	regexp.MustCompile(`^remote\.[A-Za-z0-9][A-Za-z0-9._-]*\.(url|pushurl|fetch)$`),
	regexp.MustCompile(`^branch\.[^\x00]+\.(remote|merge)$`),
	regexp.MustCompile(`^user\.(name|email)$`),
}

// auditRepoConfig reads the repository's local configuration and refuses it if it carries a key outside
// repoConfigAllowed. It runs before any operation that would otherwise let git act on that configuration.
func (e *ExecGitOps) auditRepoConfig(ctx context.Context, localPath string) error {
	out, err := e.runGitOutput(ctx, localPath, "config", "--local", "--list", "-z")
	if err != nil {
		return fmt.Errorf("read repository configuration: %w", err)
	}
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		key, _, _ := strings.Cut(entry, "\n")
		key = strings.ToLower(key)
		allowed := false
		for _, re := range repoConfigAllowed {
			if re.MatchString(key) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("%w: repository configuration key %q is not permitted", ErrInvalidArgument, clip(key))
		}
	}
	return nil
}

// checkGlobalConfigFile validates the operator-designated global configuration file, if one is set: an
// absolute path to a regular file that only its owner can write. Anything else disables git operations
// (fail closed) instead of being read.
func checkGlobalConfigFile(path string) error {
	if path == "" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil || !filepath.IsAbs(path) || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("git: CIC_GIT_GLOBAL_CONFIG must be an absolute path to a regular file not writable by group or others")
	}
	return nil
}

// requireConfiguredRemote proves that name is a remote of this repository, that the repository's
// configuration is one we accept, and that every target the operation will use passes the URL policy.
// A syntactically valid name is not evidence that the remote is configured (git would read an unknown
// name as a path or a URL), so the repository is asked.
func (e *ExecGitOps) requireConfiguredRemote(ctx context.Context, localPath, name, op string) error {
	if err := validateRemoteName(name); err != nil {
		return err
	}
	if err := e.auditRepoConfig(ctx, localPath); err != nil {
		return err
	}
	out, err := e.runGitOutput(ctx, localPath, "remote")
	if err != nil {
		return fmt.Errorf("list remotes: %w", err)
	}
	configured := false
	for _, line := range strings.Split(out, "\n") {
		if line == name {
			configured = true
		}
	}
	if !configured {
		return fmt.Errorf("%w: remote %q is not configured in this repository", ErrInvalidArgument, clip(name))
	}
	return e.validateRemoteTargets(ctx, localPath, name, op)
}

// validateRemoteTargets checks the targets of a remote for one operation, at two levels that must agree:
//
//  1. the stored values, as raw NUL-delimited records, with include files and every other configuration
//     scope git itself reads applied (so a pushurl that lives in an included file or in the worktree
//     configuration is seen), each validated exactly as stored;
//  2. the effective targets git will actually use, from "git remote get-url", which apply insteadOf and
//     pushInsteadOf rewriting, each validated as well.
//
// The two lists must have the same number of entries: a difference means the target was rewritten into
// something else or a value contains a line break, and either way the operation is refused. The URL policy
// is applied to what git will contact, not only to what was written down.
func (e *ExecGitOps) validateRemoteTargets(ctx context.Context, localPath, name, op string) error {
	var stored []string
	var getArgs []string
	var err error
	switch op {
	case "fetch":
		stored, err = e.remoteConfigValues(ctx, localPath, "remote."+name+".url")
		getArgs = []string{"remote", "get-url", "--all", "--", name}
	case "push":
		// A push uses the push URLs when there are any, otherwise the URLs, as git does.
		stored, err = e.remoteConfigValues(ctx, localPath, "remote."+name+".pushurl")
		if err == nil && len(stored) == 0 {
			stored, err = e.remoteConfigValues(ctx, localPath, "remote."+name+".url")
		}
		getArgs = []string{"remote", "get-url", "--push", "--all", "--", name}
	default:
		return fmt.Errorf("%w: unknown remote operation %q", ErrInvalidArgument, clip(op))
	}
	if err != nil {
		return fmt.Errorf("read remote %s targets: %w", op, err)
	}
	if len(stored) == 0 {
		return fmt.Errorf("%w: remote %q has no %s target URL", ErrInvalidArgument, clip(name), op)
	}
	for _, v := range stored {
		if err := validateCloneURL(v, e.allowLocalSources); err != nil {
			return fmt.Errorf("remote %q has a stored %s URL that is not allowed: %w", clip(name), op, err)
		}
	}

	raw, err := e.runGitOutput(ctx, localPath, getArgs...)
	if err != nil {
		return fmt.Errorf("read effective %s targets: %w", op, err)
	}
	raw = strings.TrimSuffix(raw, "\n") // exactly the one terminator git prints; nothing else is removed
	effective := strings.Split(raw, "\n")
	if len(effective) != len(stored) {
		return fmt.Errorf("%w: remote %q: the %s targets git will use differ from the stored ones", ErrInvalidArgument, clip(name), op)
	}
	for _, v := range effective {
		if err := validateCloneURL(v, e.allowLocalSources); err != nil {
			return fmt.Errorf("remote %q has an effective %s target that is not allowed: %w", clip(name), op, err)
		}
	}
	return nil
}

// remoteConfigValues returns every value of a multi-valued key as NUL-delimited records (git config -z),
// so one record is exactly one stored value however many lines it contains. It reads what git itself
// would read: all configuration scopes that are enabled, with include files applied. An absent key is an
// empty result, not an error.
func (e *ExecGitOps) remoteConfigValues(ctx context.Context, localPath, key string) ([]string, error) {
	out, err := e.runGitOutput(ctx, localPath, "config", "--includes", "--get-all", "-z", key)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil // git config exits 1 when the key has no value
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	records := strings.Split(out, "\x00")
	if records[len(records)-1] == "" { // -z terminates every record, including the last
		records = records[:len(records)-1]
	}
	return records, nil
}

// envAllowlist names the only variables a git child inherits from the relay process. Everything else
// (API tokens, Vault credentials, CIC_* settings, GIT_* overrides) stays with the relay: git, and the
// ssh/credential helpers it starts, have no use for them, and a transport that misbehaves must not be
// able to read them. These are the variables git needs to find itself, its home, a proxy or a CA bundle.
var envAllowlist = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "TZ": true,
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true,
	"SSH_AUTH_SOCK": true, "GIT_SSH": true, "GIT_SSH_COMMAND": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "GIT_SSL_CAINFO": true,
}

// gitEnv builds the child environment: the allowlisted inherited variables, then the fixed hardening
// values, which are appended last and so win over anything inherited.
func gitEnv(environ []string, allowLocal bool, globalConfig string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if envAllowlist[name] {
			out = append(out, kv)
		}
	}
	if globalConfig == "" {
		globalConfig = "/dev/null"
	}
	return append(out,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+globalConfig,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_AUTHOR_NAME=CIC Nexus",
		"GIT_AUTHOR_EMAIL=nexus@cic-relay.local",
		"GIT_COMMITTER_NAME=CIC Nexus",
		"GIT_COMMITTER_EMAIL=nexus@cic-relay.local",
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_ALLOW_PROTOCOL=https:ssh"+localProtocol(allowLocal),
	)
}

func localProtocol(allow bool) string {
	if allow {
		return ":file"
	}
	return ""
}

// runGit executes a git command with a hardened environment.
// dir is the working directory; empty string means the caller's working directory.
func (e *ExecGitOps) runGit(ctx context.Context, dir string, args ...string) error {
	_, err := e.runGitOutputDir(ctx, dir, args...)
	return err
}

// runGitOutput executes a git command and returns its stdout as a string.
func (e *ExecGitOps) runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	return e.runGitOutputDir(ctx, dir, args...)
}

func (e *ExecGitOps) runGitOutputDir(ctx context.Context, dir string, args ...string) (string, error) {
	// Ensure a deadline so we don't hang indefinitely.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	// Prepend the hardening configuration before the subcommand: no hooks, and only the https and ssh
	// transports (no file, no ext helper), whatever a URL or the repository configuration says.
	pre := []string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "protocol.ssh.allow=always",
	}
	if e.allowLocalSources {
		pre = append(pre, "-c", "protocol.file.allow=always")
	}
	if dir != "" {
		// safe.directory is honoured on the command line, so a registered repository owned by another
		// account keeps working although no global configuration is read.
		pre = append(pre, "-c", "safe.directory="+dir)
	}
	if err := checkGlobalConfigFile(e.globalConfig); err != nil {
		return "", err
	}
	fullArgs := append(pre, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = gitEnv(os.Environ(), e.allowLocalSources, e.globalConfig)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	op := args[0] // subcommand name (clone, fetch, status, …)
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	if e.metricSink != nil {
		e.metricSink.RecordHistogram(obs.HistogramPoint{
			Name:      "relay.git.op_duration_ms",
			Sum:       float64(elapsed.Milliseconds()),
			Count:     1,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Resource:  obs.Resource{Component: "nexus/git"},
			Attrs:     map[string]string{"op": op},
		})
	}

	if runErr != nil {
		out := buf.Bytes()
		if len(out) > maxOutputSize {
			out = out[:maxOutputSize]
		}
		msg := scrubURLs(strings.TrimSpace(string(out)))
		if e.logSink != nil {
			e.logSink.RecordLog(obs.LogRecord{
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Level:     "WARN",
				Message:   "git op failed",
				Resource:  obs.Resource{Component: "nexus/git"},
				Attrs:     map[string]string{"op": op, "error": msg},
			})
		}
		return "", fmt.Errorf("git %s: %s (%w)", scrubURLs(strings.Join(args, " ")), msg, runErr)
	}

	out := buf.Bytes()
	if len(out) > maxOutputSize {
		out = out[:maxOutputSize]
	}
	return string(out), nil
}
