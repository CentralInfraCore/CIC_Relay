package recorder

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"centralrelay/core/nexus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commitCount returns the number of commits reachable from HEAD.
func commitCount(t *testing.T, repo string) int {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	require.NoError(t, err)
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	require.NoError(t, err)
	return n
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.Output()
	require.NoError(t, err, "git %s", strings.Join(args, " "))
	return strings.TrimSpace(string(out))
}

func gitLines(t *testing.T, repo string, args ...string) []string {
	t.Helper()
	out := gitOutput(t, repo, args...)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// slowSigningMock is signingMock with a delay inside Sign. The delay widens the
// window between the HEAD read and the commit, so overlapping calls are certain
// to interleave unless signAndCommit is serialized.
func slowSigningMock(delay time.Duration) *MockCryptoService {
	m := signingMock()
	m.SignFunc = func(_ context.Context, data []byte) (string, error) {
		time.Sleep(delay)
		return "sig:" + hex.EncodeToString(data), nil
	}
	return m
}

// Concurrent workflows share one recorder: every record must land as its own
// commit and the signed chain must still verify (no lost records, no false
// "chain broken").
func TestSignAndCommit_ConcurrentRecordsKeepChainVerifiable(t *testing.T) {
	const n = 12
	repo := t.TempDir()
	r, err := NewGitStateRecorder(repo, slowSigningMock(25*time.Millisecond))
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runID := fmt.Sprintf("run-%02d", i)
			if i%2 == 0 {
				_, errs[i] = r.RecordState(context.Background(), types.ExpectedState{RunID: runID}, map[string]string{"i": runID})
			} else {
				_, _, errs[i] = r.RecordDrift(context.Background(), runID,
					map[string]interface{}{"i": runID}, map[string]interface{}{"i": runID})
			}
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		assert.NoError(t, e, "record %d must not fail", i)
	}
	assert.Equal(t, n+1, commitCount(t, repo), "root commit plus exactly one commit per record")
	assert.NoError(t, r.VerifyChain(context.Background()), "the signed chain must verify")

	// A valid chain with the right number of commits does not prove that every record
	// survived intact: each of the n distinct run-ids must appear exactly once, and the
	// signed meta.json of each commit must carry the run-id its commit message names
	// (overwritten files would pair one workflow's message with another's record).
	got := map[string]int{}
	for _, c := range gitLines(t, repo, "rev-list", "HEAD") {
		subject := gitOutput(t, repo, "show", "-s", "--format=%s", c)
		runID, ok := strings.CutPrefix(subject, "[AUTO] run-id: ")
		if !ok {
			continue // the root commit
		}
		got[runID]++
		var meta struct {
			RunID string `json:"run_id"`
		}
		require.NoError(t, json.Unmarshal([]byte(gitOutput(t, repo, "show", c+":meta.json")), &meta))
		assert.Equal(t, runID, meta.RunID, "commit %s: message and signed meta name different runs", c)
	}
	for i := 0; i < n; i++ {
		runID := fmt.Sprintf("run-%02d", i)
		assert.Equal(t, 1, got[runID], "run-id %s must be recorded exactly once", runID)
	}
	assert.Len(t, got, n)
}

// A failed HEAD read must fail the record. Signing an empty parent would put a
// record with a forged chain position into the audit log.
func TestSignAndCommit_HeadReadFailureIsAnError(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	// A repository without a single commit: `git rev-parse HEAD` fails.
	r := &GitStateRecorder{RepoPath: repo, CryptoService: signingMock()}

	hash, err := r.RecordState(context.Background(), types.ExpectedState{RunID: "r1"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parent commit")
	assert.Empty(t, hash)

	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = repo
	assert.Error(t, cmd.Run(), "no commit may be created when the parent cannot be read")
}
