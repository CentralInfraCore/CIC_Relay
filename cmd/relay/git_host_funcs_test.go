package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	nexusgit "centralrelay/core/nexus/git"
)

// --- minimal api.Memory + api.Module mocks ---

type fixedMem struct {
	api.Memory
	buf []byte
}

func (m *fixedMem) Read(offset, count uint32) ([]byte, bool) {
	end := int(offset + count)
	if end > len(m.buf) {
		return nil, false
	}
	out := make([]byte, count)
	copy(out, m.buf[offset:end])
	return out, true
}

func (m *fixedMem) Write(offset uint32, v []byte) bool {
	end := int(offset) + len(v)
	if end > len(m.buf) {
		return false
	}
	copy(m.buf[offset:], v)
	return true
}

type minMod struct {
	api.Module
	mem *fixedMem
}

func (m *minMod) Memory() api.Memory { return m.mem }

func newMod(size int) *minMod {
	return &minMod{mem: &fixedMem{buf: make([]byte, size)}}
}

func putJSON(mod *minMod, offset uint32, v interface{}) uint32 {
	data, _ := json.Marshal(v)
	mod.mem.Write(offset, data)
	return uint32(len(data))
}

// --- minimal GitOps stub ---

type stubGitOps struct {
	cloneErr    error
	fetchErr    error
	checkoutErr error
	commitHash  string
	commitErr   error
	pushErr     error
	statusErr   error
}

func (s *stubGitOps) Clone(_ context.Context, _, _, _ string) error { return s.cloneErr }
func (s *stubGitOps) Fetch(_ context.Context, _, _ string) error    { return s.fetchErr }
func (s *stubGitOps) Checkout(_ context.Context, _, _ string) error  { return s.checkoutErr }
func (s *stubGitOps) Commit(_ context.Context, _, _ string, _ []string) (string, error) {
	return s.commitHash, s.commitErr
}
func (s *stubGitOps) Push(_ context.Context, _, _, _ string) error { return s.pushErr }
func (s *stubGitOps) Status(_ context.Context, _ string) ([]nexusgit.StatusEntry, error) {
	return nil, s.statusErr
}

// --- gitReadJSON / gitWriteJSON / gitWriteError ---

func TestGitReadJSON_OK(t *testing.T) {
	mod := newMod(64)
	data, _ := json.Marshal(map[string]string{"key": "val"})
	mod.mem.Write(0, data)
	var out map[string]string
	assert.True(t, gitReadJSON(mod, 0, uint32(len(data)), &out))
	assert.Equal(t, "val", out["key"])
}

func TestGitReadJSON_BadOffset(t *testing.T) {
	mod := newMod(4)
	var out map[string]string
	assert.False(t, gitReadJSON(mod, 0, 100, &out))
}

func TestGitReadJSON_BadJSON(t *testing.T) {
	mod := newMod(16)
	mod.mem.Write(0, []byte("not-json"))
	var out map[string]string
	assert.False(t, gitReadJSON(mod, 0, 8, &out))
}

func TestGitWriteJSON_OK(t *testing.T) {
	mod := newMod(64)
	n := gitWriteJSON(mod, 0, 64, map[string]bool{"ok": true})
	assert.Greater(t, n, int32(0))
	out, ok := mod.mem.Read(0, uint32(n))
	require.True(t, ok)
	assert.Contains(t, string(out), "ok")
}

func TestGitWriteJSON_BufferTooSmall(t *testing.T) {
	mod := newMod(4)
	n := gitWriteJSON(mod, 0, 4, map[string]string{"key": "this-is-too-long-for-the-buffer"})
	assert.Equal(t, int32(-1), n)
}

func TestGitWriteJSON_WriteFailure(t *testing.T) {
	mod := newMod(4)
	n := gitWriteJSON(mod, 200, 64, map[string]bool{"ok": true})
	assert.Equal(t, int32(-1), n)
}

func TestGitWriteError_WritesErrorField(t *testing.T) {
	mod := newMod(64)
	n := gitWriteError(mod, 0, 64, "something went wrong")
	require.Greater(t, n, int32(0))
	out, _ := mod.mem.Read(0, uint32(n))
	assert.Contains(t, string(out), "something went wrong")
}

// --- registerGitHostFunctions + makeGitHostSetupFn ---

func TestRegisterGitHostFunctions(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	require.NoError(t, registerGitHostFunctions(ctx, r, &stubGitOps{}))
}

func TestMakeGitHostSetupFn(t *testing.T) {
	ctx := context.Background()
	fn := makeGitHostSetupFn(&stubGitOps{})
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	require.NoError(t, fn(ctx, r))
}

// --- make*Fn closures via mock module ---

func TestMakeCloneFn_Success(t *testing.T) {
	fn := makeCloneFn(&stubGitOps{})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r", "url": "u", "branch": "main"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	assert.Greater(t, n, int32(0))
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "ok")
}

func TestMakeCloneFn_Error(t *testing.T) {
	fn := makeCloneFn(&stubGitOps{cloneErr: errors.New("fail")})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r", "url": "u", "branch": "main"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "error")
}

func TestMakeCloneFn_BadJSON(t *testing.T) {
	fn := makeCloneFn(&stubGitOps{})
	mod := newMod(64)
	mod.mem.Write(0, []byte("badjson"))
	n := fn(context.Background(), mod, 0, 7, 32, 32)
	out, _ := mod.mem.Read(32, uint32(n))
	assert.Contains(t, string(out), "error")
}

func TestMakeFetchFn_Success(t *testing.T) {
	fn := makeFetchFn(&stubGitOps{})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r", "remote": "origin"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "ok")
}

func TestMakeFetchFn_Error(t *testing.T) {
	fn := makeFetchFn(&stubGitOps{fetchErr: errors.New("fail")})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r", "remote": "origin"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "error")
}

func TestMakeCheckoutFn_Success(t *testing.T) {
	fn := makeCheckoutFn(&stubGitOps{})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r", "ref": "main"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "ok")
}

func TestMakeCheckoutFn_Error(t *testing.T) {
	fn := makeCheckoutFn(&stubGitOps{checkoutErr: errors.New("fail")})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r", "ref": "main"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "error")
}

func TestMakeCommitFn_Success(t *testing.T) {
	fn := makeCommitFn(&stubGitOps{commitHash: "abc123"})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]interface{}{"repo_id": "r", "message": "msg", "paths": []string{"f.txt"}})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "abc123")
}

func TestMakeCommitFn_Error(t *testing.T) {
	fn := makeCommitFn(&stubGitOps{commitErr: errors.New("fail")})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]interface{}{"repo_id": "r", "message": "msg", "paths": nil})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "error")
}

func TestMakePushFn_Success(t *testing.T) {
	fn := makePushFn(&stubGitOps{})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r", "remote": "origin", "refspec": "main"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "ok")
}

func TestMakePushFn_Error(t *testing.T) {
	fn := makePushFn(&stubGitOps{pushErr: errors.New("fail")})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r", "remote": "origin", "refspec": "main"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "error")
}

func TestMakeStatusFn_Success(t *testing.T) {
	fn := makeStatusFn(&stubGitOps{})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	assert.Greater(t, n, int32(0))
}

func TestMakeStatusFn_Error(t *testing.T) {
	fn := makeStatusFn(&stubGitOps{statusErr: errors.New("fail")})
	mod := newMod(256)
	l := putJSON(mod, 0, map[string]string{"repo_id": "r"})
	n := fn(context.Background(), mod, 0, l, 128, 128)
	out, _ := mod.mem.Read(128, uint32(n))
	assert.Contains(t, string(out), "error")
}
