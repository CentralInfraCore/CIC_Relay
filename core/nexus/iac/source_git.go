package iac

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GitSourceConfig configures a GitSource.
type GitSourceConfig struct {
	URL        string        // git remote URL (CIC_GIT_SOURCE_URL)
	Branch     string        // branch to track; default: "main"
	LocalPath  string        // local clone destination (CIC_GIT_SOURCE_LOCAL_PATH)
	SSHKeyPath string        // optional path to SSH private key
	Timeout    time.Duration // per-command timeout; default: 30s
}

// GitSource implements IaCSource by cloning or fetching a git repository
// and delegating to FileSource for file enumeration.
//
// All git commands run with a hardened environment:
//   - GIT_CONFIG_NOSYSTEM=1  (no /etc/gitconfig)
//   - GIT_TERMINAL_PROMPT=0  (no interactive prompts)
//   - GIT_ASKPASS=/bin/false (credential prompts fail fast)
//   - -c core.hooksPath=/dev/null (no hooks)
type GitSource struct {
	cfg GitSourceConfig
}

// NewGitSource creates a new GitSource. Branch defaults to "main" if empty;
// Timeout defaults to 30s if zero.
func NewGitSource(cfg GitSourceConfig) *GitSource {
	if cfg.Branch == "" {
		cfg.Branch = "main"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &GitSource{cfg: cfg}
}

// FetchConfig ensures the repository is present and up-to-date, then
// returns all YAML/JSON files via FileSource.FetchConfig.
func (s *GitSource) FetchConfig(ctx context.Context) (map[string][]byte, error) {
	if _, err := os.Stat(s.cfg.LocalPath); os.IsNotExist(err) {
		// Repo not present — clone it.
		if err := s.runGit(ctx, "", "clone",
			"--branch", s.cfg.Branch,
			"--depth", "1",
			s.cfg.URL,
			s.cfg.LocalPath,
		); err != nil {
			return nil, fmt.Errorf("git clone failed: %w", err)
		}
	} else {
		// Repo exists — fetch latest and reset to remote branch.
		if err := s.runGit(ctx, s.cfg.LocalPath, "fetch", "origin"); err != nil {
			return nil, fmt.Errorf("git fetch failed: %w", err)
		}
		if err := s.runGit(ctx, s.cfg.LocalPath, "checkout", s.cfg.Branch); err != nil {
			return nil, fmt.Errorf("git checkout failed: %w", err)
		}
	}

	fs := FileSource{RootPath: s.cfg.LocalPath}
	return fs.FetchConfig(ctx)
}

// runGit executes a git command with a hardened environment and per-call timeout.
// dir is the working directory; empty string means the process inherits the caller's cwd.
func (s *GitSource) runGit(ctx context.Context, dir string, args ...string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	// -c core.hooksPath=/dev/null must come before the subcommand.
	fullArgs := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.CommandContext(cmdCtx, "git", fullArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
	)
	if s.cfg.SSHKeyPath != "" {
		cmd.Env = append(cmd.Env,
			"GIT_SSH_COMMAND=ssh -i "+s.cfg.SSHKeyPath+" -o StrictHostKeyChecking=no",
		)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %s (%w)",
			strings.Join(args, " "), strings.TrimSpace(string(output)), err)
	}
	return nil
}
