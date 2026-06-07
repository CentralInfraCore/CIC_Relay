// Package cibuild implements the ci.build native module.
// It runs a make target and returns the artifact path + SHA-256 digest,
// so the result can be recorded in the ProofTrace as a step output.
package cibuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/sirupsen/logrus"
)

// Input is the expected input structure for the ci.build module.
type Input struct {
	BuildTarget    string `json:"build_target"`    // working directory for make (optional)
	MakeTarget     string `json:"make_target"`     // make target to run (default: "build")
	ArtifactOutput string `json:"artifact_output"` // path to the produced artifact (required)
}

// Output is the result returned by Execute.
type Output struct {
	ArtifactPath   string `json:"artifact_path"`
	ArtifactDigest string `json:"artifact_digest"`
	ExitCode       int    `json:"exit_code"`
}

// buildTimeout is the maximum time a single make invocation may run.
const buildTimeout = 5 * time.Minute

// Execute runs the build and returns an Output. Satisfies the cabinet NativeFunc signature:
//
//	func(context.Context, interface{}) (interface{}, error)
func Execute(ctx context.Context, input interface{}) (interface{}, error) {
	in, err := parseInput(input)
	if err != nil {
		return nil, fmt.Errorf("ci.build: invalid input: %w", err)
	}

	target := in.MakeTarget
	if target == "" {
		target = "build"
	}

	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	cmd := exec.CommandContext(buildCtx, "make", target)
	if in.BuildTarget != "" {
		cmd.Dir = in.BuildTarget
	}

	logrus.WithFields(logrus.Fields{
		"make_target":  target,
		"build_target": in.BuildTarget,
	}).Info("ci.build: running make")

	out, err := cmd.CombinedOutput()
	if err != nil {
		logrus.WithField("output", string(out)).Warn("ci.build: make failed")
		return nil, fmt.Errorf("ci.build: make %s failed: %w", target, err)
	}
	logrus.WithField("output", string(out)).Info("ci.build: make succeeded")

	digest, err := fileDigest(in.ArtifactOutput)
	if err != nil {
		return nil, fmt.Errorf("ci.build: artifact digest for %q: %w", in.ArtifactOutput, err)
	}

	return Output{
		ArtifactPath:   in.ArtifactOutput,
		ArtifactDigest: digest,
		ExitCode:       0,
	}, nil
}

// fileDigest returns the hex-encoded SHA-256 digest of a file.
func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// parseInput extracts Input from the raw interface{} value passed by the cabinet.
func parseInput(raw interface{}) (*Input, error) {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("expected map[string]interface{}, got %T", raw)
	}
	in := &Input{}
	if v, ok := m["build_target"].(string); ok {
		in.BuildTarget = v
	}
	if v, ok := m["make_target"].(string); ok {
		in.MakeTarget = v
	}
	if v, ok := m["artifact_output"].(string); ok {
		in.ArtifactOutput = v
	}
	if in.ArtifactOutput == "" {
		return nil, fmt.Errorf("artifact_output is required")
	}
	return in, nil
}
