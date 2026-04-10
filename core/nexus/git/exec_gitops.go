package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
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
	registry *RepoRegistry
}

// NewExecGitOps creates an ExecGitOps backed by the given registry.
func NewExecGitOps(registry *RepoRegistry) *ExecGitOps {
	return &ExecGitOps{registry: registry}
}

// Clone clones url into the local path registered for repoID.
func (e *ExecGitOps) Clone(ctx context.Context, repoID, url, branch string) error {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return err
	}
	return e.runGit(ctx, "", "clone", "--branch", branch, "--depth", "1", url, localPath)
}

// Fetch fetches the given remote for the registered repo.
func (e *ExecGitOps) Fetch(ctx context.Context, repoID, remote string) error {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return err
	}
	return e.runGit(ctx, localPath, "fetch", remote)
}

// Checkout checks out the given ref in the registered repo.
func (e *ExecGitOps) Checkout(ctx context.Context, repoID, ref string) error {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return err
	}
	return e.runGit(ctx, localPath, "checkout", ref)
}

// Commit stages paths and creates a commit; returns the new commit hash.
func (e *ExecGitOps) Commit(ctx context.Context, repoID, message string, paths []string) (string, error) {
	localPath, err := e.registry.Resolve(repoID)
	if err != nil {
		return "", err
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
	return e.runGit(ctx, localPath, "push", remote, refspec)
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

	// Prepend -c core.hooksPath=/dev/null before the subcommand.
	fullArgs := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_AUTHOR_NAME=CIC Nexus",
		"GIT_AUTHOR_EMAIL=nexus@cic-relay.local",
		"GIT_COMMITTER_NAME=CIC Nexus",
		"GIT_COMMITTER_EMAIL=nexus@cic-relay.local",
	)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Run(); err != nil {
		out := buf.Bytes()
		// Truncate to 1MB to bound memory usage in error messages.
		if len(out) > maxOutputSize {
			out = out[:maxOutputSize]
		}
		return "", fmt.Errorf("git %s: %s (%w)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}

	out := buf.Bytes()
	if len(out) > maxOutputSize {
		out = out[:maxOutputSize]
	}
	return string(out), nil
}
