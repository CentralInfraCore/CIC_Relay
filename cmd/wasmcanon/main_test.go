package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"centralrelay/pkg/wasmcanon"
)

var (
	hdr = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	raw = append(append([]byte{}, hdr...), 0x00, 0x0a, 0x08, 'c', 'i', 'c', '.', 't', 'e', 's', 't', 0xaa)
)

func runCLI(args []string, in []byte) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, bytes.NewReader(in), &out, &errb)
	return code, out.String(), errb.String()
}

func TestExecIsTheSharedTransformation(t *testing.T) {
	code, out, _ := runCLI([]string{"exec"}, raw)
	assert.Equal(t, 0, code)
	assert.Equal(t, string(hdr), out)
}

func TestDigestIsTheExecutedDigest(t *testing.T) {
	code, out, _ := runCLI([]string{"digest"}, raw)
	want, _ := wasmcanon.ExecutedDigest(raw)
	assert.Equal(t, 0, code)
	assert.Equal(t, want+"\n", out)
}

func TestVersionNamesTheTransformAndTheLibrary(t *testing.T) {
	code, out, _ := runCLI([]string{"version"}, nil)
	assert.Equal(t, 0, code)
	assert.True(t, strings.HasPrefix(out, "wasm-canon/v1 wabin="), out)
}

func TestRefusals(t *testing.T) {
	code, out, errs := runCLI([]string{"exec"}, []byte("not wasm"))
	assert.Equal(t, 1, code)
	assert.Empty(t, out, "nothing is written for a refused module")
	assert.Contains(t, errs, "failed to decode wasm module with wabin")

	code, _, errs = runCLI([]string{"bogus"}, nil)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "unknown command")

	code, _, errs = runCLI(nil, nil)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "usage")

	code, _, errs = runCLI([]string{"exec"}, bytes.Repeat([]byte{0}, maxInput+1))
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "size limit")
}

func TestWabinVersionIsPinned(t *testing.T) {
	// in a test binary the dependency list is present, so the pinned version is read, never "unknown"
	assert.Contains(t, wabinVersion(), "20230304001439-f6f874872834")
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func TestIOFailuresAreReported(t *testing.T) {
	var out, errb bytes.Buffer
	assert.Equal(t, 1, run([]string{"exec"}, failReader{}, &out, &errb))
	assert.Contains(t, errb.String(), "read: boom")

	errb.Reset()
	assert.Equal(t, 1, run([]string{"exec"}, bytes.NewReader(raw), failWriter{}, &errb))
	assert.Contains(t, errb.String(), "write: boom")
}

func TestTheLimitIsInclusive(t *testing.T) {
	// exactly maxInput bytes are read (and then refused as not wasm, not as too large)
	code, _, errs := runCLI([]string{"exec"}, bytes.Repeat([]byte{0}, maxInput))
	assert.Equal(t, 1, code)
	assert.NotContains(t, errs, "size limit")
}
