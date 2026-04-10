package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRepoRegistry_KnownRepo verifies that a registered repoID resolves correctly.
func TestRepoRegistry_KnownRepo(t *testing.T) {
	reg := NewRepoRegistry()
	reg.Register("my-repo", "/data/repos/my-repo")

	path, err := reg.Resolve("my-repo")
	require.NoError(t, err)
	assert.Equal(t, "/data/repos/my-repo", path)
}

// TestRepoRegistry_WhitelistEnforce verifies that an unknown repoID is rejected.
func TestRepoRegistry_WhitelistEnforce(t *testing.T) {
	reg := NewRepoRegistry()

	_, err := reg.Resolve("unknown-repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown repoID: "unknown-repo"`)
}

// TestRepoRegistry_Overwrite verifies that re-registering a repoID replaces the path.
func TestRepoRegistry_Overwrite(t *testing.T) {
	reg := NewRepoRegistry()
	reg.Register("repo", "/old/path")
	reg.Register("repo", "/new/path")

	path, err := reg.Resolve("repo")
	require.NoError(t, err)
	assert.Equal(t, "/new/path", path)
}
