package main

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"centralrelay/core/cabinet"
)

// The host surface is everything a guest module can import: the host modules the relay registers. This test
// enumerates it from the PRODUCTION wiring (NewWasmManager + productionHostSetup, the same function main uses)
// and compares it with a reviewed policy table. It fails on
//   - a host function that is not in the table (a new, unclassified capability),
//   - a table entry that no longer exists (a capability that disappeared: the table must follow it),
//   - a host module registered in a way the enumeration can not see.
//
// Every entry carries a class that says how a call is authorised; a new class needs a deliberate edit here.
const (
	classGitCapability     = "git: per-module capability (staged/commit grant); no grant = denied"
	classCICFlowCapability = "cic-flow: per-module capability; no grant = denied"

	// The guest module config (core/cabinet/cicwasm.go) mounts no filesystem, passes no arguments or
	// environment and opens no sockets, so these WASI calls act on nothing the relay owns.
	classWASIProcess = "wasi: process/clock/random; no host resource"
	classWASIArgsEnv = "wasi: arguments and environment; both empty by the guest module config"
	classWASIFD      = "wasi: file descriptor on stdio only; no filesystem is mounted"
	classWASIPath    = "wasi: path operation; no filesystem is mounted, so nothing resolves"
	classWASISocket  = "wasi: socket operation; no socket is pre-opened, so nothing resolves"
)

func wasiClasses() map[string]string {
	m := map[string]string{}
	set := func(class string, names ...string) {
		for _, n := range names {
			m[n] = class
		}
	}
	set(classWASIProcess, "clock_res_get", "clock_time_get", "poll_oneoff", "proc_exit", "proc_raise", "random_get", "sched_yield")
	set(classWASIArgsEnv, "args_get", "args_sizes_get", "environ_get", "environ_sizes_get")
	set(classWASIFD, "fd_advise", "fd_allocate", "fd_close", "fd_datasync", "fd_fdstat_get", "fd_fdstat_set_flags",
		"fd_fdstat_set_rights", "fd_filestat_get", "fd_filestat_set_size", "fd_filestat_set_times", "fd_pread",
		"fd_prestat_dir_name", "fd_prestat_get", "fd_pwrite", "fd_read", "fd_readdir", "fd_renumber", "fd_seek",
		"fd_sync", "fd_tell", "fd_write")
	set(classWASIPath, "path_create_directory", "path_filestat_get", "path_filestat_set_times", "path_link", "path_open",
		"path_readlink", "path_remove_directory", "path_rename", "path_symlink", "path_unlink_file")
	set(classWASISocket, "sock_accept", "sock_recv", "sock_send", "sock_shutdown")
	return m
}

var expectedHostSurface = map[string]map[string]string{
	"git": {
		"clone":    classGitCapability,
		"fetch":    classGitCapability,
		"checkout": classGitCapability,
		"commit":   classGitCapability,
		"push":     classGitCapability, // a stub that always refuses: push is disabled
		"status":   classGitCapability,
	},
	"cic-flow": {
		"sign":    classCICFlowCapability,
		"actuate": classCICFlowCapability,
	},
	"wasi_snapshot_preview1": wasiClasses(),
}

// recordingRuntime records how host modules are registered during the host setup.
type recordingRuntime struct {
	wazero.Runtime
	hostModules []string
	otherPaths  []string
}

func (r *recordingRuntime) NewHostModuleBuilder(name string) wazero.HostModuleBuilder {
	r.hostModules = append(r.hostModules, name)
	return r.Runtime.NewHostModuleBuilder(name)
}

func (r *recordingRuntime) InstantiateWithConfig(ctx context.Context, b []byte, c wazero.ModuleConfig) (api.Module, error) {
	r.otherPaths = append(r.otherPaths, "InstantiateWithConfig")
	return r.Runtime.InstantiateWithConfig(ctx, b, c)
}

func (r *recordingRuntime) InstantiateModule(ctx context.Context, cm wazero.CompiledModule, c wazero.ModuleConfig) (api.Module, error) {
	r.otherPaths = append(r.otherPaths, "InstantiateModule")
	return r.Runtime.InstantiateModule(ctx, cm, c)
}

func (r *recordingRuntime) CompileModule(ctx context.Context, b []byte) (wazero.CompiledModule, error) {
	r.otherPaths = append(r.otherPaths, "CompileModule")
	return r.Runtime.CompileModule(ctx, b)
}

// enumerateHostSurface builds the production WasmManager with the production host setup and returns
// module -> sorted exported function names, for every module the setup registered plus WASI.
func enumerateHostSurface(t *testing.T) (map[string][]string, *recordingRuntime) {
	t.Helper()
	surface := map[string][]string{}
	rec := &recordingRuntime{}
	setup := productionHostSetup(&stubGitOps{}, newCICFlowRegistry(nil, nil))
	cfg := cabinet.WasmManagerConfig{LRUCacheSize: 4, HostSetupFn: func(ctx context.Context, r wazero.Runtime) error {
		rec.Runtime = r
		if err := setup(ctx, rec); err != nil {
			return err
		}
		names := append([]string{"wasi_snapshot_preview1"}, rec.hostModules...)
		for _, n := range names {
			m := r.Module(n)
			if m == nil {
				surface[n] = nil
				continue
			}
			fns := []string{}
			for name := range m.ExportedFunctionDefinitions() {
				fns = append(fns, name)
			}
			sort.Strings(fns)
			surface[n] = fns
		}
		return nil
	}}
	wm, err := cabinet.NewWasmManager(context.Background(), nil, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = wm.Close(context.Background()) })
	return surface, rec
}

func TestHostSurface_MatchesThePolicyTable(t *testing.T) {
	surface, rec := enumerateHostSurface(t)

	assert.Empty(t, rec.otherPaths, "a host module registered through a path the enumeration can not see: %v", rec.otherPaths)

	for mod, fns := range surface {
		want, known := expectedHostSurface[mod]
		if !assert.True(t, known, "unclassified host module %q (functions %v): add it to expectedHostSurface with a class", mod, fns) {
			continue
		}
		for _, fn := range fns {
			_, ok := want[fn]
			assert.True(t, ok, "unclassified host function %s.%s: add it to expectedHostSurface with a class", mod, fn)
		}
		have := map[string]bool{}
		for _, fn := range fns {
			have[fn] = true
		}
		for fn := range want {
			assert.True(t, have[fn], "host function %s.%s is classified but no longer exists: remove it from expectedHostSurface", mod, fn)
		}
	}
	for mod := range expectedHostSurface {
		_, ok := surface[mod]
		assert.True(t, ok, "host module %q is classified but is no longer registered", mod)
	}
}

// The comparison above must be able to fail: the table is checked against deliberately wrong surfaces.
func TestHostSurface_ComparisonDetectsAdditionAndDisappearance(t *testing.T) {
	diff := func(surface map[string][]string) []string {
		var problems []string
		for mod, fns := range surface {
			want, known := expectedHostSurface[mod]
			if !known {
				problems = append(problems, "module+"+mod)
				continue
			}
			have := map[string]bool{}
			for _, fn := range fns {
				have[fn] = true
				if _, ok := want[fn]; !ok {
					problems = append(problems, "func+"+mod+"."+fn)
				}
			}
			for fn := range want {
				if !have[fn] {
					problems = append(problems, "func-"+mod+"."+fn)
				}
			}
		}
		for mod := range expectedHostSurface {
			if _, ok := surface[mod]; !ok {
				problems = append(problems, "module-"+mod)
			}
		}
		sort.Strings(problems)
		return problems
	}
	real, _ := enumerateHostSurface(t)
	require.Empty(t, diff(real))

	clone := func() map[string][]string {
		c := map[string][]string{}
		for k, v := range real {
			c[k] = append([]string(nil), v...)
		}
		return c
	}
	added := clone()
	added["git"] = append(added["git"], "exec")
	assert.Equal(t, []string{"func+git.exec"}, diff(added))

	newMod := clone()
	newMod["net"] = []string{"dial"}
	assert.Equal(t, []string{"module+net"}, diff(newMod))

	gone := clone()
	gone["git"] = gone["git"][1:]
	assert.Len(t, diff(gone), 1)
	assert.Contains(t, diff(gone)[0], "func-git.")

	noMod := clone()
	delete(noMod, "cic-flow")
	assert.Equal(t, []string{"module-cic-flow"}, diff(noMod))

	wasiAdded := clone()
	wasiAdded["wasi_snapshot_preview1"] = append(wasiAdded["wasi_snapshot_preview1"], "sock_open")
	assert.Equal(t, []string{"func+wasi_snapshot_preview1.sock_open"}, diff(wasiAdded))
}
