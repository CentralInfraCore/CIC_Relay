package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"

	"centralrelay/core/cabinet"
	nexusgit "centralrelay/core/nexus/git"
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
func runGuest(t *testing.T, rt wazero.Runtime, instanceName string, ident *cabinet.InstanceIdentity, op, requestJSON string) map[string]interface{} {
	t.Helper()
	ctx := context.Background()
	if ident != nil { // what the WasmManager does: the identity travels on the context of every call
		ctx = cabinet.WithInstanceIdentity(ctx, *ident)
	}
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
	require.NoError(t, reg.publishVerified("granted@1", []byte(manifest), "art-1"))

	rt := wazero.NewRuntime(context.Background())
	defer rt.Close(context.Background())
	require.NoError(t, registerGitHostFunctions(context.Background(), rt, ops, reg))

	gid := inst("granted@1", "art-1")
	sid := inst("stranger@1", "art-1")
	reqJSON := func(v map[string]interface{}) string { b, _ := json.Marshal(v); return string(b) }
	errOf := func(out map[string]interface{}) string { s, _ := out["error"].(string); return s }

	t.Run("allowed and harmless: status", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", &gid, "status", reqJSON(map[string]interface{}{"repo_id": "r"}))
		assert.Empty(t, errOf(out), "%v", out)
		assert.Contains(t, out, "entries")
	})
	t.Run("authorization stops a module that has no grant", func(t *testing.T) {
		out := runGuest(t, rt, "stranger@1", &sid, "status", reqJSON(map[string]interface{}{"repo_id": "r"}))
		assert.Contains(t, errOf(out), "no verified git capability")
	})
	t.Run("authorization stops a stale instance of a granted module", func(t *testing.T) {
		older := inst("granted@1", "older-artifact")
		out := runGuest(t, rt, "granted@1", &older, "status", reqJSON(map[string]interface{}{"repo_id": "r"}))
		assert.Contains(t, errOf(out), "not the verified artifact")
	})
	t.Run("a call with no instance identity is refused", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", nil, "status", reqJSON(map[string]interface{}{"repo_id": "r"}))
		assert.Contains(t, errOf(out), "not an identified module instance")
	})
	t.Run("authorization stops an operation outside the manifest", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", &gid, "push", reqJSON(map[string]interface{}{"repo_id": "r", "remote": "origin", "refspec": "dev"}))
		assert.Contains(t, errOf(out), "not permitted")
	})
	t.Run("validation stops a hostile remote the manifest happens to list", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", &gid, "push", reqJSON(map[string]interface{}{"repo_id": "r", "remote": "--receive-pack=" + script, "refspec": "main"}))
		assert.Contains(t, errOf(out), "invalid argument")
		_, err := os.Stat(marker)
		assert.True(t, os.IsNotExist(err), "a guest-chosen program ran")
	})
	t.Run("validation stops a listed remote that the repository does not configure", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", &gid, "fetch", reqJSON(map[string]interface{}{"repo_id": "r", "remote": "upstream"}))
		assert.Contains(t, errOf(out), "not configured")
	})
	t.Run("validation stops a hostile refspec the manifest does not list", func(t *testing.T) {
		out := runGuest(t, rt, "granted@1", &gid, "push", reqJSON(map[string]interface{}{"repo_id": "r", "remote": "origin", "refspec": "--receive-pack=" + script}))
		assert.NotEmpty(t, errOf(out))
		_, err := os.Stat(marker)
		assert.True(t, os.IsNotExist(err), "a guest-chosen program ran")
	})
}

// guestWithStartSection assembles a module whose WebAssembly START SECTION calls git.status once, while
// it is being instantiated: guest code that runs before any exported function does.
func guestWithStartSection(requestJSON string) []byte {
	t0 := []byte{0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f} // (i32 i32 i32 i32) -> i32
	t1 := []byte{0x60, 0x00, 0x00}                               // () -> ()
	imp := append(append(name("git"), name("status")...), 0x00, 0x00)
	code := []byte{0x00, 0x41, 0x00}
	code = append(code, append([]byte{0x41}, sleb(len(requestJSON))...)...)
	code = append(code, append([]byte{0x41}, sleb(2048)...)...)
	code = append(code, append([]byte{0x41}, sleb(1024)...)...)
	code = append(code, 0x10, 0x00, 0x1a, 0x0b) // call import 0; drop; end
	data := append([]byte{0x00, 0x41, 0x00, 0x0b}, append(uleb(len(requestJSON)), requestJSON...)...)

	m := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	m = append(m, section(1, vec(t0, t1))...)
	m = append(m, section(2, vec(imp))...)
	m = append(m, section(3, vec([]byte{0x01}))...)
	m = append(m, section(5, vec([]byte{0x00, 0x01}))...)
	m = append(m, section(7, vec(append(name("memory"), 0x02, 0x00)))...)
	m = append(m, section(8, uleb(1))...) // start section: the defined function, index 1
	m = append(m, section(10, vec(append(uleb(len(code)), code...)))...)
	m = append(m, section(11, vec(data))...)
	return m
}

type statusCounter struct {
	stubGitOps
	n int
}

func (c *statusCounter) Status(ctx context.Context, repoID string) ([]nexusgit.StatusEntry, error) {
	c.n++
	return nil, nil
}

// F9: the start section runs inside InstantiateModule, before the host has a handle on the instance. It
// runs under the identity the manager created for THIS instance and THIS artifact: it can use a grant
// bound to that artifact, and can not use one bound to another, nor one left over from an earlier instance
// of the same name. Real WasmManager, real start-section guest, no simulation of the identity.
func TestGitFromAStartSection_RunsUnderTheNewInstancesOwnIdentity(t *testing.T) {
	ctx := context.Background()
	manifest := "abi:\n  imports:\n    - module: git\n      functions: [status]\ncapability_manifest:\n  git:\n    - repo: infra\n      ops: [status]\n"
	// Two different artifacts of the same module ID: the request JSON differs by a space.
	wasmX := guestWithStartSection(`{"repo_id":"infra"}`)
	wasmY := guestWithStartSection(`{"repo_id":"infra" }`)
	const id = "sg@1"

	store := cabinet.NewMemModuleStore()
	put := func(w []byte) string {
		md, err := cabinet.NewModuleDescriptor(ctx, cabinet.BaseBlock{Name: "sg", Version: "1"}, "wasm", nil, nil, w)
		require.NoError(t, err)
		store.Put(md)
		return md.Checksum()
	}
	artX := put(wasmX)

	reg := newCICFlowRegistry(nil, &stubDoer{})
	ops := &statusCounter{}
	mgr, err := cabinet.NewWasmManager(ctx, store, cabinet.WasmManagerConfig{
		LRUCacheSize: 10, DefaultTimeoutSeconds: 1, HostSetupFn: makeGitHostSetupFn(ops, reg)})
	require.NoError(t, err)
	defer mgr.Close(ctx)

	// The guest exports no Call/allocate/deallocate, so NewHostInstance reports that after the instance
	// exists; what is observed here is what the start section did on the way.
	start := func() int {
		before := ops.n
		_, _ = mgr.NewHostInstance(ctx, id)
		return ops.n - before
	}

	assert.Equal(t, 0, start(), "no grant at all: the start section's call is refused")

	grant := func(artifact string) { require.NoError(t, reg.publishVerified(id, []byte(manifest), artifact)) }
	grant("some-other-artifact")
	assert.Equal(t, 0, start(), "a grant bound to a different artifact: refused")

	grant(artX)
	assert.Equal(t, 1, start(), "a grant bound to this instance's artifact: the start section may use it")

	// The module is replaced by another artifact. The grant is still bound to the old one; the new
	// instance must not be served with it, and must not be mistaken for the old instance either.
	artY := put(wasmY)
	require.NotEqual(t, artX, artY)
	assert.Equal(t, 0, start(), "the new instance runs artifact Y; a grant for X does not apply to it")
	grant(artY)
	assert.Equal(t, 1, start(), "re-granted for Y: the new instance's start section is served, with Y's identity")
	put(wasmX)
	assert.Equal(t, 0, start(), "back to X while the grant is for Y: refused, nothing is inherited from the earlier instance")
}

// ---- F8: a grant and its artifact are published as one, and loads of one module ID do not interleave ----

// Two manifests that differ in the repository they name, published concurrently each with its own
// artifact. An observer reads the registry the whole time: every state it ever sees pairs a manifest with
// the artifact that was published WITH it (or has no artifact at all, which is never usable), never the
// manifest of one call and the artifact of another.
func TestGitGrant_PublishPairsTheManifestWithItsOwnArtifact(t *testing.T) {
	mk := func(repo string) []byte {
		return []byte("abi:\n  imports: []\ncapability_manifest:\n  git:\n    - repo: " + repo + "\n      ops: [status]\n")
	}
	reg := newCICFlowRegistry(nil, &stubDoer{})
	const id = "m@1"
	pairs := map[string]string{"artifact-a": "repo-a", "artifact-b": "repo-b"}

	stop := make(chan struct{})
	var observed, bad atomic.Int64
	var obs sync.WaitGroup
	obs.Add(1)
	go func() {
		defer obs.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			reg.mu.RLock()
			g, ok := reg.git[id]
			reg.mu.RUnlock()
			if !ok || g.artifact == "" {
				continue
			}
			observed.Add(1)
			if pairs[g.artifact] != g.rules[0].Repo {
				bad.Add(1)
			}
		}
	}()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			repo, art := "repo-a", "artifact-a"
			if g%2 == 1 {
				repo, art = "repo-b", "artifact-b"
			}
			for n := 0; n < 300; n++ {
				require.NoError(t, reg.publishVerified(id, mk(repo), art))
			}
		}(g)
	}
	wg.Wait()
	close(stop)
	obs.Wait()
	assert.Positive(t, observed.Load(), "the observer must have seen published grants")
	assert.Zero(t, bad.Load(), "a grant paired a manifest with another call's artifact")
}

// Concurrent loads of the same module ID with two different, verified artifacts: when they are done, the
// cabinet holds one artifact and the registry holds the grant that was published for THAT artifact.
// (Run with -race.) Loads are serialised, so there is no interleaving to get wrong.
func TestLoadModule_ConcurrentLoadsOfOneIDLeaveGrantAndArtifactTogether(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	defer hooks(reg)()
	svc := newCabinetForTest(t)
	const id = "gitmod@1.0"
	pki := loadedPKIWithSource(t)
	fsX, yX := gitModuleFS(t, guestWithStartSection(`{"repo_id":"infra"}`))
	fsY, yY := gitModuleFS(t, guestWithStartSection(`{"repo_id":"infra" }`))

	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			fsys, y := fsX, yX
			if g%2 == 1 {
				fsys, y = fsY, yY
			}
			for n := 0; n < 40; n++ {
				require.NoError(t, loadModule(context.Background(), svc, fsys, "m.yaml", y, pki, int64(16*1024*1024), newDiscardLogger()))
			}
		}(g)
	}
	wg.Wait()

	stored, err := svc.GetModule(id)
	require.NoError(t, err)
	assert.Equal(t, stored.Checksum(), reg.git[id].artifact, "the registry's grant belongs to the artifact the cabinet holds")
}

// Loads of one module ID are serialised: while one load sits between publishing its grant and storing its
// artifact, another load of the same ID can not start. Deterministic: the first load is held at exactly
// that point and the second is given time to run; it must not.
func TestLoadModule_LoadsOfOneIDDoNotInterleave(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	defer hooks(reg)()
	svc := newCabinetForTest(t)
	pki := loadedPKIWithSource(t)
	fsX, yX := gitModuleFS(t, guestWithStartSection(`{"repo_id":"infra"}`))
	fsY, yY := gitModuleFS(t, guestWithStartSection(`{"repo_id":"infra" }`))
	load := func(fsys fstest.MapFS, y []byte) error {
		return loadModule(context.Background(), svc, fsys, "m.yaml", y, pki, int64(16*1024*1024), newDiscardLogger())
	}

	held, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool // not sync.Once: a second caller of Once.Do would wait for the first, hiding the very thing tested
	afterCapabilityPublished = func(string) {
		if first.CompareAndSwap(false, true) {
			close(held)
			<-release
		}
	}
	defer func() { afterCapabilityPublished = nil }()

	firstDone := make(chan error, 1)
	go func() { firstDone <- load(fsX, yX) }()
	<-held // the first load has published its grant and not yet stored its artifact

	secondDone := make(chan error, 1)
	go func() { secondDone <- load(fsY, yY) }()
	select {
	case <-secondDone:
		t.Fatal("a second load of the same module ID ran inside the first load's publish-and-store section")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)

	stored, err := svc.GetModule("gitmod@1.0")
	require.NoError(t, err)
	assert.Equal(t, stored.Checksum(), reg.git["gitmod@1.0"].artifact, "grant and artifact belong to the same (the second) load")
}

// A module whose artifact can not be built gets no grant, and loses the one it had.
func TestLoadModule_NoArtifactNoGrant(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	defer hooks(reg)()
	svc := newCabinetForTest(t)
	pki := loadedPKIWithSource(t)
	const id = "gitmod@1.0"

	good, yGood := gitModuleFS(t, guestWithStartSection(`{"repo_id":"infra"}`))
	require.NoError(t, loadModule(context.Background(), svc, good, "m.yaml", yGood, pki, int64(16*1024*1024), newDiscardLogger()))
	require.NotEmpty(t, reg.git[id].artifact)

	broken, yBroken := gitModuleFS(t, []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0xff, 0xff})
	err := loadModule(context.Background(), svc, broken, "m.yaml", yBroken, pki, int64(16*1024*1024), newDiscardLogger())
	require.Error(t, err)
	_, still := reg.git[id]
	assert.False(t, still, "the artifact could not be built: neither the new grant nor the previous one remains")
}

// ---- F8 + F9 together: one authorization lifecycle, from the verified load to the running instance ----

// The real loader publishes the grant; the real manager creates the instance; the guest's start section
// asks git for something. Across reloads, an unverified reload, and loads racing with instantiation, the
// start section is served exactly when the instance's artifact is the one the current grant was published
// for.
func TestGitCapabilityLifecycle_LoadThenInstantiate(t *testing.T) {
	ctx := context.Background()
	reg := newCICFlowRegistry(nil, &stubDoer{})
	defer hooks(reg)()
	ops := &statusCounter{}

	store := cabinet.NewMemModuleStore()
	svc, err := cabinet.NewCabinetService(ctx, cabinet.NewMemSchemaStore(), store, cabinet.NewMemWorkflowStore(),
		cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1})
	require.NoError(t, err)
	mgr, err := cabinet.NewWasmManager(ctx, store, cabinet.WasmManagerConfig{
		LRUCacheSize: 10, DefaultTimeoutSeconds: 1, HostSetupFn: makeGitHostSetupFn(ops, reg)})
	require.NoError(t, err)
	defer mgr.Close(ctx)

	const id = "gitmod@1.0"
	verified, unverified := loadedPKIWithSource(t), buildLoadedPKIAssets(t)
	load := func(wasm []byte, pki *PKIAssets) {
		fsys, y := gitModuleFS(t, wasm)
		require.NoError(t, loadModule(ctx, svc, fsys, "m.yaml", y, pki, int64(16*1024*1024), newDiscardLogger()))
	}
	servedOnStart := func() int {
		before := ops.n
		_, _ = mgr.NewHostInstance(ctx, id)
		return ops.n - before
	}
	x, y := guestWithStartSection(`{"repo_id":"infra"}`), guestWithStartSection(`{"repo_id":"infra" }`)

	load(x, verified)
	assert.Equal(t, 1, servedOnStart(), "verified load: the instance's start section is served")
	load(y, verified)
	assert.Equal(t, 1, servedOnStart(), "verified reload with another artifact: served, under the new artifact's grant")
	load(x, unverified)
	assert.Equal(t, 0, servedOnStart(), "unverified reload: no grant, nothing is served, nothing is inherited")
	load(y, verified)
	assert.Equal(t, 1, servedOnStart(), "verified again: served")

	// Loads race with instantiation: every start section that is served ran under the grant of the
	// artifact the instance was built from; none is served from a half-published state. With verified
	// loads of two artifacts the grant is always present once published, so a refused start section can
	// only be a window where the previous grant was revoked and the new one not yet published: refusing
	// there is the intended fail-closed behaviour, serving the wrong artifact is not possible.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
				if n%2 == 0 {
					load(x, verified)
				} else {
					load(y, verified)
				}
			}
		}
	}()
	for n := 0; n < 60; n++ {
		_, _ = mgr.NewHostInstance(ctx, id)
	}
	close(stop)
	wg.Wait()
	load(y, verified)
	assert.Equal(t, 1, servedOnStart(), "after the race, a quiet verified load is served again")
}
