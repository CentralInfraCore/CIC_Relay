package main

// Integration regression test of the hardening series (#159, #161, #163, #164, #165, #166, #157).
//
// The unit tests of each change pin that change alone. This file exercises them TOGETHER, through
// the real entry points (bootstrapTrust, loadEmbeddedComponents / loadInstalledComponents,
// cabinet.Setx behind the /set and /v1/schema/compile routes, a real git audit recorder, the
// production default egress client), so a later change that weakens one protection, or breaks
// the way they combine, fails here. Every subtest has a negative control: an input that the
// protection must refuse. It uses stand-ins only where the real thing needs the outside world
// (Vault's Sign, the signing module's key use).

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"centralrelay/core/cabinet"
	"centralrelay/core/nexus/recorder"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- #176 + #179: the prod start-up declarations and the CRL rules ---------------------------

func TestHardeningIntegration_ProdStartupDeclarationsAndCRL(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-2 * time.Hour)

	type tc struct {
		name    string
		mode    string
		policy  func(p *crlTestPKI) string
		crl     func(t *testing.T, p *crlTestPKI) []byte // nil: no CRL in the bundle
		wantErr string                                   // empty: the relay starts
	}
	fp := func(p *crlTestPKI) string { return "sha256:" + certFingerprint(p.rootCert) }
	valid := func(t *testing.T, p *crlTestPKI) []byte { return p.crlPEM(t, p.rootCert, p.rootKey, future) }
	cases := []tc{
		{"declared, valid and fresh CRL: starts", "prod", fp, valid, ""},
		{"CIC_TRUST_MODE unset: refuses", "", fp, valid, "CIC_TRUST_MODE"},
		{"CRL issuer policy undeclared in prod: refuses", "prod", func(*crlTestPKI) string { return "" }, valid, "CIC_CRL_REQUIRED_ISSUERS"},
		{"required issuer without a CRL: refuses", "prod", fp, nil, "requires a CRL"},
		{"expired CRL: refuses", "prod", fp,
			func(t *testing.T, p *crlTestPKI) []byte { return p.crlPEM(t, p.rootCert, p.rootKey, past) }, "stale"},
		{"CRL signed by another key under the same name: refuses", "prod", fp,
			func(t *testing.T, p *crlTestPKI) []byte { return p.forgedCRLPEM(t) }, "signature does not verify"},
		{"declared fingerprint matches no CA of the bundle: refuses", "prod",
			func(*crlTestPKI) string { return "sha256:" + strings.Repeat("ab", 32) }, valid, "no CA certificate of the bundle"},
		{"issuer policy none, explicitly: starts", "prod", func(*crlTestPKI) string { return "none" }, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newCRLTestPKI(t)
			parts := [][]byte{certPEM(p.rootCert)}
			if c.crl != nil {
				parts = append(parts, c.crl(t, p))
			}
			writeBundle(t, parts...)
			t.Setenv("CIC_TRUST_MODE", c.mode)
			t.Setenv("CIC_CRL_REQUIRED_ISSUERS", c.policy(p))
			mode, assets, err := bootstrapTrust(embeddedFS, newDiscardLogger())
			if c.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, TrustModeProd, mode)
				assert.NotNil(t, assets.CRLFreshness, "prod enforces CRL freshness at verification time")
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
			assert.Nil(t, assets, "no trust context is handed out on a refusal")
		})
	}
}

// --- #176 + #179 + #182: the load paths, the CRL and the shared trust context ----------------

func TestHardeningIntegration_ProdComponentsOnBothLoadPaths(t *testing.T) {
	p := newCRLTestPKI(t)                                         // root + leaf (serial 4242); crlPEM revokes that leaf
	_, unrelatedLeaf := trustCtxCA(t, "Integration Unrelated CA") // a CA that is not in the operator bundle
	good := issueIntegrationLeaf(t, p, 4343)
	writeBundle(t, certPEM(p.rootCert), p.crlPEM(t, p.rootCert, p.rootKey, time.Now().Add(24*time.Hour)))
	t.Setenv("CIC_TRUST_MODE", "prod")
	t.Setenv("CIC_CRL_REQUIRED_ISSUERS", "sha256:"+certFingerprint(p.rootCert))

	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	outside, err := fs.ReadFile(getSubFS(t, "pki/signers/sources/module"), "ee.pem")
	require.NoError(t, err)

	fresh := func() *PKIAssets { // each load gets the trust context main would build
		_, a, err := bootstrapTrust(embeddedFS, newDiscardLogger())
		require.NoError(t, err)
		a.SourceRoots, a.SourceLoaded = a.Roots, true // the cic-flow provenance anchor
		return a
	}
	cases := []struct {
		name       string
		cert       []byte
		wantLoaded bool
	}{
		{"signed by the operator CA, not revoked", good, true},
		{"signed by the operator CA, revoked in its CRL", certPEM(p.leaf), false},
		{"signed by a CA outside the operator bundle", outside, false},
		{"signed by an unrelated CA that is not in the bundle", unrelatedLeaf, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files, _ := buildModuleFS(t, wasm, c.cert)
			emb := loadVia(t, "embedded", files, fresh())
			ins := loadVia(t, "installed", files, fresh())
			assert.Equal(t, ins, emb, "the embedded and the installed path must reach the same verdict")
			assert.Equal(t, c.wantLoaded, emb.registered)
			if c.wantLoaded {
				assert.Equal(t, []string{sharedTrustModuleID}, emb.bound, "a verified module gets its capability on both paths")
			} else {
				assert.Empty(t, emb.bound, "a rejected module gets no capability")
			}
		})
	}
}

// --- #173 + #177 + #181: one real cabinet service, a real git recorder, the token, the routes --

type hiCrypto struct {
	failSign atomic.Bool
	delay    time.Duration
}

func (c *hiCrypto) Sign(_ context.Context, d []byte) (string, error) {
	if c.failSign.Load() {
		return "", errors.New("vault unavailable at secret/integration")
	}
	time.Sleep(c.delay) // widens the window between reading HEAD and committing
	return "sig:" + hex.EncodeToString(d), nil
}
func (c *hiCrypto) Verify(_ context.Context, d []byte, s string) (bool, error) {
	return s == "sig:"+hex.EncodeToString(d), nil
}
func (c *hiCrypto) Decrypt(_ context.Context, b []byte) ([]byte, error) { return b, nil }
func (c *hiCrypto) HealthCheck(context.Context) error                   { return nil }

type hiEnv struct {
	repo     string
	gitRec   *recorder.GitStateRecorder
	crypto   *hiCrypto
	mux      http.Handler
	ids      map[string]string
	signRuns atomic.Int32
}

const hiConcurrent = 8

// newHiEnv wires what main wires: the cabinet service with a real git audit recorder, required
// evidence, the production protected-module list, and the relay server with the API token behind
// the production route table. Only the signing module's body and Vault's Sign are stand-ins.
func newHiEnv(t *testing.T) *hiEnv {
	t.Helper()
	ctx := context.Background()
	e := &hiEnv{repo: t.TempDir(), crypto: &hiCrypto{delay: 15 * time.Millisecond}, ids: map[string]string{}}

	var err error
	e.gitRec, err = recorder.NewGitStateRecorder(e.repo, e.crypto)
	require.NoError(t, err)
	svc, err := cabinet.NewCabinetService(ctx, cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(),
		cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1},
		cabinet.WithRecorder(recorder.NewWorkflowRecorder(e.gitRec)),
		cabinet.WithEvidenceRequired(true),
		cabinet.WithProtectedModules(protectedModuleIDs...))
	require.NoError(t, err)

	register := func(name, fn string, f func(context.Context, interface{}) (interface{}, error)) {
		mod, err := cabinet.NewModuleDescriptor(ctx, cabinet.BaseBlock{Name: name, Version: "1.0"}, "native", nil, nil, nil)
		require.NoError(t, err)
		mod.NativeImpl = cabinet.NativeFunctions{fn: f}
		require.NoError(t, svc.PutModule(mod))
	}
	register("echo", "echo", cabinet.CoreEcho)
	register("cic.artifact.sign", "sign", func(_ context.Context, in interface{}) (interface{}, error) {
		e.signRuns.Add(1)
		return in, nil
	})
	addWF := func(short, name string, steps ...string) {
		wf, err := cabinet.NewWorkflow(ctx, cabinet.WorkflowIdentity{Version: "1.0"}, cabinet.WorkflowConfig{Name: name, Steps: steps}, cabinet.WorkflowSigning{})
		require.NoError(t, err)
		require.NoError(t, svc.PutWorkflow(wf))
		e.ids[short] = wf.ID()
	}
	addWF("public", "wf.public", "echo@1.0.echo(input) -> e")
	addWF("sign", "wf.sign", "echo@1.0.echo(input) -> e", "cic.artifact.sign@1.0.sign(e) -> signed")
	addWF("compile", "cic.schema.compile", "echo@1.0.echo(input) -> a", "cic.artifact.sign@1.0.sign(a) -> signed_artifact")
	for i := 0; i < hiConcurrent; i++ {
		addWF(fmt.Sprintf("conc%d", i), fmt.Sprintf("wf.conc%d", i), "echo@1.0.echo(input) -> e")
	}

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	e.mux = newMux(&relayServer{api: svc, config: &config{MaxJSONInputSize: 1 << 20}, logger: logger, apiToken: testAPIToken})
	return e
}

func (e *hiEnv) post(path, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func (e *hiEnv) git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = e.repo
	out, err := cmd.Output()
	require.NoError(t, err, "git %s", strings.Join(args, " "))
	return strings.TrimSpace(string(out))
}

func (e *hiEnv) commits(t *testing.T) int {
	t.Helper()
	var n int
	_, err := fmt.Sscan(e.git(t, "rev-list", "--count", "HEAD"), &n)
	require.NoError(t, err)
	return n
}

func hiSetBody(workflowID string) string {
	b, _ := json.Marshal(map[string]any{"workflow_id": workflowID, "payload": map[string]any{"msg": "hi"}})
	return string(b)
}

type hiProof struct {
	Proof struct {
		Persisted bool   `json:"persisted"`
		Commit    string `json:"commit"`
		ErrorCode string `json:"error_code"`
	} `json:"proof"`
	Code               string `json:"code"`
	ExecutionCompleted bool   `json:"execution_completed"`
	ChainHash          string `json:"chain_hash"`
}

func decodeHi(t *testing.T, w *httptest.ResponseRecorder) hiProof {
	t.Helper()
	var b hiProof
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &b), w.Body.String())
	return b
}

func TestHardeningIntegration_AuthorizationProofAndConcurrency(t *testing.T) {
	t.Run("authorization: a protected operation needs the token on every path", func(t *testing.T) {
		e := newHiEnv(t)
		before := e.commits(t)

		for name, auth := range map[string]string{"anonymous": "", "wrong token": "Bearer " + testAPIToken + "x"} {
			w := e.post("/set", auth, hiSetBody(e.ids["sign"]))
			assert.Equal(t, http.StatusUnauthorized, w.Code, "/set, %s: %s", name, w.Body.String())
		}
		w := e.post("/v1/schema/compile", "", `{"schema":"postgresql","version":"v0.18.dev"}`)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "compile route, anonymous")
		assert.EqualValues(t, 0, e.signRuns.Load(), "the signing operation never ran for an anonymous caller")
		assert.Equal(t, before, e.commits(t), "and no evidence was recorded for a refused call")

		w = e.post("/set", "Bearer "+testAPIToken, hiSetBody(e.ids["sign"]))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.EqualValues(t, 1, e.signRuns.Load(), "with the token it runs")
	})

	t.Run("compile route: token, then input validation", func(t *testing.T) {
		e := newHiEnv(t)
		w := e.post("/v1/schema/compile", "Bearer "+testAPIToken, "{\"schema\":\"pg\\nkind: Evil\",\"version\":\"v1.0.0\"}")
		assert.Equal(t, http.StatusBadRequest, w.Code, "a control character in schema is refused before anything runs")
		assert.EqualValues(t, 0, e.signRuns.Load())

		w = e.post("/v1/schema/compile", "Bearer "+testAPIToken, `{"schema":"postgresql","version":"v0.18.dev"}`)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})

	t.Run("proof persistence: the commit returned is the audit repository's HEAD, and the chain verifies", func(t *testing.T) {
		e := newHiEnv(t)
		w := e.post("/set", "Bearer "+testAPIToken, hiSetBody(e.ids["sign"]))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		b := decodeHi(t, w)
		assert.True(t, b.Proof.Persisted)
		assert.Equal(t, e.git(t, "rev-parse", "HEAD"), b.Proof.Commit)
		assert.NoError(t, e.gitRec.VerifyChain(context.Background()))
	})

	t.Run("evidence required: an outage is a 503 that says the workflow already ran, and leaks no detail", func(t *testing.T) {
		e := newHiEnv(t)
		e.crypto.failSign.Store(true)
		w := e.post("/set", "Bearer "+testAPIToken, hiSetBody(e.ids["sign"]))
		require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
		b := decodeHi(t, w)
		assert.Equal(t, "evidence_required", b.Code)
		assert.True(t, b.ExecutionCompleted)
		assert.False(t, b.Proof.Persisted)
		assert.NotEmpty(t, b.ChainHash)
		assert.EqualValues(t, 1, e.signRuns.Load(), "the workflow did run: the 503 is about the missing proof, not a refusal")
		assert.NotContains(t, w.Body.String(), "secret/integration", "the recorder's error text does not reach the caller")
	})

	t.Run("an unprotected workflow still runs anonymously, and is recorded", func(t *testing.T) {
		e := newHiEnv(t)
		w := e.post("/set", "", hiSetBody(e.ids["public"]))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.True(t, decodeHi(t, w).Proof.Persisted)
	})

	t.Run("concurrent workflows: every record lands once and the signed chain verifies", func(t *testing.T) {
		e := newHiEnv(t)
		var wg sync.WaitGroup
		codes := make([]int, hiConcurrent)
		for i := 0; i < hiConcurrent; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				codes[i] = e.post("/set", "", hiSetBody(e.ids[fmt.Sprintf("conc%d", i)])).Code
			}(i)
		}
		wg.Wait()
		for i, c := range codes {
			assert.Equal(t, http.StatusOK, c, "request %d", i)
		}
		assert.Equal(t, hiConcurrent+1, e.commits(t), "the root commit plus exactly one record per workflow")
		seen := map[string]int{}
		for _, subject := range strings.Split(e.git(t, "log", "--format=%s"), "\n") {
			seen[subject]++
		}
		for i := 0; i < hiConcurrent; i++ {
			id := e.ids[fmt.Sprintf("conc%d", i)]
			assert.Equal(t, 1, seen["[AUTO] run-id: "+id], "run-id %s must be recorded exactly once", id)
		}
		assert.NoError(t, e.gitRec.VerifyChain(context.Background()), "no false 'chain broken', no lost record")
	})
}

// --- #163: the production default egress client -----------------------------------------------

func TestHardeningIntegration_Egress(t *testing.T) {
	// The redirect target is a second LOCAL server with its own hit counter, which must stay at zero.
	// Nothing outside the test process is ever contacted, even if a regression starts following
	// redirects: the forbidden second request would land here and be counted.
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("must not be reached"))
	}))
	defer target.Close()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, target.URL+"/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	t.Run("the default doer (what main wires) does not dial an allow-listed private destination", func(t *testing.T) {
		out := callActuate(t, newCICFlowRegistry(nil, nil).doer, allowLocal(t, srv), srv.URL)
		assert.Equal(t, "blocked_address", out["code"])
		assert.Zero(t, hits.Load(), "the server was never reached")
		assert.Zero(t, targetHits.Load())
	})

	t.Run("a redirect to a non-allowed host is returned, not followed", func(t *testing.T) {
		out := callActuate(t, newEgressClient(true), allowLocal(t, srv), srv.URL) // private allowed: only the redirect is tested
		assert.Equal(t, float64(http.StatusFound), out["status"], "%v", out)
		assert.EqualValues(t, 1, hits.Load(), "only the first hop was made")
		assert.Zero(t, targetHits.Load(), "the redirect target was never contacted: the forbidden second request did not happen")
		hdrs, ok := out["headers"].(map[string]interface{})
		require.True(t, ok, "%v", out)
		assert.Equal(t, target.URL+"/latest/meta-data/", hdrs["location"], "the redirect is handed back to the caller verbatim")
	})

	t.Run("an over-cap response is a typed error", func(t *testing.T) {
		big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(egressMaxResponseBytes+1))
			w.WriteHeader(http.StatusOK)
		}))
		defer big.Close()
		out := callActuate(t, newEgressClient(true), allowLocal(t, big), big.URL)
		assert.Equal(t, "response_too_large", out["code"])
	})
}

// issueIntegrationLeaf issues a second code-signing leaf (not revoked) from the test root.
func issueIntegrationLeaf(t *testing.T, p *crlTestPKI, serial int64) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "Integration Good Signer"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.rootCert, &key.PublicKey, p.rootKey)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
