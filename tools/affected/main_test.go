package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// newTestModule builds a temporary module "testmod" with a dependency chain
// pkgc -> pkgb -> pkga, plus an unrelated pkgd, and returns its root dir.
//
//	pkga (leaf)
//	pkgb imports pkga
//	pkgc imports pkgb
//	pkgd imports nothing, is imported by nothing
func newTestModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"go.mod": "module testmod\n\ngo 1.21\n",
		"pkga/pkga.go": `package pkga

func A() string { return "a" }
`,
		"pkgb/pkgb.go": `package pkgb

import "testmod/pkga"

func B() string { return pkga.A() }
`,
		"pkgc/pkgc.go": `package pkgc

import "testmod/pkgb"

func C() string { return pkgb.B() }
`,
		"pkgd/pkgd.go": `package pkgd

func D() string { return "d" }
`,
	}

	for relPath, content := range files {
		full := filepath.Join(dir, relPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}

	return dir
}

func TestAffectedPackages_TransitiveImporters(t *testing.T) {
	dir := newTestModule(t)
	cfg := &packages.Config{Dir: dir}

	result, err := affectedPackages(cfg, []string{"pkga/pkga.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"testmod/pkga", "testmod/pkgb", "testmod/pkgc"}, result)
}

func TestAffectedPackages_LeafChange(t *testing.T) {
	dir := newTestModule(t)
	cfg := &packages.Config{Dir: dir}

	result, err := affectedPackages(cfg, []string{"pkgc/pkgc.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"testmod/pkgc"}, result)
}

func TestAffectedPackages_UnrelatedPackage(t *testing.T) {
	dir := newTestModule(t)
	cfg := &packages.Config{Dir: dir}

	result, err := affectedPackages(cfg, []string{"pkgd/pkgd.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"testmod/pkgd"}, result)
}

func TestAffectedPackages_MultipleChangedFiles(t *testing.T) {
	dir := newTestModule(t)
	cfg := &packages.Config{Dir: dir}

	result, err := affectedPackages(cfg, []string{"pkgd/pkgd.go", "pkga/pkga.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"testmod/pkga", "testmod/pkgb", "testmod/pkgc", "testmod/pkgd"}, result)
}

func TestAffectedPackages_NoChangedFiles(t *testing.T) {
	dir := newTestModule(t)
	cfg := &packages.Config{Dir: dir}

	result, err := affectedPackages(cfg, nil)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestAffectedPackages_NonGoFileIgnored(t *testing.T) {
	dir := newTestModule(t)
	cfg := &packages.Config{Dir: dir}

	result, err := affectedPackages(cfg, []string{"README.md", "  "})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestAffectedPackages_UnknownFileIgnored(t *testing.T) {
	dir := newTestModule(t)
	cfg := &packages.Config{Dir: dir}

	result, err := affectedPackages(cfg, []string{"nonexistent/file.go"})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestAffectedPackages_LoadError(t *testing.T) {
	cfg := &packages.Config{Dir: filepath.Join(t.TempDir(), "does-not-exist")}

	_, err := affectedPackages(cfg, []string{"pkga/pkga.go"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load packages")
}

func TestMain_PrintsAffectedPackages(t *testing.T) {
	dir := newTestModule(t)

	bin := filepath.Join(t.TempDir(), "affected")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	out, err := build.CombinedOutput()
	require.NoError(t, err, "build failed: %s", out)

	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewBufferString("pkga/pkga.go\n")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard

	require.NoError(t, cmd.Run())
	assert.Equal(t, "testmod/pkga\ntestmod/pkgb\ntestmod/pkgc\n", stdout.String())
}

func TestMain_FatalOnLoadError(t *testing.T) {
	oldFatalf := fatalf
	t.Cleanup(func() { fatalf = oldFatalf })

	called := false
	fatalf = func(format string, args ...interface{}) { called = true }

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })
	require.NoError(t, os.Chdir(t.TempDir()))

	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString("foo.go\n")
	require.NoError(t, err)
	require.NoError(t, w.Close())
	os.Stdin = r

	main()
	assert.True(t, called, "fatalf must be called when affectedPackages fails")
}
