package main

import (
	"context"
	"encoding/json"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"centralrelay/core/cabinet"
	nexusgit "centralrelay/core/nexus/git"
)

// makeGitHostSetupFn returns a WasmManagerConfig.HostSetupFn that registers the
// typed git host module ("git") on the wazero runtime.
// WASM modules can import these functions to perform git operations through the
// relay's capability boundary — they never receive arbitrary exec access.
func makeGitHostSetupFn(gitOps nexusgit.GitOps, authz gitAuthorizer) func(ctx context.Context, r wazero.Runtime) error {
	return func(ctx context.Context, r wazero.Runtime) error {
		return registerGitHostFunctions(ctx, r, gitOps, authz)
	}
}

// gitAuthorizer decides whether the calling module may perform op. A nil authorizer denies everything.
type gitAuthorizer interface {
	authorizeGit(instance cabinet.InstanceIdentity, op string, req gitAuthRequest) error
}

// authorized wraps a git host function so that it runs only if the *calling instance* is permitted.
// The caller's identity is the one the WasmManager attached to the call context when it created the
// instance (module ID, the artifact compiled for it, a per-instance serial). It is never read from the
// request, and a context without it is refused, so a call that does not come from a managed instance (a
// start section included: it runs under the same identity) can not borrow one. The instance name the
// runtime reports for the module must agree with the identity; a disagreement is refused as well.
// gitHostFn is the uniform host-function ABI of the git module.
type gitHostFn = func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32

func authorized(authz gitAuthorizer, op string, inner gitHostFn) gitHostFn {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		var req gitAuthRequest
		if !gitReadJSON(mod, reqPtr, reqLen, &req) {
			return gitWriteError(mod, outPtr, outLen, "bad request JSON")
		}
		if authz == nil {
			return gitWriteError(mod, outPtr, outLen, "git: module has no verified git capability")
		}
		id, ok := cabinet.InstanceIdentityFrom(ctx)
		if !ok || id.Name != mod.Name() {
			return gitWriteError(mod, outPtr, outLen, "git: the caller is not an identified module instance")
		}
		if err := authz.authorizeGit(id, op, req); err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		return inner(ctx, mod, reqPtr, reqLen, outPtr, outLen)
	}
}

// registerGitHostFunctions instantiates the "git" host module on the wazero runtime.
// Each exported function follows a uniform ABI:
//
//	(reqPtr i32, reqLen i32, outPtr i32, outLen i32) -> i32
//
// The caller passes a JSON-encoded request in WASM linear memory at (reqPtr, reqLen).
// The response JSON is written to (outPtr, outLen), and the return value is the number
// of bytes written, or -1 on error.
func registerGitHostFunctions(ctx context.Context, r wazero.Runtime, gitOps nexusgit.GitOps, authz gitAuthorizer) error {
	b := r.NewHostModuleBuilder("git")

	b.NewFunctionBuilder().
		WithFunc(authorized(authz, "clone", makeCloneFn(gitOps))).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("clone")

	b.NewFunctionBuilder().
		WithFunc(authorized(authz, "fetch", makeFetchFn(gitOps))).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("fetch")

	b.NewFunctionBuilder().
		WithFunc(authorized(authz, "checkout", makeCheckoutFn(gitOps))).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("checkout")

	b.NewFunctionBuilder().
		WithFunc(authorized(authz, "commit", makeCommitFn(gitOps))).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("commit")

	b.NewFunctionBuilder().
		WithFunc(pushDisabledFn).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("push")

	b.NewFunctionBuilder().
		WithFunc(authorized(authz, "status", makeStatusFn(gitOps))).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("status")

	_, err := b.Instantiate(ctx)
	return err
}

// --- host function factories ---

// makeCloneFn creates a WASM host function that clones a git repository.
func makeCloneFn(ops nexusgit.GitOps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		var req struct {
			RepoID string `json:"repo_id"`
			URL    string `json:"url"`
			Branch string `json:"branch"`
		}
		if !gitReadJSON(mod, reqPtr, reqLen, &req) {
			return gitWriteError(mod, outPtr, outLen, "bad request JSON")
		}
		if err := ops.Clone(ctx, req.RepoID, req.URL, req.Branch); err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		return gitWriteJSON(mod, outPtr, outLen, map[string]bool{"ok": true})
	}
}

// makeFetchFn creates a WASM host function that fetches updates from a git remote.
func makeFetchFn(ops nexusgit.GitOps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		var req struct {
			RepoID string `json:"repo_id"`
			Remote string `json:"remote"`
		}
		if !gitReadJSON(mod, reqPtr, reqLen, &req) {
			return gitWriteError(mod, outPtr, outLen, "bad request JSON")
		}
		if err := ops.Fetch(ctx, req.RepoID, req.Remote); err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		return gitWriteJSON(mod, outPtr, outLen, map[string]bool{"ok": true})
	}
}

// makeCheckoutFn creates a WASM host function that checks out a git reference.
func makeCheckoutFn(ops nexusgit.GitOps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		var req struct {
			RepoID string `json:"repo_id"`
			Ref    string `json:"ref"`
		}
		if !gitReadJSON(mod, reqPtr, reqLen, &req) {
			return gitWriteError(mod, outPtr, outLen, "bad request JSON")
		}
		if err := ops.Checkout(ctx, req.RepoID, req.Ref); err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		return gitWriteJSON(mod, outPtr, outLen, map[string]bool{"ok": true})
	}
}

// makeCommitFn creates a WASM host function that commits changes to a git repository.
func makeCommitFn(ops nexusgit.GitOps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		var req struct {
			RepoID  string   `json:"repo_id"`
			Message string   `json:"message"`
			Paths   []string `json:"paths"`
		}
		if !gitReadJSON(mod, reqPtr, reqLen, &req) {
			return gitWriteError(mod, outPtr, outLen, "bad request JSON")
		}
		hash, err := ops.Commit(ctx, req.RepoID, req.Message, req.Paths)
		if err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		return gitWriteJSON(mod, outPtr, outLen, map[string]string{"hash": hash})
	}
}

// pushDisabledFn is the git.push host function: it exists so that a module that imports it still links, and
// it refuses every call. It does not read the request, does not consult the authorizer and never reaches
// GitOps.Push (gitPushDisabled says why).
func pushDisabledFn(_ context.Context, mod api.Module, _, _, outPtr, outLen uint32) int32 {
	return gitWriteError(mod, outPtr, outLen, "git: "+gitPushDisabled)
}

// makeStatusFn creates a WASM host function that queries the status of a git repository.
func makeStatusFn(ops nexusgit.GitOps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		var req struct {
			RepoID string `json:"repo_id"`
		}
		if !gitReadJSON(mod, reqPtr, reqLen, &req) {
			return gitWriteError(mod, outPtr, outLen, "bad request JSON")
		}
		entries, err := ops.Status(ctx, req.RepoID)
		if err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		return gitWriteJSON(mod, outPtr, outLen, map[string]interface{}{"entries": entries})
	}
}

// --- ABI helpers ---

// gitReadJSON reads reqLen bytes from WASM memory at reqPtr and unmarshals into v.
func gitReadJSON(mod api.Module, reqPtr, reqLen uint32, v interface{}) bool {
	data, ok := mod.Memory().Read(reqPtr, reqLen)
	if !ok {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

// gitWriteJSON marshals v as JSON and writes it into WASM memory at (outPtr, outLen).
// Returns bytes written, or -1 if the buffer is too small.
func gitWriteJSON(mod api.Module, outPtr, outLen uint32, v interface{}) int32 {
	data, err := json.Marshal(v)
	if err != nil {
		return -1
	}
	if uint32(len(data)) > outLen {
		return -1
	}
	if !mod.Memory().Write(outPtr, data) {
		return -1
	}
	return int32(len(data))
}

// gitWriteError writes a JSON error response into WASM memory.
func gitWriteError(mod api.Module, outPtr, outLen uint32, msg string) int32 {
	return gitWriteJSON(mod, outPtr, outLen, map[string]string{"error": msg})
}
