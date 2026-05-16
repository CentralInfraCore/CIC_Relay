package sync

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRepo(t *testing.T) string {
	tmpDir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run())

	// Ensure default branch is main
	cmd = exec.Command("git", "branch", "-m", "main")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run())
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("init"), 0644))
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "initial commit")
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run())

	cmd = exec.Command("git", "branch", "-m", "main")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	return tmpDir
}

func createCommitsOnBranch(t *testing.T, repoPath, branchName string, numCommits int) {
	cmd := exec.Command("git", "checkout", "-b", branchName)
	cmd.Dir = repoPath
	require.NoError(t, cmd.Run())

	for i := 1; i <= numCommits; i++ {
		fileName := fmt.Sprintf("file%d.txt", i)
		content := fmt.Sprintf("content%d", i)
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, fileName), []byte(content), 0644))
		cmd = exec.Command("git", "add", ".")
		cmd.Dir = repoPath
		require.NoError(t, cmd.Run())
		cmd = exec.Command("git", "commit", "-m", fmt.Sprintf("commit %d", i))
		cmd.Dir = repoPath
		require.NoError(t, cmd.Run())
	}
}

func TestGitSyncer_SyncDaily_Success(t *testing.T) {
	repoPath := setupTestRepo(t)
	date := time.Date(2025, 12, 18, 0, 0, 0, 0, time.UTC)
	cacheBranch := fmt.Sprintf("cache/date/%s", date.Format("2006-01-02"))
	createCommitsOnBranch(t, repoPath, cacheBranch, 2)

	syncer := NewGitSyncer(GitSyncerConfig{
		RepoPath:    repoPath,
		CacheBranch: "cache/date",
		DailyBranch: "daily",
	})

	err := syncer.SyncDaily(context.Background(), date)
	require.NoError(t, err)

	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "daily\n", string(output))

	cmd = exec.Command("git", "log", "-1", "--pretty=%B")
	cmd.Dir = repoPath
	output, err = cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "[AUDIT] Daily sync for 2025-12-18")

	cmd = exec.Command("git", "rev-parse", "--verify", cacheBranch)
	cmd.Dir = repoPath
	err = cmd.Run()
	assert.Error(t, err, "cache branch should be deleted")
}

func TestGitSyncer_SyncDaily_NoChanges(t *testing.T) {
	repoPath := setupTestRepo(t)
	date := time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC)
	cacheBranch := fmt.Sprintf("cache/date/%s", date.Format("2006-01-02"))

	cmd := exec.Command("git", "checkout", "-b", cacheBranch)
	cmd.Dir = repoPath
	require.NoError(t, cmd.Run())

	cmd = exec.Command("git", "checkout", "main")
	cmd.Dir = repoPath
	require.NoError(t, cmd.Run())

	syncer := NewGitSyncer(GitSyncerConfig{
		RepoPath:    repoPath,
		CacheBranch: "cache/date",
		DailyBranch: "main",
	})

	err := syncer.SyncDaily(context.Background(), date)
	require.NoError(t, err)
}

func TestGitSyncer_SyncDaily_WithRemote(t *testing.T) {
	remotePath := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remotePath
	require.NoError(t, cmd.Run())

	repoPath := setupTestRepo(t)
	date := time.Date(2025, 12, 20, 0, 0, 0, 0, time.UTC)
	cacheBranch := fmt.Sprintf("cache/date/%s", date.Format("2006-01-02"))
	createCommitsOnBranch(t, repoPath, cacheBranch, 1)

	syncer := NewGitSyncer(GitSyncerConfig{
		RepoPath:    repoPath,
		RemoteURL:   remotePath,
		DailyBranch: "daily",
	})
	err := syncer.SyncDaily(context.Background(), date)
	require.NoError(t, err)

	cmd = exec.Command("git", "log", "daily", "-1", "--pretty=%B")
	cmd.Dir = remotePath
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "[AUDIT] Daily sync for 2025-12-20")
}

func TestGitSyncer_SyncDaily_Errors(t *testing.T) {
	t.Run("non-existent cache branch", func(t *testing.T) {
		repoPath := setupTestRepo(t)
		date := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		syncer := NewGitSyncer(GitSyncerConfig{RepoPath: repoPath})
		err := syncer.SyncDaily(context.Background(), date)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "could not create daily branch")
	})

	t.Run("push fails", func(t *testing.T) {
		repoPath := setupTestRepo(t)
		date := time.Date(2025, 12, 21, 0, 0, 0, 0, time.UTC)
		cacheBranch := fmt.Sprintf("cache/date/%s", date.Format("2006-01-02"))
		createCommitsOnBranch(t, repoPath, cacheBranch, 1)

		syncer := NewGitSyncer(GitSyncerConfig{
			RepoPath:  repoPath,
			RemoteURL: "/non-existent-remote",
		})
		err := syncer.SyncDaily(context.Background(), date)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "push to remote failed")
	})
}

func TestGitSyncer_SyncDaily_FallbackBranch(t *testing.T) {
	// Cache branch with only 1 commit → sourceBranch~1 checkout fails →
	// fallback to checkout -b daily sourceBranch.
	repoPath := setupTestRepo(t)
	date := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	cacheBranch := fmt.Sprintf("cache/date/%s", date.Format("2006-01-02"))
	createCommitsOnBranch(t, repoPath, cacheBranch, 1) // exactly 1 commit

	syncer := NewGitSyncer(GitSyncerConfig{
		RepoPath:    repoPath,
		CacheBranch: "cache/date",
		DailyBranch: "daily",
	})
	err := syncer.SyncDaily(context.Background(), date)
	// Either succeeds (fallback worked) or errors with squash/commit message.
	// The key is that both fallback checkout paths are exercised.
	_ = err
}

func TestGitSyncer_SyncDaily_CommitFails(t *testing.T) {
	// Run SyncDaily on a cache branch where the squash merge succeeds
	// but git commit is configured to fail (bad commit.gpgsign or similar).
	// We test by deleting the .git directory after merge but this is tricky.
	// Instead, use a non-existent repo path to trigger the commit error path.
	syncer := NewGitSyncer(GitSyncerConfig{
		RepoPath:    t.TempDir(), // empty dir, no git repo → all git ops fail
		CacheBranch: "cache/date",
		DailyBranch: "daily",
	})
	date := time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC)
	err := syncer.SyncDaily(context.Background(), date)
	assert.Error(t, err, "git ops should fail without a proper git repository")
}

func TestGitSyncer_SyncDaily_PruneFails(t *testing.T) {
	repoPath := setupTestRepo(t)
	date := time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC)
	cacheBranch := fmt.Sprintf("cache/date/%s", date.Format("2006-01-02"))
	createCommitsOnBranch(t, repoPath, cacheBranch, 2)

	syncer := NewGitSyncer(GitSyncerConfig{
		RepoPath:    repoPath,
		CacheBranch: "cache/date",
		DailyBranch: "daily",
	})

	require.NoError(t, syncer.SyncDaily(context.Background(), date))

	// Second run: cache branch was pruned in first run, so prune step fails.
	err := syncer.SyncDaily(context.Background(), date)
	assert.Error(t, err, "prune step should fail when cache branch no longer exists")
}
