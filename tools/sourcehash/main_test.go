package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain_Success(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/a.go", []byte("package a"), 0o644))

	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"sourcehash", dir}

	// Should not call osExit
	origExit := osExit
	called := false
	osExit = func(code int) { called = true }
	t.Cleanup(func() { osExit = origExit })

	main()
	assert.False(t, called, "osExit must not be called on success")
}

func TestMain_Error(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"sourcehash", "/nonexistent-dir-xyz-abc"}

	exitCode := 0
	origExit := osExit
	osExit = func(code int) { exitCode = code }
	t.Cleanup(func() { osExit = origExit })

	main()
	assert.Equal(t, 1, exitCode)
}

func TestMain_DefaultRoot(t *testing.T) {
	// No args → uses "." which exists
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"sourcehash"}

	called := false
	origExit := osExit
	osExit = func(code int) { called = true }
	t.Cleanup(func() { osExit = origExit })

	main()
	assert.False(t, called)
}
