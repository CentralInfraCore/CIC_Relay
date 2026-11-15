package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
)

// sleb encodes v as a signed LEB128 (wasm i32.const immediates and section sizes use LEB128).
func sleb(v int) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0) {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

func uleb(v int) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

func section(id byte, body []byte) []byte {
	return append(append([]byte{id}, uleb(len(body))...), body...)
}

func vec(items ...[]byte) []byte {
	out := uleb(len(items))
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

func name(s string) []byte { return append(uleb(len(s)), s...) }

// guestCalling assembles a real wasm module that imports git.<op>, holds request JSON in its memory
// at 0, and exports run(): it calls the import with (0, len, 2048, 1024) and returns the byte count.
// The response is left in the guest's memory at 2048. No toolchain is involved: the bytes are the module.
func guestCalling(op, requestJSON string) []byte {
	t0 := []byte{0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f}
	t1 := []byte{0x60, 0x00, 0x01, 0x7f}
	imp := append(append(name("git"), name(op)...), 0x00, 0x00) // func import, type 0
	code := []byte{0x00}                                        // no locals
	code = append(code, 0x41, 0x00)                             // i32.const 0
	code = append(code, append([]byte{0x41}, sleb(len(requestJSON))...)...)
	code = append(code, append([]byte{0x41}, sleb(2048)...)...)
	code = append(code, append([]byte{0x41}, sleb(1024)...)...)
	code = append(code, 0x10, 0x00, 0x0b) // call import 0; end
	data := append([]byte{0x00, 0x41, 0x00, 0x0b}, append(uleb(len(requestJSON)), requestJSON...)...)

	m := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	m = append(m, section(1, vec(t0, t1))...)
	m = append(m, section(2, vec(imp))...)
	m = append(m, section(3, vec([]byte{0x01}))...)
	m = append(m, section(5, vec([]byte{0x00, 0x01}))...)
	m = append(m, section(7, vec(append(name("run"), 0x00, 0x01), append(name("memory"), 0x02, 0x00)))...)
	m = append(m, section(10, vec(append(uleb(len(code)), code...)))...)
	m = append(m, section(11, vec(data))...)
	return m
}

// runGuest instantiates the assembled guest under instanceName in rt, calls run(), and returns the
// decoded response the host function wrote into the guest's memory.
func runGuest(t *testing.T, rt wazero.Runtime, instanceName, op, requestJSON string) map[string]interface{} {
	t.Helper()
	ctx := context.Background()
	compiled, err := rt.CompileModule(ctx, guestCalling(op, requestJSON))
	require.NoError(t, err)
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(instanceName).WithStartFunctions())
	require.NoError(t, err)
	defer mod.Close(ctx)

	res, err := mod.ExportedFunction("run").Call(ctx)
	require.NoError(t, err)
	n := int32(res[0])
	if n <= 0 {
		return map[string]interface{}{"_n": float64(n)}
	}
	raw, ok := mod.Memory().Read(2048, uint32(n))
	require.True(t, ok)
	out := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return out
}

// The whole path, in a real wazero runtime with a real guest module: guest -> host import -> per-module
// authorization (identity from the runtime) -> argument validation -> git. Each case names the layer
// that must stop it and checks the reason, not just that something failed.
func TestGitFromARealGuest(t *testing.T) {
	ops, work := realGitRepo(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.marker")
	script := filepath.Join(dir, "prog.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho ran > '"+marker+"'\nexit 1\n"), 0o755))
	_ = work

	// The manifest grants repo "r" with remotes that include a hostile string and an unconfigured name,
	// so authorization lets them through and the argument validation underneath is what must stop them.
	manifest := fmt.Sprintf(`
abi:
  imports:
    - module: git
      functions: [status, fetch, push]
capability_manifest:
  git:
    - repo: r
      ops: [status, fetch, push]
      remotes: ["--receive-pack=%s", "upstream", "origin"]
      refs: ["main"]
`, script)
	reg := newCICFlowRegistry(nil, &stubDoer{})
	require.NoError(t, reg.registerVerified("granted@1", []byte(manifest)))
	reg.bindArtifact("granted@1", "art-1")
	reg.noteInstance("granted@1", "art-1")

	rt := wazero.NewRuntime(context.Background())
	defer rt.Close(context.Background())
	require.NoError(t, registerGitHostFunctions(context.Background(), rt, ops, reg))

	reqJSON := func(v map[string]interface{}) string { b, _ := json.Marshal(v); return string(b) }
	errOf := func(out map[string]interface{}) string { s, _ := out["error"].(string); return s }

	t.Run("allowed and harmless: status", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", "status", reqJSON(map[string]interface{}{"repo_id": "r"}))
		assert.Empty(t, errOf(out), "%v", out)
		assert.Contains(t, out, "entries")
	})
	t.Run("authorization stops a module that has no grant", func(t *testing.T) {
		out := runGuest(t, rt, "stranger@1", "status", reqJSON(map[string]interface{}{"repo_id": "r"}))
		assert.Contains(t, errOf(out), "no verified git capability")
	})
	t.Run("authorization stops a stale instance of a granted module", func(t *testing.T) {
		reg.noteInstance("granted@1", "older-artifact")
		defer reg.noteInstance("granted@1", "art-1")
		out := runGuest(t, rt, "granted@1", "status", reqJSON(map[string]interface{}{"repo_id": "r"}))
		assert.Contains(t, errOf(out), "not the verified artifact")
	})
	t.Run("authorization stops an operation outside the manifest", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", "push", reqJSON(map[string]interface{}{"repo_id": "r", "remote": "origin", "refspec": "dev"}))
		assert.Contains(t, errOf(out), "not permitted")
	})
	t.Run("validation stops a hostile remote the manifest happens to list", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", "push", reqJSON(map[string]interface{}{"repo_id": "r", "remote": "--receive-pack=" + script, "refspec": "main"}))
		assert.Contains(t, errOf(out), "invalid argument")
		_, err := os.Stat(marker)
		assert.True(t, os.IsNotExist(err), "a guest-chosen program ran")
	})
	t.Run("validation stops a listed remote that the repository does not configure", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", "fetch", reqJSON(map[string]interface{}{"repo_id": "r", "remote": "upstream"}))
		assert.Contains(t, errOf(out), "not configured")
	})
	t.Run("validation stops a hostile refspec the manifest does not list", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", "push", reqJSON(map[string]interface{}{"repo_id": "r", "remote": "origin", "refspec": "--receive-pack=" + script}))
		assert.NotEmpty(t, errOf(out))
		_, err := os.Stat(marker)
		assert.True(t, os.IsNotExist(err), "a guest-chosen program ran")
	})
}
