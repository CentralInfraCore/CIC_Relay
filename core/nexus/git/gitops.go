package git

import "context"

// StatusEntry represents a single file entry from git status --porcelain.
type StatusEntry struct {
	Path  string // file path (relative to repo root)
	State string // two-char XY porcelain state code (e.g., "M ", " M", "??")
}

// GitOps is the typed API that WASM modules can invoke for git operations.
// All operations are scoped to registered repositories — arbitrary paths
// are not accepted.
type GitOps interface {
	// Clone clones url (branch) into the local path registered for repoID.
	Clone(ctx context.Context, repoID, url, branch string) error

	// Fetch fetches the given remote for the registered repo.
	Fetch(ctx context.Context, repoID, remote string) error

	// Checkout checks out the given ref in the registered repo.
	Checkout(ctx context.Context, repoID, ref string) error

	// Commit stages paths and creates a commit; returns the new commit hash.
	Commit(ctx context.Context, repoID, message string, paths []string) (string, error)

	// Push pushes refspec to remote for the registered repo.
	Push(ctx context.Context, repoID, remote, refspec string) error

	// Status returns the working-tree status of the registered repo.
	Status(ctx context.Context, repoID string) ([]StatusEntry, error)
}
