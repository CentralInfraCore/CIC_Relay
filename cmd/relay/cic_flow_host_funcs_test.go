package main

import (
	"bytes"
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

// perModule resolves the calling guest's deps by mod.Name() from the registry;
// an unregistered module is denied (fail-closed), a registered one gets its deps.
func TestPerModule_Resolution(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{resp: okResponse(200, "ok", nil)})
	reg.deps["mod-a@1.0"] = cicFlowDeps{doer: reg.doer, egress: egressPolicy{{Host: "api.example.com"}}}

	fn := perModule(reg, makeActuateFn)

	// registered module → egress policy applies (allowed host permitted)
	mod := newMod(8192)
	mod.name = "mod-a@1.0"
	_, out := invoke(fn, mod, map[string]interface{}{"method": "GET", "url": "https://api.example.com/x"})
	assert.Equal(t, float64(200), out["status"])

	// registered module, host outside its policy → denied
	mod2 := newMod(8192)
	mod2.name = "mod-a@1.0"
	_, out = invoke(fn, mod2, map[string]interface{}{"method": "GET", "url": "https://evil.example/x"})
	assert.Contains(t, out["error"], "egress denied")

	// unregistered module → no verified capability
	mod3 := newMod(8192)
	mod3.name = "unknown@1.0"
	_, out = invoke(fn, mod3, map[string]interface{}{"method": "GET", "url": "https://api.example.com/x"})
	assert.Contains(t, out["error"], "no verified capability")
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
