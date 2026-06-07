package git

import (
	"fmt"
	"sync"
)

// RepoRegistry maps symbolic repoIDs to local filesystem paths.
// WASM modules address repositories by repoID only — they never receive
// arbitrary path strings, enforcing the capability boundary.
type RepoRegistry struct {
	mu    sync.RWMutex
	repos map[string]string
}

// NewRepoRegistry creates an empty RepoRegistry.
func NewRepoRegistry() *RepoRegistry {
	return &RepoRegistry{repos: make(map[string]string)}
}

// Register associates repoID with localPath. Subsequent calls with the same
// repoID overwrite the previous mapping.
func (r *RepoRegistry) Register(repoID, localPath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.repos[repoID] = localPath
}

// Resolve returns the local path for repoID, or an error if the repoID is
// not registered. Unknown repoIDs are rejected so WASM modules cannot access
// arbitrary filesystem paths.
func (r *RepoRegistry) Resolve(repoID string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	path, ok := r.repos[repoID]
	if !ok {
		return "", fmt.Errorf("unknown repoID: %q", repoID)
	}
	return path, nil
}
