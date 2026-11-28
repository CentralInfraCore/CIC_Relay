package main

import (
	"bytes"
	"centralrelay/core/cabinet"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// --- stubs ---

type stubSigner struct {
	sig     string
	err     error
	gotData []byte
}

func (s *stubSigner) Sign(_ context.Context, data []byte) (string, error) {
	s.gotData = data
	return s.sig, s.err
}

type stubDoer struct {
	resp   *http.Response
	err    error
	gotReq *http.Request
}

func (d *stubDoer) Do(req *http.Request) (*http.Response, error) {
	d.gotReq = req
	return d.resp, d.err
}

func okResponse(status int, body string, headers map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
}

// hostFn is the uniform (req_ptr,req_len,out_ptr,out_len)->i32 host-function type.
type hostFn = func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32

// invoke runs a host fn with req at offset 0 and the output buffer at offset
// 4096, returning n and the decoded response JSON.
func invoke(fn hostFn, mod *minMod, req interface{}) (int32, map[string]interface{}) {
	const outOff = 4096
	reqLen := putJSON(mod, 0, req)
	n := fn(context.Background(), mod, 0, reqLen, outOff, 2048)
	out := map[string]interface{}{}
	if n > 0 {
		_ = json.Unmarshal(mod.mem.buf[outOff:outOff+uint32(n)], &out)
	}
	return n, out
}

// --- sign ---

func TestMakeSignFn_Success(t *testing.T) {
	sg := &stubSigner{sig: "vault:v1:abc"}
	mod := newMod(8192)
	n, out := invoke(makeSignFn(cicFlowDeps{sign: sg}), mod, map[string]string{
		"data_base64": base64.StdEncoding.EncodeToString([]byte("canonical-string")),
		"trace_id":    "t1",
	})
	require.Positive(t, n)
	assert.Equal(t, "vault:v1:abc", out["signature"])
	assert.Equal(t, []byte("canonical-string"), sg.gotData)
}

func TestMakeSignFn_NoSigner(t *testing.T) {
	mod := newMod(8192)
	_, out := invoke(makeSignFn(cicFlowDeps{}), mod, map[string]string{
		"data_base64": base64.StdEncoding.EncodeToString([]byte("x")),
	})
	assert.Contains(t, out["error"], "no signer configured")
}

func TestMakeSignFn_BadJSON(t *testing.T) {
	mod := newMod(8192)
	mod.mem.Write(0, []byte("not-json"))
	n := makeSignFn(cicFlowDeps{sign: &stubSigner{}})(context.Background(), mod, 0, 8, 4096, 2048)
	require.Positive(t, n)
	out := map[string]interface{}{}
	_ = json.Unmarshal(mod.mem.buf[4096:4096+uint32(n)], &out)
	assert.Contains(t, out["error"], "bad request JSON")
}

func TestMakeSignFn_BadBase64(t *testing.T) {
	mod := newMod(8192)
	_, out := invoke(makeSignFn(cicFlowDeps{sign: &stubSigner{}}), mod, map[string]string{"data_base64": "!!!not-base64!!!"})
	assert.Contains(t, out["error"], "not valid base64")
}

func TestMakeSignFn_SignerError(t *testing.T) {
	mod := newMod(8192)
	_, out := invoke(makeSignFn(cicFlowDeps{sign: &stubSigner{err: errors.New("vault down")}}), mod, map[string]string{
		"data_base64": base64.StdEncoding.EncodeToString([]byte("x")),
	})
	assert.Contains(t, out["error"], "vault down")
}

// --- actuate ---

func TestMakeActuateFn_Success_CarriesAuthorization(t *testing.T) {
	doer := &stubDoer{resp: okResponse(200, "pong", map[string]string{"etag": "e1", "opc-request-id": "r1"})}
	deps := cicFlowDeps{doer: doer, egress: egressPolicy{{Host: "api.example.com"}}}
	mod := newMod(16384)

	const outOff = 8192
	reqLen := putJSON(mod, 0, map[string]interface{}{
		"method": "PUT",
		"url":    "https://api.example.com/20160918/vcns/x",
		"headers": map[string]string{
			"authorization": `Signature keyId="...",algorithm="rsa-sha256",signature="..."`,
			"date":          "Thu, 18 Jul 2026 12:00:00 GMT",
		},
		"body_base64": base64.StdEncoding.EncodeToString([]byte(`{"cidrBlock":"10.0.0.0/16"}`)),
		"trace_id":    "t1",
	})
	n := makeActuateFn(deps)(context.Background(), mod, 0, reqLen, outOff, 4096)
	require.Positive(t, n)
	out := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(mod.mem.buf[outOff:outOff+uint32(n)], &out))

	assert.Equal(t, float64(200), out["status"])
	body, _ := base64.StdEncoding.DecodeString(out["body_base64"].(string))
	assert.Equal(t, "pong", string(body))
	hdrs := out["headers"].(map[string]interface{})
	assert.Equal(t, "e1", hdrs["etag"])

	// The caller's Authorization is forwarded verbatim, not stripped/replaced.
	require.NotNil(t, doer.gotReq)
	assert.Equal(t, "PUT", doer.gotReq.Method)
	assert.Equal(t, `Signature keyId="...",algorithm="rsa-sha256",signature="..."`, doer.gotReq.Header.Get("Authorization"))
	sent, _ := io.ReadAll(doer.gotReq.Body)
	assert.Equal(t, `{"cidrBlock":"10.0.0.0/16"}`, string(sent))
}

func TestMakeActuateFn_EgressDenied(t *testing.T) {
	doer := &stubDoer{resp: okResponse(200, "", nil)}
	mod := newMod(8192)
	_, out := invoke(makeActuateFn(cicFlowDeps{doer: doer, egress: egressPolicy{{Host: "allowed.example"}}}), mod, map[string]interface{}{
		"method": "GET", "url": "https://evil.example/x",
	})
	assert.Contains(t, out["error"], "egress denied")
	assert.Nil(t, doer.gotReq) // never sent
}

func TestMakeActuateFn_EmptyEgressDeniesAll(t *testing.T) {
	doer := &stubDoer{resp: okResponse(200, "", nil)}
	mod := newMod(8192)
	_, out := invoke(makeActuateFn(cicFlowDeps{doer: doer}), mod, map[string]interface{}{
		"method": "GET", "url": "https://api.example.com/x",
	})
	assert.Contains(t, out["error"], "egress denied")
}

func TestMakeActuateFn_BadJSON(t *testing.T) {
	mod := newMod(8192)
	mod.mem.Write(0, []byte("nope"))
	n := makeActuateFn(cicFlowDeps{doer: &stubDoer{}})(context.Background(), mod, 0, 4, 4096, 2048)
	out := map[string]interface{}{}
	_ = json.Unmarshal(mod.mem.buf[4096:4096+uint32(n)], &out)
	assert.Contains(t, out["error"], "bad request JSON")
}

func TestMakeActuateFn_InvalidURL(t *testing.T) {
	mod := newMod(8192)
	_, out := invoke(makeActuateFn(cicFlowDeps{doer: &stubDoer{}, egress: egressPolicy{{Host: "x"}}}), mod, map[string]interface{}{"url": "ftp://nope"})
	assert.Contains(t, out["error"], "invalid url")
}

func TestMakeActuateFn_BadBodyBase64(t *testing.T) {
	mod := newMod(8192)
	_, out := invoke(makeActuateFn(cicFlowDeps{doer: &stubDoer{}, egress: egressPolicy{{Host: "api.example.com"}}}), mod, map[string]interface{}{
		"method": "POST", "url": "https://api.example.com/x", "body_base64": "!!bad!!",
	})
	assert.Contains(t, out["error"], "not valid base64")
}

func TestMakeActuateFn_DoerError(t *testing.T) {
	mod := newMod(8192)
	_, out := invoke(makeActuateFn(cicFlowDeps{doer: &stubDoer{err: errors.New("dial fail")}, egress: egressPolicy{{Host: "api.example.com"}}}), mod, map[string]interface{}{
		"method": "GET", "url": "https://api.example.com/x",
	})
	assert.Contains(t, out["error"], "dial fail")
}

// --- registration / setup / combine ---

func TestRegisterCICFlowHostFunctions(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer func() { _ = r.Close(ctx) }()
	require.NoError(t, registerCICFlowHostFunctions(ctx, r, newCICFlowRegistry(nil, nil)))
}

func TestMakeCICFlowHostSetupFn(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer func() { _ = r.Close(ctx) }()
	require.NoError(t, makeCICFlowHostSetupFn(newCICFlowRegistry(nil, nil))(ctx, r))
}

// invokeAsInstance is invoke with the runtime-assigned instance identity on the context (nil: none).
func invokeAsInstance(fn hostFn, mod *minMod, id *cabinet.InstanceIdentity, req interface{}) map[string]interface{} {
	const outOff = 4096
	ctx := context.Background()
	if id != nil {
		ctx = cabinet.WithInstanceIdentity(ctx, *id)
	}
	reqLen := putJSON(mod, 0, req)
	n := fn(ctx, mod, 0, reqLen, outOff, 2048)
	out := map[string]interface{}{}
	if n > 0 {
		_ = json.Unmarshal(mod.mem.buf[outOff:outOff+uint32(n)], &out)
	}
	return out
}

// perModule resolves the calling instance's deps from the registry by its runtime identity: the grant must be
// active and bound to the instance's artifact and authenticated envelope digest (#162 V7). An unregistered module, a
// staged grant, another artifact or another envelope identity under the same module ID is denied (fail-closed).
func TestPerModule_Resolution(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{resp: okResponse(200, "ok", nil)})
	deps := cicFlowDeps{doer: reg.doer, egress: egressPolicy{{Host: "api.example.com"}}}
	reg.deps["mod-a@1.0"] = flowGrant{deps: deps, artifact: "art-1", envelopeDigest: "sha256:env-1", active: true}

	fn := perModule(reg, makeActuateFn)
	good := &cabinet.InstanceIdentity{Name: "mod-a@1.0", Artifact: "art-1", Serial: 1, Binding: "sha256:env-1"}
	allowed := map[string]interface{}{"method": "GET", "url": "https://api.example.com/x"}

	mod := func(name string) *minMod { m := newMod(8192); m.name = name; return m }

	// the authenticated instance → egress policy applies (allowed host permitted)
	assert.Equal(t, float64(200), invokeAsInstance(fn, mod("mod-a@1.0"), good, allowed)["status"])
	// same instance, host outside its policy → denied
	assert.Contains(t, invokeAsInstance(fn, mod("mod-a@1.0"), good, map[string]interface{}{"method": "GET", "url": "https://evil.example/x"})["error"], "egress denied")

	denied := map[string]*cabinet.InstanceIdentity{
		"no identity":                  nil,
		"unregistered module":          {Name: "unknown@1.0", Artifact: "art-1", Serial: 2, Binding: "sha256:env-1"},
		"same ID, other artifact":      {Name: "mod-a@1.0", Artifact: "art-2", Serial: 3, Binding: "sha256:env-1"},
		"same ID, other envelope":      {Name: "mod-a@1.0", Artifact: "art-1", Serial: 4, Binding: "sha256:env-2"},
		"same ID, no envelope binding": {Name: "mod-a@1.0", Artifact: "art-1", Serial: 5, Binding: ""},
	}
	for name, id := range denied {
		guest := "mod-a@1.0"
		if id != nil {
			guest = id.Name
		}
		assert.Contains(t, invokeAsInstance(fn, mod(guest), id, allowed)["error"], "no verified capability", name)
	}
	// the identity must name the calling guest
	assert.Contains(t, invokeAsInstance(fn, mod("other@1.0"), good, allowed)["error"], "no verified capability")

	// a staged (not yet committed) grant authorises nothing
	g := reg.deps["mod-a@1.0"]
	g.active = false
	reg.deps["mod-a@1.0"] = g
	assert.Contains(t, invokeAsInstance(fn, mod("mod-a@1.0"), good, allowed)["error"], "no verified capability")
}

func TestCombineHostSetups(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer func() { _ = r.Close(ctx) }()

	var order []string
	a := func(context.Context, wazero.Runtime) error { order = append(order, "a"); return nil }
	b := func(context.Context, wazero.Runtime) error { order = append(order, "b"); return nil }
	require.NoError(t, combineHostSetups(a, nil, b)(ctx, r))
	assert.Equal(t, []string{"a", "b"}, order)
}

func TestCombineHostSetups_StopsOnError(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer func() { _ = r.Close(ctx) }()

	var ran []string
	a := func(context.Context, wazero.Runtime) error { ran = append(ran, "a"); return errors.New("boom") }
	b := func(context.Context, wazero.Runtime) error { ran = append(ran, "b"); return nil }
	require.Error(t, combineHostSetups(a, b)(ctx, r))
	assert.Equal(t, []string{"a"}, ran) // b never ran
}
