package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The git child gets the allowlisted variables and the fixed hardening values, nothing else. The test
// puts a fake "git" first on PATH that records its environment, so it observes the real child process.
func TestGitChildEnvironment_IsAllowlisted(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "env.txt")
	fake := filepath.Join(dir, "git")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nenv > '"+record+"'\n"), 0o755))

	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("CIC_API_TOKEN", "secret-api-token")
	t.Setenv("VAULT_TOKEN", "secret-vault-token")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file:ext")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_EXEC_PATH", "/tmp/elsewhere")
	t.Setenv("GIT_SSH_COMMAND", "ssh -o BatchMode=yes")

	reg := NewRepoRegistry()
	reg.Register("r", t.TempDir())
	_, err := NewExecGitOps(reg).Status(context.Background(), "r")
	require.NoError(t, err)

	raw, err2 := os.ReadFile(record)
	err = err2
	require.NoError(t, err)
	got := string(raw)
	for _, leaked := range []string{"secret-api-token", "secret-vault-token", "CIC_API_TOKEN", "VAULT_TOKEN", "GIT_CONFIG_COUNT", "GIT_EXEC_PATH", "file:ext"} {
		assert.NotContains(t, got, leaked, "the git child must not inherit %s", leaked)
	}
	assert.Contains(t, got, "GIT_ALLOW_PROTOCOL=https:ssh\n", "the fixed value wins over an inherited one")
	assert.Contains(t, got, "GIT_SSH_COMMAND=ssh -o BatchMode=yes", "operator transport settings on the allowlist pass through")
	assert.True(t, strings.Contains(got, "PATH="), "PATH passes through")
}
