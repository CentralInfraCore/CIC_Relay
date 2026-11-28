package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"centralrelay/core/cabinet"
)

// cic-flow is the decomposed, guest-facing, pure-Go counterpart of the FFI
// `run_flow` Bearer trust-flow (see features/feature-011-oci-provider). A
// sandboxed provider module imports two host functions and orchestrates its own
// sign-then-send actuation:
//
//	sign     — sign a caller-built canonical string with a scoped Vault key
//	actuate  — egress-policed HTTP carrying the caller's OWN Authorization header
//
// The split keeps the provider-specific canonicalization in the guest and the
// key in the host: `sign` never sees the network, `actuate` never sees the key.
// Sign-then-send opens nothing secret (the signature is the authenticator and is
// public; the key stays in Vault via Transit), so unlike `run_flow` this path
// needs no Rust airlock and is pure Go, like the `git` host module.

// signer is the subset of core/nexus/crypto.CryptoService that cic-flow.sign
// needs: sign caller-built bytes with a key that never leaves the backend (Vault
// Transit). *crypto.VaultCryptoService satisfies it.
type signer interface {
	Sign(ctx context.Context, data []byte) (string, error)
}

// httpDoer is the egress transport cic-flow.actuate uses. *http.Client satisfies
// it; tests inject a double.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// cicFlowDeps are the host-side capabilities the cic-flow module exposes. They
// are the relay's, not the guest's: the guest can only express what to sign and
// what to send — never name the key or widen the egress allowlist.
//
// Zero value is deliberately inert and safe: a nil signer makes `sign` a clean
// error, and an empty egress set denies every `actuate`. Real deps are bound
// per-module from the signed capability manifest once R3 lands.
type cicFlowDeps struct {
	sign   signer       // Vault Transit signer; nil → sign returns a typed error
	doer   httpDoer     // egress transport; nil → http.DefaultClient
	egress egressPolicy // per-module egress policy (host/method/path/port); empty → deny all
}

// makeCICFlowHostSetupFn returns a HostSetupFn that registers the "cic-flow"
// host module on the wazero runtime, backed by a per-module capability registry.
func makeCICFlowHostSetupFn(reg *cicFlowRegistry) func(ctx context.Context, r wazero.Runtime) error {
	return func(ctx context.Context, r wazero.Runtime) error {
		return registerCICFlowHostFunctions(ctx, r, reg)
	}
}

// registerCICFlowHostFunctions instantiates the "cic-flow" host module. Each
// exported function follows the same uniform ABI as the `git` host module:
//
//	(req_ptr i32, req_len i32, out_ptr i32, out_len i32) -> i32
//
// A JSON request is read from linear memory at (req_ptr, req_len); the JSON
// response is written to (out_ptr, out_len); the return value is bytes written,
// or -1 on a memory/marshal error. Each call resolves the *calling* guest's deps
// from reg by module name; an unregistered module is denied (fail-closed).
func registerCICFlowHostFunctions(ctx context.Context, r wazero.Runtime, reg *cicFlowRegistry) error {
	b := r.NewHostModuleBuilder("cic-flow")

	b.NewFunctionBuilder().
		WithFunc(perModule(reg, makeSignFn)).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("sign")

	b.NewFunctionBuilder().
		WithFunc(perModule(reg, makeActuateFn)).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").
		WithResultNames("n").
		Export("actuate")

	_, err := b.Instantiate(ctx)
	return err
}

// perModule wraps a deps-bound host-function factory so the exported host
// function resolves the *calling* instance's deps from the registry by the
// runtime-assigned InstanceIdentity (name, artifact, envelope binding; #162 V7),
// which must also name the calling guest (`mod.Name()`). A module with no
// authenticated capability for exactly that instance is denied — fail-closed.
func perModule(reg *cicFlowRegistry, make func(cicFlowDeps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		id, _ := cabinet.InstanceIdentityFrom(ctx)
		deps, ok := reg.lookupFor(id)
		if !ok || id.Name != mod.Name() {
			return gitWriteError(mod, outPtr, outLen, "cic-flow: module has no verified capability")
		}
		return make(deps)(ctx, mod, reqPtr, reqLen, outPtr, outLen)
	}
}

// --- host function factories ---

// makeSignFn signs the caller-built canonical string (base64 in `data_base64`)
// with the scoped Vault key. The guest never names the key; the returned
// signature is not secret. The plaintext canonical string is opaque to the host.
func makeSignFn(deps cicFlowDeps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		var req struct {
			DataBase64 string `json:"data_base64"`
			TraceID    string `json:"trace_id"`
		}
		if !gitReadJSON(mod, reqPtr, reqLen, &req) {
			return gitWriteError(mod, outPtr, outLen, "bad request JSON")
		}
		if deps.sign == nil {
			return gitWriteError(mod, outPtr, outLen, "cic-flow: no signer configured")
		}
		data, err := base64.StdEncoding.DecodeString(req.DataBase64)
		if err != nil {
			return gitWriteError(mod, outPtr, outLen, "data_base64 not valid base64")
		}
		sig, err := deps.sign.Sign(ctx, data)
		if err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		return gitWriteJSON(mod, outPtr, outLen, map[string]string{"signature": sig})
	}
}

// makeActuateFn performs egress-policed HTTP carrying the caller's own headers —
// the caller's Authorization is forwarded verbatim, never stripped or replaced
// (this is the core of R2: the guest's `Authorization: Signature …` is the
// authenticator). The egress allowlist is the host's, not the request's: a host
// outside it is a typed error, not a send.
func makeActuateFn(deps cicFlowDeps) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		var req struct {
			Method     string            `json:"method"`
			URL        string            `json:"url"`
			Headers    map[string]string `json:"headers"`
			BodyBase64 string            `json:"body_base64"`
			TraceID    string            `json:"trace_id"`
		}
		if !gitReadJSON(mod, reqPtr, reqLen, &req) {
			return gitWriteError(mod, outPtr, outLen, "bad request JSON")
		}
		u, err := url.Parse(req.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
			return gitWriteError(mod, outPtr, outLen, "invalid url")
		}
		method := req.Method
		if method == "" {
			method = http.MethodGet
		}
		if !deps.egress.allows(method, u.Hostname(), portOf(u), u.Path) {
			return gitWriteError(mod, outPtr, outLen, "egress denied: not permitted by the module manifest")
		}
		var body io.Reader
		if req.BodyBase64 != "" {
			raw, decErr := base64.StdEncoding.DecodeString(req.BodyBase64)
			if decErr != nil {
				return gitWriteError(mod, outPtr, outLen, "body_base64 not valid base64")
			}
			body = bytes.NewReader(raw)
		}
		httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
		if err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		for k, v := range req.Headers {
			httpReq.Header.Set(k, v) // caller's Authorization is carried verbatim
		}
		resp, err := deps.doer.Do(httpReq)
		if err != nil {
			if errors.Is(err, errEgressBlockedAddr) {
				return egressWriteError(mod, outPtr, outLen, "blocked_address", err.Error())
			}
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		defer func() { _ = resp.Body.Close() }()
		// A response over the cap is a typed error and is never held in memory: the declared
		// length is checked first, and the read stops one byte past the cap.
		if resp.ContentLength > egressMaxResponseBytes {
			return egressWriteError(mod, outPtr, outLen, "response_too_large", egressTooLargeMsg)
		}
		respBody, err := io.ReadAll(io.LimitReader(resp.Body, egressMaxResponseBytes+1))
		if err != nil {
			return gitWriteError(mod, outPtr, outLen, err.Error())
		}
		if len(respBody) > egressMaxResponseBytes {
			return egressWriteError(mod, outPtr, outLen, "response_too_large", egressTooLargeMsg)
		}
		respHeaders := make(map[string]string, len(resp.Header))
		for k := range resp.Header {
			respHeaders[strings.ToLower(k)] = resp.Header.Get(k)
		}
		return gitWriteJSON(mod, outPtr, outLen, map[string]interface{}{
			"status":      resp.StatusCode,
			"headers":     respHeaders,
			"body_base64": base64.StdEncoding.EncodeToString(respBody),
		})
	}
}

// egressTooLargeMsg is the message of the response_too_large error.
var egressTooLargeMsg = "egress: response exceeds the " + strconv.Itoa(egressMaxResponseBytes) + "-byte limit"

// egressWriteError writes a typed egress error: {"error": <msg>, "code": <code>}. The "error"
// key is the one every guest already reads; "code" is additive.
func egressWriteError(mod api.Module, outPtr, outLen uint32, code, msg string) int32 {
	return gitWriteJSON(mod, outPtr, outLen, map[string]string{"error": msg, "code": code})
}

// portOf returns the request's TCP port: the explicit URL port, else the scheme
// default (443 for https, 80 for http).
func portOf(u *url.URL) int {
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	if u.Scheme == "http" {
		return 80
	}
	return 443
}

// combineHostSetups returns a HostSetupFn that runs each setup in order, so
// several host modules (e.g. "git" and "cic-flow") register on one runtime. The
// first error stops the chain; nil setups are skipped.
func combineHostSetups(setups ...func(context.Context, wazero.Runtime) error) func(context.Context, wazero.Runtime) error {
	return func(ctx context.Context, r wazero.Runtime) error {
		for _, s := range setups {
			if s == nil {
				continue
			}
			if err := s(ctx, r); err != nil {
				return err
			}
		}
		return nil
	}
}
