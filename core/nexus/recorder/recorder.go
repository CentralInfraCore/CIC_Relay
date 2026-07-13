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
	// The parent commit hash binds this record to its predecessor: it is part of
	// the signed manifest, so the Vault signature covers the chain position — a
	// tamper-evident signed chain (changing any earlier commit invalidates the
	// git parent linkage that every later signature attests to). Empty only if
	// there is no prior commit (never, since init() makes the .gitignore root).
	parent, err := r.getHeadHash()
	if err != nil {
		parent = ""
	}
	meta := map[string]interface{}{
		"run_id":    expected.RunID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"parent":    parent,
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

// VerifyChain walks the recorded state commits oldest-to-newest and verifies, for
// each, that (1) the Vault signature over the file manifest is valid — the
// content is authentic and unaltered — and (2) the parent commit hash the record
// signed matches its actual git parent — the chain is intact. Any tampering with
// a committed file or any break in the chain is detected. The initial .gitignore
// commit (which carries no signature) is skipped. Returns nil iff the whole chain
// of state commits verifies; the signing key never leaves Vault.
func (r *GitStateRecorder) VerifyChain(ctx context.Context) error {
	if r.CryptoService == nil {
		return fmt.Errorf("cryptoservice is not configured")
	}
	commits, err := r.listCommits()
	if err != nil {
		return err
	}
	verified := 0
	for _, c := range commits {
		sigRaw, err := r.showFileAtCommit(c, "signature.meta")
		if err != nil {
			continue // not a state commit (e.g. the initial .gitignore commit)
		}
		expected, err := r.showFileAtCommit(c, "expected.json")
		if err != nil {
			return fmt.Errorf("commit %s: missing expected.json: %w", c, err)
		}
		actual, err := r.showFileAtCommit(c, "actual.json")
		if err != nil {
			return fmt.Errorf("commit %s: missing actual.json: %w", c, err)
		}
		metaRaw, err := r.showFileAtCommit(c, "meta.json")
		if err != nil {
			return fmt.Errorf("commit %s: missing meta.json: %w", c, err)
		}

		// Recompute the signed manifest exactly as RecordState did.
		manifest, err := r.createManifest(map[string][]byte{
			"expected.json": expected,
			"actual.json":   actual,
			"meta.json":     metaRaw,
		})
		if err != nil {
			return fmt.Errorf("commit %s: %w", c, err)
		}
		manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return fmt.Errorf("commit %s: marshal manifest: %w", c, err)
		}
		manifestHash := sha256.Sum256(manifestBytes)

		var sigDoc struct {
			Signature string `json:"signature"`
		}
		if err := json.Unmarshal(sigRaw, &sigDoc); err != nil {
			return fmt.Errorf("commit %s: invalid signature.meta: %w", c, err)
		}
		ok, err := r.CryptoService.Verify(ctx, manifestHash[:], sigDoc.Signature)
		if err != nil {
			return fmt.Errorf("commit %s: verify: %w", c, err)
		}
		if !ok {
			return fmt.Errorf("commit %s: signature verification failed (content altered or wrong key)", c)
		}

		// Chain linkage: the parent the record signed must be the real git parent.
		var m struct {
			Parent string `json:"parent"`
		}
		if err := json.Unmarshal(metaRaw, &m); err != nil {
			return fmt.Errorf("commit %s: invalid meta.json: %w", c, err)
		}
		gitParent, err := r.parentOf(c)
		if err != nil {
			return fmt.Errorf("commit %s: %w", c, err)
		}
		if m.Parent != gitParent {
			return fmt.Errorf("commit %s: chain broken (signed parent %q != git parent %q)", c, m.Parent, gitParent)
		}
		verified++
	}
	if verified == 0 {
		return fmt.Errorf("no signed state commits to verify")
	}
	return nil
}

// listCommits returns all commit hashes oldest-to-newest.
func (r *GitStateRecorder) listCommits() ([]string, error) {
	cmd := exec.Command("git", "log", "--reverse", "--format=%H")
	cmd.Dir = r.RepoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list commits: %w", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// showFileAtCommit returns the content of file as it existed at commit.
func (r *GitStateRecorder) showFileAtCommit(commit, file string) ([]byte, error) {
	cmd := exec.Command("git", "show", commit+":"+file)
	cmd.Dir = r.RepoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to read %s at %s: %w", file, commit, err)
	}
	return out, nil
}

// parentOf returns the parent commit hash of commit, or "" if it is the root.
func (r *GitStateRecorder) parentOf(commit string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", commit+"^")
	cmd.Dir = r.RepoPath
	out, err := cmd.Output()
	if err != nil {
		return "", nil // no parent → root commit
	}
	return strings.TrimSpace(string(out)), nil
}
