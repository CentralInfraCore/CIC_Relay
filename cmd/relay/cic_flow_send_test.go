package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/hostsign"
)

const (
	sendHost = "iaas.eu-frankfurt-1.oraclecloud.com"
	sendRes  = "ocid1.vcn.oc1.eu-frankfurt-1.aaaa"
	sendBind = "sha256:abababababababababababababababababababababababababababababababab"
)

const sendModuleYAML = `
abi:
  imports:
    - module: cic-flow
      functions: [send]
capability_manifest:
  egress:
    - host: "*.oraclecloud.com"
      methods: [GET, PUT, DELETE]
  secrets:
    - handle: "oci/*"
      ops: [sign]
`

func testSendConfig() *sendConfig {
	pol := hostsign.Policy{
		Hosts: []string{sendHost},
		Operations: map[string]hostsign.OperationPolicy{
			"observe": {Methods: []string{"GET", "HEAD"}, PathTemplate: "/20160918/vcns/{resource_id}"},
			"destroy": {Methods: []string{"DELETE"}, PathTemplate: "/20160918/vcns/{resource_id}"},
		},
		Bindings:                map[string]string{sendRes: sendBind},
		ResponseHeaderAllowlist: []string{"etag"},
		Limits:                  hostsign.Limits{TimeoutMS: 2000, MaxRequestBytes: 4096, MaxResponseBytes: 4096},
	}
	cfg, err := loadSendConfigForTest(pol)
	if err != nil {
		panic(err)
	}
	return cfg
}

func loadSendConfigForTest(pol hostsign.Policy) (*sendConfig, error) {
	dir, err := os.MkdirTemp("", "sendcfg")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	raw, _ := json.Marshal(pol)
	p := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		return nil, err
	}
	return loadSendConfig(p, "ten/usr/fp")
}

func sendRegistry(t *testing.T, sg signer, d httpDoer) *cicFlowRegistry {
	t.Helper()
	reg := newCICFlowRegistry(func(string) (signer, error) { return sg, nil }, d)
	reg.configureSend(testSendConfig())
	return reg
}

func sendReq(method, op string) map[string]any {
	return map[string]any{
		"v": 1, "signing_profile": "http-signature:oci:draft-cavage@1", "key_ref": "oci/default",
		"request": map[string]any{"method": method, "host": sendHost, "path": "/20160918/vcns/" + sendRes},
		"intent":  map[string]any{"operation": op, "resource_id": sendRes, "binding_digest": sendBind},
	}
}

func callSend(t *testing.T, deps cicFlowDeps, req any) (int32, map[string]any, string) {
	t.Helper()
	mod := newMod(1 << 16)
	reqLen := putJSON(mod, 0, req)
	const outOff = 1 << 15
	n := makeSendFn(deps)(context.Background(), mod, 0, reqLen, outOff, 1<<14)
	require.Positive(t, n)
	raw := string(mod.mem.buf[outOff : outOff+uint32(n)])
	out := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	return n, out, raw
}

// Through the production registry (publish -> lookup -> host function): the guest's structured
// request is signed and sent by the host; the guest answer carries no signature or Authorization.
func TestSend_ThroughRegistryAndHostFunction(t *testing.T) {
	sg := &stubSigner{sig: "RAW-SIGNATURE-b64"}
	doer := &stubDoer{resp: okResponse(200, `{"id":"x"}`, map[string]string{"etag": "e1", "set-cookie": "a=b"})}
	reg := sendRegistry(t, sg, doer)
	require.NoError(t, reg.publishVerified("oracle", []byte(sendModuleYAML), "art"))
	deps, ok := reg.lookup("oracle")
	require.True(t, ok)

	_, out, raw := callSend(t, deps, sendReq("GET", "observe"))
	assert.Equal(t, "completed", out["outcome"], raw)
	assert.NotContains(t, raw, "RAW-SIGNATURE-b64", "AC-SS-01: the signature never reaches the guest")
	assert.NotContains(t, strings.ToLower(raw), "authorization")
	require.NotNil(t, doer.gotReq)
	assert.Contains(t, doer.gotReq.Header.Get("authorization"), `signature="RAW-SIGNATURE-b64"`)
	assert.Contains(t, doer.gotReq.Header.Get("authorization"), `keyId="ten/usr/fp"`, "the key id is the operator's")
	assert.Contains(t, string(sg.gotData), "(request-target): get /20160918/vcns/"+sendRes)
	recs := deps.send.Log().Records()
	require.Len(t, recs, 1)
	assert.Equal(t, "allowed", recs[0].Decision)

	// a refused request: no further signature call, no further send
	sg.gotData = nil
	doer.gotReq = nil
	_, out, raw = callSend(t, deps, sendReq("DELETE", "observe"))
	assert.Equal(t, "denied", out["outcome"], raw)
	assert.Contains(t, raw, "POLICY_METHOD")
	assert.Nil(t, sg.gotData)
	assert.Nil(t, doer.gotReq)
}

// A send module gets no raw primitives: even a wasm that imports sign/actuate beyond what its
// project.yaml declares is refused at call time.
func TestSend_ModuleHasNoRawSignOrActuate(t *testing.T) {
	sg := &stubSigner{sig: "S"}
	doer := &stubDoer{resp: okResponse(200, "x", nil)}
	reg := sendRegistry(t, sg, doer)
	require.NoError(t, reg.publishVerified("oracle", []byte(sendModuleYAML), "art"))
	deps, _ := reg.lookup("oracle")
	assert.Nil(t, deps.sign)
	assert.Empty(t, deps.egress)

	mod := newMod(8192)
	_, out := invoke(makeSignFn(deps), mod, map[string]string{"data_base64": "eA=="})
	assert.Contains(t, out["error"], "no signer configured")
	_, out = invoke(makeActuateFn(deps), newMod(8192), map[string]string{"method": "GET", "url": "https://" + sendHost + "/x"})
	assert.Contains(t, out["error"], "egress denied")
	assert.Nil(t, doer.gotReq)
}

func TestSend_LoadTimeRules(t *testing.T) {
	// no operator configuration -> the module is rejected at load
	reg := newCICFlowRegistry(nil, &stubDoer{})
	err := reg.publishVerified("oracle", []byte(sendModuleYAML), "a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CIC_HOSTSIGN_POLICY")
	_, ok := reg.lookup("oracle")
	assert.False(t, ok)

	reg = sendRegistry(t, &stubSigner{}, &stubDoer{})
	both := strings.Replace(sendModuleYAML, "[send]", "[send, sign]", 1)
	err = reg.publishVerified("m", []byte(both), "a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "raw primitives")
	both = strings.Replace(sendModuleYAML, "[send]", "[send, actuate]", 1)
	assert.Error(t, reg.publishVerified("m", []byte(both), "a"))

	noSecret := strings.Replace(sendModuleYAML, "ops: [sign]", "ops: [read]", 1)
	err = reg.publishVerified("m", []byte(noSecret), "a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ops:[sign]")

	noEgress := strings.Split(sendModuleYAML, "  egress:")[0] + "  secrets:\n    - handle: \"oci/*\"\n      ops: [sign]\n"
	err = reg.publishVerified("m", []byte(noEgress), "a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no egress")
}

func TestSend_KeyRefMustMatchManifestHandle(t *testing.T) {
	sg := &stubSigner{sig: "S"}
	reg := sendRegistry(t, sg, &stubDoer{resp: okResponse(200, "{}", nil)})
	require.NoError(t, reg.publishVerified("oracle", []byte(sendModuleYAML), "a"))
	deps, _ := reg.lookup("oracle")
	req := sendReq("GET", "observe")
	req["key_ref"] = "vault/other"
	_, out, raw := callSend(t, deps, req)
	assert.Equal(t, "denied", out["outcome"])
	assert.Contains(t, raw, "KEY_REF_UNAUTHORIZED")
	assert.Nil(t, sg.gotData)

	// a resolver error is fail-closed too
	reg2 := newCICFlowRegistry(func(string) (signer, error) { return nil, errors.New("no key") }, &stubDoer{})
	reg2.configureSend(testSendConfig())
	require.NoError(t, reg2.publishVerified("oracle", []byte(sendModuleYAML), "a"))
	deps2, _ := reg2.lookup("oracle")
	_, out, _ = callSend(t, deps2, sendReq("GET", "observe"))
	assert.Equal(t, "denied", out["outcome"])
}

func TestSend_NotGrantedAndBadMemory(t *testing.T) {
	mod := newMod(4096)
	n := makeSendFn(cicFlowDeps{})(context.Background(), mod, 0, 4, 1024, 512)
	require.Positive(t, n)
	assert.Contains(t, string(mod.mem.buf[1024:1024+uint32(n)]), "not granted")
}

func TestHandleMatches(t *testing.T) {
	assert.True(t, handleMatches("oci/*", "oci/default"))
	assert.True(t, handleMatches("oci/default", "oci/default"))
	assert.False(t, handleMatches("oci/*", "oci/a/b"))
	assert.False(t, handleMatches("oci/*", "vault/default"))
	assert.False(t, handleMatches("oci/*", "oci/"))
	assert.False(t, handleMatches("oci/default", "oci/other"))
}

func TestLoadSendConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	_, err := loadSendConfig(filepath.Join(dir, "missing.json"), "k")
	assert.Error(t, err)
	_, err = loadSendConfig(write("bad.json", "{"), "k")
	assert.Error(t, err)
	_, err = loadSendConfig(write("empty.json", "{}"), "k")
	assert.Error(t, err, "an empty policy is invalid")
	raw, _ := json.Marshal(hostsign.Policy{
		Hosts: []string{sendHost}, Operations: map[string]hostsign.OperationPolicy{"observe": {Methods: []string{"GET"}, PathTemplate: "/v/{resource_id}"}},
		Limits: hostsign.Limits{TimeoutMS: 1, MaxRequestBytes: 1, MaxResponseBytes: 1},
	})
	good := write("good.json", string(raw))
	_, err = loadSendConfig(good, "")
	assert.Error(t, err, "key id required")
	cfg, err := loadSendConfig(good, "t/u/f")
	require.NoError(t, err)
	assert.Equal(t, "t/u/f", cfg.keyID)

	t.Setenv("CIC_HOSTSIGN_POLICY", "")
	c, err := sendConfigFromEnv()
	assert.NoError(t, err)
	assert.Nil(t, c, "unset -> send not configured")
	t.Setenv("CIC_HOSTSIGN_POLICY", good)
	t.Setenv("CIC_OCI_SIGN_KEY_ID", "t/u/f")
	c, err = sendConfigFromEnv()
	require.NoError(t, err)
	assert.NotNil(t, c)
}
