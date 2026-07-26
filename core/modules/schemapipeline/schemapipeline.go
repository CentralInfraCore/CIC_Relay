// Package schemapipeline implements the four-step CIC-Schemas build pipeline
// as native relay modules.
//
// Workflow steps (in order):
//
//	cic.pipeline.start@1.0    — git clone + branch, record source_ref + tree_digest
//	cic.pipeline.test@1.0     — make test in the builder container
//	cic.pipeline.validate@1.0 — make validate in the builder container
//	cic.pipeline.release@1.0  — make release, read signed artifact from source/
//
// The output of release feeds directly into cic.source.assert@1.0 as source_assertion.
// Every step adds its own merkle leaf (step_hash) to the carry-over map.
package schemapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"centralrelay/pkg/merkle"
)

// CommandRunner executes an external command and returns combined output,
// the process exit code, and any execution-level error (not a non-zero exit).
// A non-zero exit is reported via exitCode, not err.
type CommandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) (output string, exitCode int, err error)
}

// OSCommandRunner is the production CommandRunner using os/exec.
type OSCommandRunner struct{}

// Run executes name with args in dir (empty = caller's cwd).
func (r *OSCommandRunner) Run(ctx context.Context, dir, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Harden: no inherited stdin, merge stderr into stdout for single output digest.
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), exitErr.ExitCode(), nil
		}
		return string(out), -1, err
	}
	return string(out), 0, nil
}

// PipelineConfig holds the wiring needed by all pipeline steps.
// Construct once; each step function closes over it.
type PipelineConfig struct {
	// BuildDir is the host path (relay-visible) of the shared build volume.
	// After clone, this directory contains the CIC-Schemas working tree.
	BuildDir string

	// BuilderContainer is the name of the Docker container running the
	// CIC-Schemas build environment. Pipeline steps exec make inside it.
	BuilderContainer string

	// Runner is the command executor. Defaults to OSCommandRunner if nil.
	Runner CommandRunner
}

func (c *PipelineConfig) runner() CommandRunner {
	if c.Runner != nil {
		return c.Runner
	}
	return &OSCommandRunner{}
}

// --- Step 1: Start ---

// NewStartFunc returns the cic.pipeline.start@1.0 native function.
// It git-clones the requested repo+branch into BuildDir and records
// source_ref, tree_digest, and builder_image_digest as hash-chain leaves.
//
// Required input fields: repo_url, branch, schema, version.
// Optional: build_dir (overrides PipelineConfig.BuildDir per-request).
func (c *PipelineConfig) NewStartFunc() func(context.Context, interface{}) (interface{}, error) {
	return func(ctx context.Context, input interface{}) (interface{}, error) {
		m, ok := input.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("cic.pipeline.start: expected map, got %T", input)
		}
		repoURL, _ := m["repo_url"].(string)
		branch, _ := m["branch"].(string)
		commit, _ := m["commit"].(string)
		// schema/version are not read here: they belong to the CIC-Schemas pipeline
		// and simply travel through the flow via copyMap.

		if repoURL == "" {
			return nil, fmt.Errorf("cic.pipeline.start: repo_url is required")
		}
		if branch == "" && commit == "" {
			return nil, fmt.Errorf("cic.pipeline.start: branch or commit is required")
		}
		// schema/version are required by the CIC-Schemas pipeline, which reads the
		// released artifact from source/<schema>.yaml. A generic repo CI run has no
		// such concept, so they are carried when present and not demanded here; the
		// steps that actually need them validate on their own.

		buildDir := c.BuildDir
		if override, _ := m["build_dir"].(string); override != "" {
			buildDir = override
		}
		if buildDir == "" {
			return nil, fmt.Errorf("cic.pipeline.start: build_dir is required (set in config or request)")
		}

		r := c.runner()

		// Clean build dir before clone.
		if err := os.RemoveAll(buildDir); err != nil {
			return nil, fmt.Errorf("cic.pipeline.start: clean build dir: %w", err)
		}

		// Clone, then pin to the requested revision.
		//
		// A shallow, branch-pinned clone can only ever reach that branch's tip, so
		// it cannot serve a request that names a specific commit. When `commit` is
		// given we take a full clone and check it out; without it the original
		// shallow branch clone is kept, since it is markedly cheaper.
		if commit != "" {
			out, code, err := r.Run(ctx, "", "git", "clone", repoURL, buildDir)
			if err != nil {
				return nil, fmt.Errorf("cic.pipeline.start: git clone: %w", err)
			}
			if code != 0 {
				return nil, fmt.Errorf("cic.pipeline.start: git clone exit %d: %s", code, strings.TrimSpace(out))
			}
			out, code, err = r.Run(ctx, buildDir, "git", "checkout", "--detach", commit)
			if err != nil {
				return nil, fmt.Errorf("cic.pipeline.start: git checkout %s: %w", commit, err)
			}
			if code != 0 {
				return nil, fmt.Errorf("cic.pipeline.start: git checkout %s exit %d: %s", commit, code, strings.TrimSpace(out))
			}
		} else {
			// git clone --branch <branch> --depth 1 <url> <dir>
			out, code, err := r.Run(ctx, "", "git", "clone", "--branch", branch, "--depth", "1", repoURL, buildDir)
			if err != nil {
				return nil, fmt.Errorf("cic.pipeline.start: git clone: %w", err)
			}
			if code != 0 {
				return nil, fmt.Errorf("cic.pipeline.start: git clone exit %d: %s", code, strings.TrimSpace(out))
			}
		}

		// Resolve HEAD commit.
		sourceRef, code, err := r.Run(ctx, buildDir, "git", "rev-parse", "HEAD")
		if err != nil {
			return nil, fmt.Errorf("cic.pipeline.start: git rev-parse HEAD: %w", err)
		}
		if code != 0 {
			return nil, fmt.Errorf("cic.pipeline.start: git rev-parse exit %d", code)
		}
		sourceRef = strings.TrimSpace(sourceRef)

		// Compute tree digest: sha256 of all tracked file paths+contents.
		treeDigest, err := computeTreeDigest(buildDir)
		if err != nil {
			return nil, fmt.Errorf("cic.pipeline.start: tree digest: %w", err)
		}

		// Builder image digest via docker inspect.
		builderContainer := c.BuilderContainer
		if override, _ := m["builder_container"].(string); override != "" {
			builderContainer = override
		}
		builderImageDigest := ""
		if builderContainer != "" {
			imgDigest, code2, err2 := r.Run(ctx, "", "docker", "inspect",
				"--format", "{{.Image}}", builderContainer)
			if err2 == nil && code2 == 0 {
				builderImageDigest = strings.TrimSpace(imgDigest)
			}
		}

		stepHash := computeStepHash("start", map[string]string{
			"source_ref":           sourceRef,
			"source_tree_digest":   treeDigest,
			"builder_image_digest": builderImageDigest,
			"branch":               branch,
		})

		out2 := copyMap(m)
		out2["build_dir"] = buildDir
		out2["builder_container"] = builderContainer
		out2["source_ref"] = sourceRef
		out2["source_tree_digest"] = treeDigest
		out2["builder_image_digest"] = builderImageDigest
		out2["pipeline_step"] = "start"
		out2["step_hash"] = stepHash
		return out2, nil
	}
}

// --- Step 2: Test ---

// NewTestFunc returns the cic.pipeline.test@1.0 native function.
// Runs `docker exec <builder_container> make test` and records stdout_digest + exit_code.
func (c *PipelineConfig) NewTestFunc() func(context.Context, interface{}) (interface{}, error) {
	return func(ctx context.Context, input interface{}) (interface{}, error) {
		return c.runMakeStep(ctx, input, "test", "cic.pipeline.test")
	}
}

// --- Step 3: Validate ---

// NewValidateFunc returns the cic.pipeline.validate@1.0 native function.
// Runs `docker exec <builder_container> make validate` and records stdout_digest + exit_code.
func (c *PipelineConfig) NewValidateFunc() func(context.Context, interface{}) (interface{}, error) {
	return func(ctx context.Context, input interface{}) (interface{}, error) {
		return c.runMakeStep(ctx, input, "validate", "cic.pipeline.validate")
	}
}

// --- Generic repo CI ---

// NewRepoBuildFunc returns the cic.repo.build@1.0 native function.
//
// The schema pipeline is CIC-Schemas-shaped: it demands schema/version and reads
// the released artifact from a fixed source/<schema>.yaml. This step is the
// generic counterpart — the caller names the make target and the file that make
// produces, and the result is handed to cic.artifact.sign as raw_artifact.
//
// Like test/validate it runs inside the builder container, not in the relay
// process, so the build toolchain stays in a container the operator controls.
//
// Input:  make_target (default "build"), artifact_output (required, relative to
//         build_dir), builder_container, build_dir
// Output: the make step's record plus raw_artifact / artifact_path / artifact_digest
func (c *PipelineConfig) NewRepoBuildFunc() func(context.Context, interface{}) (interface{}, error) {
	return func(ctx context.Context, input interface{}) (interface{}, error) {
		m, ok := input.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("cic.repo.build: expected map, got %T", input)
		}

		makeTarget, _ := m["make_target"].(string)
		if makeTarget == "" {
			makeTarget = "build"
		}
		artifactOutput, _ := m["artifact_output"].(string)
		if artifactOutput == "" {
			return nil, fmt.Errorf("cic.repo.build: artifact_output is required")
		}
		buildDir, _ := m["build_dir"].(string)
		if buildDir == "" {
			buildDir = c.BuildDir
		}
		if buildDir == "" {
			return nil, fmt.Errorf("cic.repo.build: build_dir is required")
		}

		// artifact_output comes from the request, so it must not be able to name a
		// file outside the build directory.
		artifactPath, err := resolveInBuildDir(buildDir, artifactOutput)
		if err != nil {
			return nil, fmt.Errorf("cic.repo.build: %w", err)
		}

		res, err := c.runMakeStep(ctx, input, makeTarget, "cic.repo.build")
		if err != nil {
			return nil, err
		}
		out2, _ := res.(map[string]interface{})

		artifactBytes, err := os.ReadFile(artifactPath)
		if err != nil {
			return nil, fmt.Errorf("cic.repo.build: read artifact %q: %w", artifactOutput, err)
		}

		out2["raw_artifact"] = string(artifactBytes)
		out2["artifact_path"] = artifactOutput
		out2["artifact_digest"] = "sha256:" + digestString(string(artifactBytes))
		out2["artifact_size"] = int64(len(artifactBytes))
		return out2, nil
	}
}

// resolveInBuildDir joins rel onto buildDir and refuses anything that escapes it.
func resolveInBuildDir(buildDir, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("artifact_output must be relative to build_dir, got %q", rel)
	}
	base, err := filepath.Abs(buildDir)
	if err != nil {
		return "", fmt.Errorf("resolve build_dir: %w", err)
	}
	full := filepath.Join(base, rel)
	if full != base && !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("artifact_output %q escapes build_dir", rel)
	}
	return full, nil
}

// runMakeStep is the shared implementation for test and validate steps.
// It runs `make <target>` inside the builder container and records the result.
func (c *PipelineConfig) runMakeStep(ctx context.Context, input interface{}, makeTarget, moduleName string) (interface{}, error) {
	m, ok := input.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s: expected map, got %T", moduleName, input)
	}

	builderContainer, _ := m["builder_container"].(string)
	if builderContainer == "" {
		builderContainer = c.BuilderContainer
	}
	if builderContainer == "" {
		return nil, fmt.Errorf("%s: builder_container is required", moduleName)
	}

	r := c.runner()
	out, exitCode, err := r.Run(ctx, "", "docker", "exec", builderContainer, "make", makeTarget)
	if err != nil {
		return nil, fmt.Errorf("%s: docker exec: %w", moduleName, err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("%s: make %s exit %d: %s", moduleName, makeTarget, exitCode, strings.TrimSpace(out))
	}

	stdoutDigest := digestString(out)
	// command_digest binds the step hash to the exact command that was run,
	// strengthening reproduction proof beyond stdout alone.
	cmdDigest := digestString("docker exec " + builderContainer + " make " + makeTarget)

	prevStepHash, _ := m["step_hash"].(string)
	stepHash := computeStepHash(makeTarget, map[string]string{
		"command_digest": cmdDigest,
		"exit_code":      "0",
		"make_target":    makeTarget,
		"prev_step_hash": prevStepHash,
		"stdout_digest":  stdoutDigest,
	})

	out2 := copyMap(m)
	out2["pipeline_step"] = makeTarget
	out2["step_hash"] = stepHash
	out2[makeTarget+"_exit_code"] = 0
	out2[makeTarget+"_stdout_digest"] = stdoutDigest
	return out2, nil
}

// --- Step 4: Release ---

// NewReleaseFunc returns the cic.pipeline.release@1.0 native function.
// Runs `docker exec <builder_container> make release VERSION=<version>`,
// then reads the signed artifact from <build_dir>/source/<schema>.yaml
// and returns it as source_assertion for cic.source.assert.
func (c *PipelineConfig) NewReleaseFunc() func(context.Context, interface{}) (interface{}, error) {
	return func(ctx context.Context, input interface{}) (interface{}, error) {
		m, ok := input.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("cic.pipeline.release: expected map, got %T", input)
		}

		builderContainer, _ := m["builder_container"].(string)
		if builderContainer == "" {
			builderContainer = c.BuilderContainer
		}
		if builderContainer == "" {
			return nil, fmt.Errorf("cic.pipeline.release: builder_container is required")
		}

		schema, _ := m["schema"].(string)
		version, _ := m["version"].(string)
		buildDir, _ := m["build_dir"].(string)
		if buildDir == "" {
			buildDir = c.BuildDir
		}

		if schema == "" {
			return nil, fmt.Errorf("cic.pipeline.release: schema is required")
		}
		if version == "" {
			return nil, fmt.Errorf("cic.pipeline.release: version is required")
		}
		if buildDir == "" {
			return nil, fmt.Errorf("cic.pipeline.release: build_dir is required")
		}

		// Strip leading 'v' for the VERSION parameter (make release expects semver without 'v').
		releaseVersion := strings.TrimPrefix(version, "v")

		r := c.runner()
		out, exitCode, err := r.Run(ctx, "", "docker", "exec", builderContainer,
			"make", "release", "VERSION="+releaseVersion)
		if err != nil {
			return nil, fmt.Errorf("cic.pipeline.release: docker exec: %w", err)
		}
		if exitCode != 0 {
			return nil, fmt.Errorf("cic.pipeline.release: make release exit %d: %s", exitCode, strings.TrimSpace(out))
		}

		stdoutDigest := digestString(out)

		// Read the signed artifact from source/<schema>.yaml in build_dir.
		artifactPath := filepath.Join(buildDir, "source", schema+".yaml")
		artifactBytes, err := os.ReadFile(artifactPath)
		if err != nil {
			return nil, fmt.Errorf("cic.pipeline.release: read artifact %q: %w", artifactPath, err)
		}

		artifactDigest := "sha256:" + digestString(string(artifactBytes))
		artifactSize := int64(len(artifactBytes))
		cmdDigest := digestString("docker exec " + builderContainer + " make release VERSION=" + releaseVersion)

		prevStepHash, _ := m["step_hash"].(string)
		stepHash := computeStepHash("release", map[string]string{
			"artifact_digest": artifactDigest,
			"command_digest":  cmdDigest,
			"exit_code":       "0",
			"prev_step_hash":  prevStepHash,
			"stdout_digest":   stdoutDigest,
		})

		out2 := copyMap(m)
		out2["pipeline_step"] = "release"
		out2["step_hash"] = stepHash
		out2["release_exit_code"] = 0
		out2["release_stdout_digest"] = stdoutDigest
		out2["artifact_digest"] = artifactDigest
		out2["artifact_size"] = artifactSize
		// Hand off to cic.source.assert:
		out2["source_assertion"] = string(artifactBytes)
		return out2, nil
	}
}

// --- helpers ---

// computeStepHash returns a Merkle root over the step's named leaves.
// keys is iterated in sorted order for determinism.
func computeStepHash(step string, leaves map[string]string) string {
	// Fixed leaf order: step name first, then sorted keys.
	items := [][]byte{
		merkle.LeafHash("step", step),
	}
	// Deterministic iteration via sorted keys.
	keys := sortedKeys(leaves)
	for _, k := range keys {
		items = append(items, merkle.LeafHash(k, leaves[k]))
	}
	return merkle.HexMerkleRoot(items)
}

// computeTreeDigest returns a content-addressable digest of the source tree at buildDir.
//
// Algorithm (normalization rules):
//
//   - Traversal: filepath.WalkDir in OS readdir order (UTF-8 byte order on Linux/ext4).
//   - Scope: regular files only. Directories, symlinks, and device files are skipped.
//     The .git directory is excluded entirely.
//   - Path encoding: each file's path is the slash-free relative path from buildDir,
//     using the OS-native path separator (always '/' on Linux). No normalization of
//     line endings, permissions, timestamps, or ownership is performed.
//   - Per-file hash: raw sha256 of file bytes (no length prefix, no mode bits).
//   - Accumulator: single SHA-256 updated with (rel_path_bytes || file_sha256_bytes)
//     for each file in WalkDir order. No delimiter between path and hash — the
//     fixed 32-byte sha256 length makes the concatenation unambiguous.
//   - Result format: "sha256:<lowercase-hex-of-accumulator>".
//   - Empty / missing directory: returns sha256("empty-tree") prefixed with "sha256:".
//
// Limitation: this digest is NOT portable across OS/filesystem types that sort
// directory entries differently from Linux ext4. It is suitable for Docker-based
// pipelines where the build directory always lives on the same host filesystem.
func computeTreeDigest(buildDir string) (string, error) {
	if _, err := os.Stat(buildDir); os.IsNotExist(err) {
		sum := sha256.Sum256([]byte("empty-tree"))
		return "sha256:" + hex.EncodeToString(sum[:]), nil
	}
	h := sha256.New()
	err := filepath.WalkDir(buildDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(buildDir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		h.Write([]byte(rel))
		h.Write(sum[:])
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// digestString returns the lowercase hex SHA-256 of s.
func digestString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// copyMap shallow-copies a map[string]interface{}.
func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m)+8)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// sortedKeys returns the keys of m in sorted order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Insertion sort — map is small (< 10 entries), no import needed.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
