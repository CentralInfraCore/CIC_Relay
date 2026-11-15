package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
}

// NewExecGitOps creates an ExecGitOps backed by the given registry.
func NewExecGitOps(registry *RepoRegistry) *ExecGitOps {
	return &ExecGitOps{registry: registry}
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
	if err := validateRef(branch); err != nil {
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
	if err := validateRemoteName(remote); err != nil {
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
	return e.runGit(ctx, localPath, "checkout", ref, "--")
}

// Commit stages paths and creates a commit; returns the new commit hash.
func (e *ExecGitOps) Commit(ctx context.Context, repoID, message string, paths []string) (string, error) {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
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
	if err := e.runGit(ctx, localPath, "commit", "-m", message); err != nil {
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
	if err := validateRemoteName(remote); err != nil {
		return err
	}
	if err := validateRefspec(refspec); err != nil {
		return err
	}
	return e.runGit(ctx, localPath, "push", "--", remote, refspec)
}

// Status returns the working-tree status of the registered repo.
func (e *ExecGitOps) Status(ctx context.Context, repoID string) ([]StatusEntry, error) {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
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
func gitEnv(environ []string, allowLocal bool) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if envAllowlist[name] {
			out = append(out, kv)
		}
	}
	return append(out,
		"GIT_CONFIG_NOSYSTEM=1",
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
	fullArgs := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "protocol.ssh.allow=always",
	}, args...)
	if e.allowLocalSources {
		fullArgs = append(fullArgs[:len(fullArgs)-len(args)], append([]string{"-c", "protocol.file.allow=always"}, args...)...)
	}
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = gitEnv(os.Environ(), e.allowLocalSources)

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
		msg := strings.TrimSpace(string(out))
		if e.logSink != nil {
			e.logSink.RecordLog(obs.LogRecord{
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Level:     "WARN",
				Message:   "git op failed",
				Resource:  obs.Resource{Component: "nexus/git"},
				Attrs:     map[string]string{"op": op, "error": msg},
			})
		}
		return "", fmt.Errorf("git %s: %s (%w)", strings.Join(args, " "), msg, runErr)
	}

	out := buf.Bytes()
	if len(out) > maxOutputSize {
		out = out[:maxOutputSize]
	}
	return string(out), nil
}
