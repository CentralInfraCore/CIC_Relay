package main

import (
	"context"
	"encoding/json"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	nexusgit "centralrelay/core/nexus/git"
)

// makeGitHostSetupFn returns a WasmManagerConfig.HostSetupFn that registers the
// typed git host module ("git") on the wazero runtime.
// WASM modules can import these functions to perform git operations through the
// relay's capability boundary — they never receive arbitrary exec access.
func makeGitHostSetupFn(gitOps nexusgit.GitOps) func(ctx context.Context, r wazero.Runtime) error {
	return func(ctx context.Context, r wazero.Runtime) error {
		return registerGitHostFunctions(ctx, r, gitOps)
	}
}

// registerGitHostFunctions instantiates the "git" host module on r.
// Each function follows a uniform ABI:
//
//	(reqPtr i32, reqLen i32, outPtr i32, outLen i32) -> i32
//
// The caller passes a JSON-encoded request in WASM linear memory at
// (reqPtr, reqLen). The response JSON is written to (outPtr, outLen).
// The return value is the number of bytes written, or -1 on error.
func registerGitHostFunctions(ctx context.Context, r wazero.Runtime, gitOps nexusgit.GitOps) error {
	b := r.NewHostModuleBuilder("git")

	b.NewFunctionBuilder().
		WithFunc(makeCloneFn(gitOps)).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("clone")

	b.NewFunctionBuilder().
		WithFunc(makeFetchFn(gitOps)).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("fetch")

	b.NewFunctionBuilder().
		WithFunc(makeCheckoutFn(gitOps)).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("checkout")

	b.NewFunctionBuilder().
		WithFunc(makeCommitFn(gitOps)).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("commit")

	b.NewFunctionBuilder().
		WithFunc(makePushFn(gitOps)).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("push")

	b.NewFunctionBuilder().
		WithFunc(makeStatusFn(gitOps)).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("status")

	_, err := b.Instantiate(ctx)
	return err
}

// --- host function factories ---

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

func makePushFn(ops nexusgit.GitOps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		var req struct {
			RepoID  string `json:"repo_id"`
			Remote  string `json:"remote"`
			Refspec string `json:"refspec"`
		}
		if !gitReadJSON(mod, reqPtr, reqLen, &req) {
			return gitWriteError(mod, outPtr, outLen, "bad request JSON")
		}
		if err := ops.Push(ctx, req.RepoID, req.Remote, req.Refspec); err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		return gitWriteJSON(mod, outPtr, outLen, map[string]bool{"ok": true})
	}
}

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
