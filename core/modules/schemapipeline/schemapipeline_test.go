package schemapipeline_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"centralrelay/core/modules/schemapipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock CommandRunner ---

type mockRunner struct {
	calls  []mockCall
	cursor int
}

type mockCall struct {
	name     string
	args     []string
	output   string
	exitCode int
	err      error
}

func (m *mockRunner) Run(_ context.Context, _ string, name string, args ...string) (string, int, error) {
	if m.cursor >= len(m.calls) {
		return "", -1, fmt.Errorf("unexpected Run call: %s %v", name, args)
	}
	call := m.calls[m.cursor]
	m.cursor++
	return call.output, call.exitCode, call.err
}

func (m *mockRunner) expectNext(name, output string, exitCode int) {
	m.calls = append(m.calls, mockCall{name: name, output: output, exitCode: exitCode})
}

func (m *mockRunner) expectErr(name string, err error) {
	m.calls = append(m.calls, mockCall{name: name, err: err, exitCode: -1})
}

// --- Start tests ---

func TestStart_Success(t *testing.T) {
	buildDir := t.TempDir()
	mr := &mockRunner{}
	mr.expectNext("git clone", "", 0)
	mr.expectNext("git rev-parse", "abc123def456\n", 0)
	mr.expectNext("docker inspect", "sha256:imageabc\n", 0)

	// Note: Start calls os.RemoveAll(buildDir) before clone;
	// mock clone is a no-op so buildDir won't exist afterwards.
	// computeTreeDigest handles the missing dir with a stable empty-tree hash.

	cfg := &schemapipeline.PipelineConfig{
		BuildDir:         buildDir,
		BuilderContainer: "cic-builder",
		Runner:           mr,
	}

	fn := cfg.NewStartFunc()
	result, err := fn(context.Background(), map[string]interface{}{
		"repo_url": "https://git.example.com/schemas.git",
		"branch":   "postgres/dev",
		"schema":   "postgresql",
		"version":  "v0.18.0",
	})
	require.NoError(t, err)

	out, ok := result.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "abc123def456", out["source_ref"])
	assert.Equal(t, "sha256:imageabc", out["builder_image_digest"])
	assert.NotEmpty(t, out["source_tree_digest"]) // stable empty-tree hash
	assert.NotEmpty(t, out["step_hash"])
	assert.Equal(t, "start", out["pipeline_step"])
	assert.Equal(t, buildDir, out["build_dir"])
}

func TestStart_MissingRepoURL(t *testing.T) {
	cfg := &schemapipeline.PipelineConfig{BuildDir: t.TempDir(), Runner: &mockRunner{}}
	fn := cfg.NewStartFunc()
	_, err := fn(context.Background(), map[string]interface{}{
		"branch": "main", "schema": "pg", "version": "v1.0.0",
	})
	require.ErrorContains(t, err, "repo_url is required")
}

func TestStart_MissingBranch(t *testing.T) {
	cfg := &schemapipeline.PipelineConfig{BuildDir: t.TempDir(), Runner: &mockRunner{}}
	fn := cfg.NewStartFunc()
	_, err := fn(context.Background(), map[string]interface{}{
		"repo_url": "https://git.example.com/schemas.git",
		"schema":   "pg", "version": "v1.0.0",
	})
	require.ErrorContains(t, err, "branch is required")
}

func TestStart_CloneFailure(t *testing.T) {
	buildDir := t.TempDir()
	mr := &mockRunner{}
	mr.expectNext("git clone", "fatal: repo not found", 128)

	cfg := &schemapipeline.PipelineConfig{BuildDir: buildDir, Runner: mr}
	fn := cfg.NewStartFunc()
	_, err := fn(context.Background(), map[string]interface{}{
		"repo_url": "https://git.example.com/schemas.git",
		"branch":   "main", "schema": "pg", "version": "v1.0.0",
	})
	require.ErrorContains(t, err, "git clone exit 128")
}

// --- Test step tests ---

func TestTest_Success(t *testing.T) {
	mr := &mockRunner{}
	mr.expectNext("docker exec make test", "3 passed\n", 0)

	cfg := &schemapipeline.PipelineConfig{BuilderContainer: "cic-builder", Runner: mr}
	fn := cfg.NewTestFunc()
	result, err := fn(context.Background(), map[string]interface{}{
		"schema": "postgresql", "version": "v0.18.0",
		"build_dir": "/build", "step_hash": "prevhash",
	})
	require.NoError(t, err)

	out := result.(map[string]interface{})
	assert.Equal(t, "test", out["pipeline_step"])
	assert.Equal(t, 0, out["test_exit_code"])
	assert.NotEmpty(t, out["test_stdout_digest"])
	assert.NotEmpty(t, out["step_hash"])
	// step_hash changed from input
	assert.NotEqual(t, "prevhash", out["step_hash"])
}

func TestTest_Failure(t *testing.T) {
	mr := &mockRunner{}
	mr.expectNext("docker exec make test", "1 failed\n", 1)

	cfg := &schemapipeline.PipelineConfig{BuilderContainer: "cic-builder", Runner: mr}
	fn := cfg.NewTestFunc()
	_, err := fn(context.Background(), map[string]interface{}{
		"schema": "postgresql", "version": "v0.18.0",
	})
	require.ErrorContains(t, err, "make test exit 1")
}

func TestTest_MissingContainer(t *testing.T) {
	cfg := &schemapipeline.PipelineConfig{Runner: &mockRunner{}}
	fn := cfg.NewTestFunc()
	_, err := fn(context.Background(), map[string]interface{}{"schema": "pg"})
	require.ErrorContains(t, err, "builder_container is required")
}

// --- Validate step tests ---

func TestValidate_Success(t *testing.T) {
	mr := &mockRunner{}
	mr.expectNext("docker exec make validate", "All schemas valid\n", 0)

	cfg := &schemapipeline.PipelineConfig{BuilderContainer: "cic-builder", Runner: mr}
	fn := cfg.NewValidateFunc()
	result, err := fn(context.Background(), map[string]interface{}{
		"schema": "postgresql", "version": "v0.18.0",
	})
	require.NoError(t, err)
	out := result.(map[string]interface{})
	assert.Equal(t, "validate", out["pipeline_step"])
	assert.Equal(t, 0, out["validate_exit_code"])
}

func TestValidate_Failure(t *testing.T) {
	mr := &mockRunner{}
	mr.expectNext("docker exec make validate", "schema error\n", 2)

	cfg := &schemapipeline.PipelineConfig{BuilderContainer: "cic-builder", Runner: mr}
	fn := cfg.NewValidateFunc()
	_, err := fn(context.Background(), map[string]interface{}{"schema": "pg"})
	require.ErrorContains(t, err, "make validate exit 2")
}

// --- Release step tests ---

func TestRelease_Success(t *testing.T) {
	buildDir := t.TempDir()
	sourceDir := filepath.Join(buildDir, "source")
	require.NoError(t, os.MkdirAll(sourceDir, 0755))
	artifactContent := "metadata:\n  name: postgresql\nspec: {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "postgresql.yaml"), []byte(artifactContent), 0644))

	mr := &mockRunner{}
	mr.expectNext("docker exec make release", "Release OK\n", 0)

	cfg := &schemapipeline.PipelineConfig{
		BuildDir:         buildDir,
		BuilderContainer: "cic-builder",
		Runner:           mr,
	}
	fn := cfg.NewReleaseFunc()
	result, err := fn(context.Background(), map[string]interface{}{
		"schema": "postgresql", "version": "v0.18.0",
		"build_dir": buildDir,
	})
	require.NoError(t, err)

	out := result.(map[string]interface{})
	assert.Equal(t, "release", out["pipeline_step"])
	assert.Equal(t, 0, out["release_exit_code"])
	assert.Equal(t, artifactContent, out["source_assertion"])
	assert.NotEmpty(t, out["artifact_digest"])
	assert.Equal(t, int64(len(artifactContent)), out["artifact_size"])
}

func TestRelease_MakeFailure(t *testing.T) {
	mr := &mockRunner{}
	mr.expectNext("docker exec make release", "vault error\n", 1)

	cfg := &schemapipeline.PipelineConfig{BuilderContainer: "cic-builder", Runner: mr}
	fn := cfg.NewReleaseFunc()
	_, err := fn(context.Background(), map[string]interface{}{
		"schema": "postgresql", "version": "v0.18.0", "build_dir": "/build",
	})
	require.ErrorContains(t, err, "make release exit 1")
}

func TestRelease_ArtifactMissing(t *testing.T) {
	buildDir := t.TempDir() // no source/ subdir

	mr := &mockRunner{}
	mr.expectNext("docker exec make release", "ok\n", 0)

	cfg := &schemapipeline.PipelineConfig{
		BuildDir: buildDir, BuilderContainer: "cic-builder", Runner: mr,
	}
	fn := cfg.NewReleaseFunc()
	_, err := fn(context.Background(), map[string]interface{}{
		"schema": "postgresql", "version": "v0.18.0", "build_dir": buildDir,
	})
	require.ErrorContains(t, err, "read artifact")
}

func TestRelease_VersionStripsV(t *testing.T) {
	buildDir := t.TempDir()
	sourceDir := filepath.Join(buildDir, "source")
	require.NoError(t, os.MkdirAll(sourceDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "pg.yaml"), []byte("artifact"), 0644))

	var capturedArgs []string
	mr := &captureRunner{}
	mr.onRun = func(name string, args []string) (string, int, error) {
		capturedArgs = append([]string{name}, args...)
		return "ok\n", 0, nil
	}

	cfg := &schemapipeline.PipelineConfig{
		BuildDir: buildDir, BuilderContainer: "c", Runner: mr,
	}
	fn := cfg.NewReleaseFunc()
	_, err := fn(context.Background(), map[string]interface{}{
		"schema": "pg", "version": "v1.2.3", "build_dir": buildDir,
	})
	require.NoError(t, err)
	// VERSION= should not include the leading 'v'.
	assert.Contains(t, capturedArgs, "VERSION=1.2.3")
}

// captureRunner captures the last call's args for inspection.
type captureRunner struct {
	onRun func(name string, args []string) (string, int, error)
}

func (r *captureRunner) Run(_ context.Context, _ string, name string, args ...string) (string, int, error) {
	return r.onRun(name, args)
}

// --- step_hash chaining ---

// TestValidateStepHash_Deterministic verifies that sortedKeys produces a stable
// sorted order on every call: the same input must yield an identical step_hash
// across repeated invocations. Go randomises map iteration per range, so any
// mutation that breaks the sort (disabled inner loop, reversed comparison,
// wrong swap indices) will produce a different hash on at least one iteration.
func TestValidateStepHash_Deterministic(t *testing.T) {
	const iterations = 10
	baseInput := map[string]interface{}{
		"schema": "pg", "version": "v1",
		"source_ref": "abc123", "builder_image_digest": "sha256:xyz",
	}
	var firstHash string
	for i := 0; i < iterations; i++ {
		mr := &mockRunner{}
		mr.expectNext("docker exec make validate", "All schemas valid\n", 0)
		cfg := &schemapipeline.PipelineConfig{BuilderContainer: "cic-builder", Runner: mr}
		result, err := cfg.NewValidateFunc()(context.Background(), baseInput)
		require.NoError(t, err)
		out := result.(map[string]interface{})
		hash, _ := out["step_hash"].(string)
		require.NotEmpty(t, hash, "iteration %d: step_hash must not be empty", i)
		if i == 0 {
			firstHash = hash
		} else {
			assert.Equal(t, firstHash, hash, "step_hash must be identical across all iterations (iteration %d)", i+1)
		}
	}
}

// TestValidateStepHash_GoldenValue asserts the exact step_hash for a known input.
// This kills mutations that sort keys in the wrong order (e.g. reversed comparison):
// any wrong sort produces a different Merkle root.
func TestValidateStepHash_GoldenValue(t *testing.T) {
	mr := &mockRunner{}
	mr.expectNext("docker exec make validate", "All schemas valid\n", 0)
	cfg := &schemapipeline.PipelineConfig{BuilderContainer: "cic-builder", Runner: mr}
	result, err := cfg.NewValidateFunc()(context.Background(), map[string]interface{}{
		"schema": "pg", "version": "v1",
		"source_ref": "abc123", "builder_image_digest": "sha256:xyz",
	})
	require.NoError(t, err)
	out := result.(map[string]interface{})
	const wantHash = "83c744aaf378012a947a3d69abd836591913a87028a5b4970f4f560aa7e69e3c"
	assert.Equal(t, wantHash, out["step_hash"],
		"step_hash golden mismatch — sortedKeys key order may have changed")
}

// --- OSCommandRunner ---

func TestOSCommandRunner_Run_Success(t *testing.T) {
	r := &schemapipeline.OSCommandRunner{}
	out, code, err := r.Run(context.Background(), "", "echo", "hello")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "hello")
}

func TestOSCommandRunner_Run_NonZeroExit(t *testing.T) {
	r := &schemapipeline.OSCommandRunner{}
	_, code, err := r.Run(context.Background(), "", "sh", "-c", "exit 2")
	require.NoError(t, err)
	assert.Equal(t, 2, code)
}

func TestOSCommandRunner_Run_WithDir(t *testing.T) {
	dir := t.TempDir()
	r := &schemapipeline.OSCommandRunner{}
	out, code, err := r.Run(context.Background(), dir, "sh", "-c", "echo $PWD")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.NotEmpty(t, out)
}

// --- computeTreeDigest via NewStartFunc ---

// fileCreatingRunner simulates a git clone by writing a file to buildDir.
type fileCreatingRunner struct{ buildDir string }

func (r *fileCreatingRunner) Run(_ context.Context, _ string, name string, args ...string) (string, int, error) {
	if name == "git" && len(args) > 0 && args[0] == "clone" {
		_ = os.MkdirAll(r.buildDir, 0o755)
		_ = os.WriteFile(filepath.Join(r.buildDir, "schema.yaml"), []byte("kind: Schema\n"), 0o644)
		return "", 0, nil
	}
	if name == "git" {
		return "abc123def456\n", 0, nil
	}
	if name == "docker" {
		return "sha256:imageabc\n", 0, nil
	}
	return "", 0, nil
}

func TestStart_ComputesTreeDigest_NonEmpty(t *testing.T) {
	buildDir := t.TempDir()
	cfg := &schemapipeline.PipelineConfig{
		BuildDir:         buildDir,
		BuilderContainer: "cic-builder",
		Runner:           &fileCreatingRunner{buildDir: buildDir},
	}
	result, err := cfg.NewStartFunc()(context.Background(), map[string]interface{}{
		"repo_url": "https://git.example.com/schemas.git",
		"branch":   "main",
		"schema":   "postgresql",
		"version":  "v1.0",
	})
	require.NoError(t, err)
	out := result.(map[string]interface{})
	treeDigest, _ := out["source_tree_digest"].(string)
	assert.True(t, strings.HasPrefix(treeDigest, "sha256:"), "tree digest should have sha256: prefix")
	emptyTreeHash := "sha256:" + func() string {
		sum := sha256.Sum256([]byte("empty-tree"))
		return hex.EncodeToString(sum[:])
	}()
	assert.NotEqual(t, emptyTreeHash, treeDigest, "non-empty clone should differ from empty-tree hash")
}

func TestStart_ComputesTreeDigest_GitDirSkipped(t *testing.T) {
	buildDir := t.TempDir()
	runner := &fileCreatingRunner{buildDir: buildDir}
	// Pre-create a .git dir — it should be excluded from the digest
	_ = os.MkdirAll(filepath.Join(buildDir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(buildDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)

	cfg := &schemapipeline.PipelineConfig{
		BuildDir:         buildDir,
		BuilderContainer: "cic-builder",
		Runner:           runner,
	}
	result, err := cfg.NewStartFunc()(context.Background(), map[string]interface{}{
		"repo_url": "https://git.example.com/schemas.git",
		"branch":   "main",
		"schema":   "postgresql",
		"version":  "v1.0",
	})
	require.NoError(t, err)
	out := result.(map[string]interface{})
	assert.NotEmpty(t, out["source_tree_digest"])
}

func TestStepHashChangesAcrossSteps(t *testing.T) {
	mr := &mockRunner{}
	mr.expectNext("test", "out1", 0)
	mr.expectNext("validate", "out2", 0)

	cfg := &schemapipeline.PipelineConfig{BuilderContainer: "c", Runner: mr}

	input := map[string]interface{}{
		"schema": "pg", "version": "v1", "step_hash": "initial",
	}

	r1, _ := cfg.NewTestFunc()(context.Background(), input)
	m1 := r1.(map[string]interface{})

	r2, _ := cfg.NewValidateFunc()(context.Background(), m1)
	m2 := r2.(map[string]interface{})

	assert.NotEqual(t, "initial", m1["step_hash"])
	assert.NotEqual(t, m1["step_hash"], m2["step_hash"])
}