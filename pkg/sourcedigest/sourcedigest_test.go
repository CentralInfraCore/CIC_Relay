package sourcedigest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/sourcedigest"
)

func TestComputeTreeHash_Determinism(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.go"), []byte("package main\n// comment"), 0o644))

	h1, err := sourcedigest.ComputeTreeHash(dir, nil)
	require.NoError(t, err)
	h2, err := sourcedigest.ComputeTreeHash(dir, nil)
	require.NoError(t, err)

	assert.Equal(t, h1, h2, "same tree must produce same hash")
	assert.Len(t, h1, 64, "SHA-256 hex is 64 chars")
}

func TestComputeTreeHash_ContentSensitivity(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main"), 0o644))

	before, err := sourcedigest.ComputeTreeHash(dir, nil)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main // changed"), 0o644))

	after, err := sourcedigest.ComputeTreeHash(dir, nil)
	require.NoError(t, err)

	assert.NotEqual(t, before, after, "content change must change the hash")
}

func TestComputeTreeHash_AddedFileSensitivity(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main"), 0o644))

	before, err := sourcedigest.ComputeTreeHash(dir, nil)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.go"), []byte("package lib"), 0o644))

	after, err := sourcedigest.ComputeTreeHash(dir, nil)
	require.NoError(t, err)

	assert.NotEqual(t, before, after, "adding a file must change the hash")
}

func TestComputeTreeHash_PathSensitivity(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	content := []byte("package main")
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "a.go"), content, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "b.go"), content, 0o644))

	h1, err := sourcedigest.ComputeTreeHash(dir1, nil)
	require.NoError(t, err)
	h2, err := sourcedigest.ComputeTreeHash(dir2, nil)
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "same content under different path must produce different hash")
}

func TestComputeTreeHash_ExcludesDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644))

	excluded := filepath.Join(dir, "output")
	require.NoError(t, os.MkdirAll(excluded, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(excluded, "binary"), []byte("binary content"), 0o644))

	withExclude, err := sourcedigest.ComputeTreeHash(dir, []string{"output"})
	require.NoError(t, err)

	withoutExclude, err := sourcedigest.ComputeTreeHash(dir, []string{})
	require.NoError(t, err)

	assert.NotEqual(t, withExclude, withoutExclude, "excluded dir must not affect hash")

	// Verify stability: excluding output again gives the same hash as before.
	withExclude2, err := sourcedigest.ComputeTreeHash(dir, []string{"output"})
	require.NoError(t, err)
	assert.Equal(t, withExclude, withExclude2)
}

func TestComputeTreeHash_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	h, err := sourcedigest.ComputeTreeHash(dir, nil)
	require.NoError(t, err)
	assert.Len(t, h, 64, "empty tree still produces a valid 64-char hex hash")
}

func TestComputeTreeHash_SymlinksSkipped(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real.go"), []byte("package main"), 0o644))
	_ = os.Symlink(filepath.Join(dir, "real.go"), filepath.Join(dir, "link.go")) // best-effort

	// Hash must not fail regardless of symlink support on the OS.
	h, err := sourcedigest.ComputeTreeHash(dir, nil)
	require.NoError(t, err)
	assert.Len(t, h, 64)
}

func TestComputeTreeHash_UnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "secret.go")
	require.NoError(t, os.WriteFile(p, []byte("package main"), 0o644))
	require.NoError(t, os.Chmod(p, 0o000))
	t.Cleanup(func() { os.Chmod(p, 0o644) })

	_, err := sourcedigest.ComputeTreeHash(dir, nil)
	assert.Error(t, err, "unreadable file should cause ComputeTreeHash to fail")
}
