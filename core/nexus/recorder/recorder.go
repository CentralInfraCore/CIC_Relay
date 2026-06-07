package recorder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"centralrelay/core/nexus/crypto"
	"centralrelay/core/nexus/types"
)

// StateRecorder defines the interface for persisting state snapshots.
type StateRecorder interface {
	RecordState(ctx context.Context, expected types.ExpectedState, actual interface{}) (commitHash string, err error)
}

// GitStateRecorder implements StateRecorder using a local Git repository.
type GitStateRecorder struct {
	RepoPath      string
	CryptoService crypto.CryptoService
}

// NewGitStateRecorder creates a new recorder that operates on the given repository path.
func NewGitStateRecorder(repoPath string, cryptoSvc crypto.CryptoService) (*GitStateRecorder, error) {
	recorder := &GitStateRecorder{
		RepoPath:      repoPath,
		CryptoService: cryptoSvc,
	}

	if err := recorder.init(); err != nil {
		return nil, fmt.Errorf("failed to initialize git repository at %s: %w", repoPath, err)
	}

	return recorder, nil
}

// init ensures the repository directory and the git repository exist.
func (r *GitStateRecorder) init() error {
	if err := os.MkdirAll(r.RepoPath, 0755); err != nil {
		return err
	}
	gitPath := filepath.Join(r.RepoPath, ".git")
	if _, err := os.Stat(gitPath); err == nil {
		return nil
	}
	if err := r.runGit("init"); err != nil {
		return err
	}

	// Configure git user identity for this repository to allow commits
	if err := r.runGit("config", "user.email", "nexus@cic-relay.local"); err != nil {
		return fmt.Errorf("failed to set git user.email: %w", err)
	}
	if err := r.runGit("config", "user.name", "CIC Nexus"); err != nil {
		return fmt.Errorf("failed to set git user.name: %w", err)
	}

	// Fix: Explicitly un-ignore .gitignore so we can add it without -f
	gitignoreContent := `
*
!.gitignore
!expected.json
!actual.json
!meta.json
!signature.meta
`
	gitignorePath := filepath.Join(r.RepoPath, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(gitignoreContent), 0644); err != nil {
		return err
	}
	if err := r.runGit("add", ".gitignore"); err != nil {
		return err
	}
	return r.runGit("commit", "-m", "Initial commit: .gitignore")
}

// RecordState persists the state to JSON files, signs it, and creates a git commit.
func (r *GitStateRecorder) RecordState(ctx context.Context, expected types.ExpectedState, actual interface{}) (string, error) {
	filesToWrite := make(map[string][]byte)
	var err error

	// 1. Serialize all data to be written
	filesToWrite["expected.json"], err = json.MarshalIndent(expected, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal expected state: %w", err)
	}
	filesToWrite["actual.json"], err = json.MarshalIndent(actual, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal actual state: %w", err)
	}
	meta := map[string]interface{}{
		"run_id":    expected.RunID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	filesToWrite["meta.json"], err = json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal meta: %w", err)
	}

	// 2. Create a manifest of files and their hashes for signing
	manifest, err := r.createManifest(filesToWrite)
	if err != nil {
		return "", err
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal manifest: %w", err)
	}

	// 3. Sign the manifest hash
	if r.CryptoService == nil {
		return "", fmt.Errorf("cryptoservice is not configured")
	}
	manifestHash := sha256.Sum256(manifestBytes)
	signature, err := r.CryptoService.Sign(ctx, manifestHash[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign manifest: %w", err)
	}
	filesToWrite["signature.meta"], err = json.MarshalIndent(map[string]string{"signature": signature}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal signature: %w", err)
	}

	// 4. Write all files to disk
	for name, content := range filesToWrite {
		if err := os.WriteFile(filepath.Join(r.RepoPath, name), content, 0644); err != nil {
			return "", fmt.Errorf("failed to write %s: %w", name, err)
		}
	}

	// 5. Git Add & Commit
	if err := r.runGit("add", "."); err != nil {
		return "", err
	}
	commitMsg := fmt.Sprintf("[AUTO] run-id: %s", expected.RunID)
	if err := r.runGit("commit", "--allow-empty", "-m", commitMsg); err != nil {
		return "", err
	}

	// 6. Get and return commit hash
	return r.getHeadHash()
}

// createManifest generates a sorted list of files and their SHA256 hashes.
func (r *GitStateRecorder) createManifest(files map[string][]byte) (map[string]string, error) {
	manifest := make(map[string]string)
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys) // Sort filenames for deterministic manifest

	for _, filename := range keys {
		hash := sha256.Sum256(files[filename])
		manifest[filename] = hex.EncodeToString(hash[:])
	}
	return manifest, nil
}

// runGit executes a git command in the repo directory.
func (r *GitStateRecorder) runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.RepoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %s (%w)", strings.Join(args, " "), string(output), err)
	}
	return nil
}

// getHeadHash returns the full hash of the current HEAD commit.
func (r *GitStateRecorder) getHeadHash() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = r.RepoPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD hash: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
