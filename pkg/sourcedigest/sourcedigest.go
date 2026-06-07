// Package sourcedigest provides a deterministic source tree hash suitable for
// embedding into binaries as a ProofTrace build-input anchor.
//
// Algorithm:
//  1. WalkDir the root path, collecting (relPath, SHA-256(content)) pairs.
//  2. Sort entries by relPath (lexicographic, forward-slash normalised).
//  3. Aggregate: SHA-256 over "relPath\x00hexContent\n" lines in sorted order.
//
// The result is a stable, 64-char lowercase hex string that is sensitive to
// any change in file content, path, or set of tracked files.  It is NOT
// sensitive to file mode or mtime — only to content and relative paths.
package sourcedigest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultExcludes is the set of top-level directory names skipped by default
// during tree traversal.  These are build artefact or cache directories whose
// contents are not part of the source inputs.
var DefaultExcludes = []string{".git", "output", "dist", "tmp", "CurrentTest"}

// ComputeTreeHash returns a deterministic SHA-256 digest of all regular files
// under rootPath.  Top-level directories whose name matches an entry in
// excludes (or DefaultExcludes if nil) are skipped entirely.
func ComputeTreeHash(rootPath string, excludes []string) (string, error) {
	if excludes == nil {
		excludes = DefaultExcludes
	}
	excludeSet := make(map[string]bool, len(excludes))
	for _, e := range excludes {
		excludeSet[e] = true
	}

	type entry struct {
		relPath string
		hexHash string
	}
	var entries []entry

	err := filepath.WalkDir(rootPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(rootPath, path)
		if err != nil {
			return err
		}
		// Normalise to forward slashes so the digest is platform-independent.
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			top := strings.SplitN(rel, "/", 2)[0]
			if excludeSet[top] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		h, err := fileContentHash(path)
		if err != nil {
			return fmt.Errorf("hash %s: %w", rel, err)
		}
		entries = append(entries, entry{relPath: rel, hexHash: h})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk %q: %w", rootPath, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relPath < entries[j].relPath
	})

	agg := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(agg, "%s\x00%s\n", e.relPath, e.hexHash)
	}
	return hex.EncodeToString(agg.Sum(nil)), nil
}

// fileContentHash returns the hex-encoded SHA-256 of the file at path.
func fileContentHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
