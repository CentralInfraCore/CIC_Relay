package cibuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseInput covers input validation logic.
func TestParseInput(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		raw := map[string]interface{}{
			"build_target":    "/tmp/project",
			"make_target":     "release",
			"artifact_output": "/tmp/project/dist/relay",
		}
		in, err := parseInput(raw)
		require.NoError(t, err)
		assert.Equal(t, "/tmp/project", in.BuildTarget)
		assert.Equal(t, "release", in.MakeTarget)
		assert.Equal(t, "/tmp/project/dist/relay", in.ArtifactOutput)
	})

	t.Run("missing artifact_output", func(t *testing.T) {
		raw := map[string]interface{}{"make_target": "build"}
		_, err := parseInput(raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "artifact_output is required")
	})

	t.Run("wrong type", func(t *testing.T) {
		_, err := parseInput("not a map")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected map")
	})
}

// TestFileDigest verifies SHA-256 digest calculation.
func TestFileDigest(t *testing.T) {
	tmpDir := t.TempDir()
	content := []byte("hello relay")
	path := filepath.Join(tmpDir, "artifact")
	require.NoError(t, os.WriteFile(path, content, 0644))

	digest, err := fileDigest(path)
	require.NoError(t, err)

	sum := sha256.Sum256(content)
	expected := hex.EncodeToString(sum[:])
	assert.Equal(t, expected, digest)
}

// TestFileDigest_NotFound verifies error on missing file.
func TestFileDigest_NotFound(t *testing.T) {
	_, err := fileDigest("/nonexistent/path/artifact")
	require.Error(t, err)
}

// TestExecute_MissingArtifactOutput verifies that missing artifact_output fails fast.
func TestExecute_MissingArtifactOutput(t *testing.T) {
	_, err := Execute(context.Background(), map[string]interface{}{
		"make_target": "build",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact_output is required")
}

// TestExecute_WrongInputType verifies type guard.
func TestExecute_WrongInputType(t *testing.T) {
	_, err := Execute(context.Background(), 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected map")
}

// TestExecute_MakeFails verifies that a failing make returns an error.
func TestExecute_MakeFails(t *testing.T) {
	tmpDir := t.TempDir()
	// Write a Makefile with a failing target.
	makefile := "fail:\n\t@exit 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Makefile"), []byte(makefile), 0644))

	_, err := Execute(context.Background(), map[string]interface{}{
		"build_target":    tmpDir,
		"make_target":     "fail",
		"artifact_output": filepath.Join(tmpDir, "out"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "make fail failed")
}

// TestExecute_Success verifies a full successful build round-trip.
func TestExecute_Success(t *testing.T) {
	tmpDir := t.TempDir()
	artifactPath := filepath.Join(tmpDir, "out.bin")
	// Makefile writes a known artifact.
	makefile := "build:\n\t@echo hello > " + artifactPath + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Makefile"), []byte(makefile), 0644))

	result, err := Execute(context.Background(), map[string]interface{}{
		"build_target":    tmpDir,
		"make_target":     "build",
		"artifact_output": artifactPath,
	})
	require.NoError(t, err)

	out, ok := result.(Output)
	require.True(t, ok)
	assert.Equal(t, artifactPath, out.ArtifactPath)
	assert.Equal(t, 0, out.ExitCode)
	assert.NotEmpty(t, out.ArtifactDigest)

	// Verify digest matches actual file content.
	data, _ := os.ReadFile(artifactPath)
	sum := sha256.Sum256(data)
	assert.Equal(t, hex.EncodeToString(sum[:]), out.ArtifactDigest)
}