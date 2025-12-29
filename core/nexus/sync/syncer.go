package sync

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Syncer defines the interface for synchronizing local state to a central repository.
type Syncer interface {
	// SyncDaily performs the daily squash, push, and prune operations.
	SyncDaily(ctx context.Context, date time.Time) error
}

// GitSyncerConfig holds the configuration for the GitSyncer.
type GitSyncerConfig struct {
	RepoPath    string
	RemoteURL   string
	CacheBranch string // e.g., "cache/date"
	DailyBranch string // e.g., "daily"
}

// GitSyncer implements the Syncer interface using git commands.
type GitSyncer struct {
	config GitSyncerConfig
}

// NewGitSyncer creates a new GitSyncer.
func NewGitSyncer(config GitSyncerConfig) *GitSyncer {
	if config.CacheBranch == "" {
		config.CacheBranch = "cache/date"
	}
	if config.DailyBranch == "" {
		config.DailyBranch = "daily"
	}
	return &GitSyncer{
		config: config,
	}
}

// SyncDaily performs the daily squash and push.
func (s *GitSyncer) SyncDaily(ctx context.Context, date time.Time) error {
	sourceBranch := fmt.Sprintf("%s/%s", s.config.CacheBranch, date.Format("2006-01-02"))

	// 1. Ensure the daily branch exists and is up-to-date
	if err := s.runGit("checkout", s.config.DailyBranch); err != nil {
		// If it doesn't exist, create it from the first commit of the source branch
		if err := s.runGit("checkout", "-b", s.config.DailyBranch, fmt.Sprintf("%s~1", sourceBranch)); err != nil {
			// Fallback if there's only one commit
			if err := s.runGit("checkout", "-b", s.config.DailyBranch, sourceBranch); err != nil {
				return fmt.Errorf("could not create daily branch: %w", err)
			}
		}
	}
	if s.config.RemoteURL != "" {
		if err := s.runGit("pull", s.config.RemoteURL, s.config.DailyBranch); err != nil {
			// Ignore error if the remote branch doesn't exist yet
		}
	}

	// 2. Perform a squash merge from the source branch
	err := s.runGit("merge", "--squash", sourceBranch)
	if err != nil {
		// If there are no new commits, git merge returns an error. We can ignore it.
		if strings.Contains(err.Error(), "Already up to date") {
			fmt.Println("No new commits to merge, skipping sync.")
			return nil
		}
		return fmt.Errorf("squash merge failed: %w", err)
	}

	// 3. Create the squash commit
	commitMsg := fmt.Sprintf("[AUDIT] Daily sync for %s", date.Format("2006-01-02"))
	if err := s.runGit("commit", "-m", commitMsg); err != nil {
		// It might fail if there are no changes, which is not a critical error.
		if strings.Contains(err.Error(), "nothing to commit") {
			fmt.Println("No changes to sync, skipping push.")
			return nil // Nothing to do
		}
		return fmt.Errorf("squash commit failed: %w", err)
	}

	// 4. Push the daily branch to the remote
	if s.config.RemoteURL != "" {
		if err := s.runGit("push", s.config.RemoteURL, s.config.DailyBranch); err != nil {
			return fmt.Errorf("push to remote failed: %w", err)
		}
	}

	// 5. Prune the old local cache branch
	if err := s.runGit("branch", "-D", sourceBranch); err != nil {
		return fmt.Errorf("failed to prune local cache branch %s: %w", sourceBranch, err)
	}

	return nil
}

// runGit executes a git command in the repo directory.
func (s *GitSyncer) runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.config.RepoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %s (%w)", strings.Join(args, " "), string(output), err)
	}
	return nil
}
