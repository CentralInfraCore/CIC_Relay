package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/merkle"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// boundaryEnv is a real cabinet service with the production protected-module list, counting
// native stand-ins for the protected components, and the workflows an attacker would try.
type boundaryEnv struct {
	server *relayServer
	mux    http.Handler
	runs   map[string]int
	ids    map[string]string // short name -> workflow ID
}

func newBoundaryEnv(t *testing.T) *boundaryEnv {
	t.Helper()
	ctx := context.Background()
	env := &boundaryEnv{runs: map[string]int{}, ids: map[string]string{}}
	svc, err := cabinet.NewCabinetService(ctx, cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(),
		cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1},
		cabinet.WithProtectedModules(protectedModuleIDs...))
	require.NoError(t, err)

	register := func(name, fn string) {
		mod, err := cabinet.NewModuleDescriptor(ctx, cabinet.BaseBlock{Name: name, Version: "1.0"}, "native", nil, nil, nil)
		require.NoError(t, err)
		mod.NativeImpl = cabinet.NativeFunctions{fn: func(_ context.Context, in interface{}) (interface{}, error) {
			env.runs[name]++
			return in, nil
		}}
		require.NoError(t, svc.PutModule(mod))
	}
	for _, m := range [][2]string{{"echo", "echo"}, {"cic.artifact.sign", "sign"}, {"cic.pipeline.start", "start"}, {"cic.repo.build", "build"}} {
		register(m[0], m[1])
	}
	addWF := func(short, name string, steps ...string) {
		wf, err := cabinet.NewWorkflow(ctx, cabinet.WorkflowIdentity{Version: "1.0"}, cabinet.WorkflowConfig{Name: name, Steps: steps}, cabinet.WorkflowSigning{})
		require.NoError(t, err)
		require.NoError(t, svc.PutWorkflow(wf))
		env.ids[short] = wf.ID()
	}
	addWF("public", "wf.public", "echo@1.0.echo(input) -> e")
	addWF("sign", "wf.sign", "echo@1.0.echo(input) -> e", "cic.artifact.sign@1.0.sign(e) -> signed")
	// A workflow with no signing step that clones a repository: it must be protected by the
	// component it runs, not by the presence of a signing step.
	addWF("clone", "wf.clone", "cic.pipeline.start@1.0.start(input) -> started")
	addWF("build", "wf.build", "cic.repo.build@1.0.build(input) -> artifact")
	// The workflows the authenticated endpoints start, under their real IDs.
	addWF("compile", "cic.schema.compile", "echo@1.0.echo(input) -> a", "cic.artifact.sign@1.0.sign(a) -> signed_artifact")

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	env.server = &relayServer{api: svc, config: &config{MaxJSONInputSize: 1 << 20}, logger: logger, apiToken: testAPIToken}
	env.mux = newMux(env.server)
	return env
}

func (e *boundaryEnv) post(path, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func setBody(workflowID string) string {
	b, _ := json.Marshal(map[string]any{"workflow_id": workflowID, "payload": map[string]any{"msg": "hi"}})
	return string(b)
}

func (e *boundaryEnv) totalProtectedRuns() int {
	return e.runs["cic.artifact.sign"] + e.runs["cic.pipeline.start"] + e.runs["cic.repo.build"]
}

// The core of the review finding: /set must not be a way around the protection. Anonymous callers,
// and callers with a wrong token, cannot run a protected operation by naming a workflow.
func TestBoundary_SetCannotRunProtectedOperationsAnonymously(t *testing.T) {
	for _, wf := range []string{"sign", "clone", "build"} {
		t.Run(wf, func(t *testing.T) {
			env := newBoundaryEnv(t)

			w := env.post("/set", "", setBody(env.ids[wf]))
			assert.Equal(t, http.StatusUnauthorized, w.Code, "anonymous: %s", w.Body.String())
			assertErrorJSON(t, w, "unauthorized")
			assert.Equal(t, 0, env.totalProtectedRuns(), "no protected operation ran")
			assert.Equal(t, 0, env.runs["echo"], "and no step before it ran either")

			w = env.post("/set", "Bearer "+testAPIToken+"x", setBody(env.ids[wf]))
			assert.Equal(t, http.StatusUnauthorized, w.Code, "a wrong token is refused, never downgraded to anonymous")
			assert.Equal(t, 0, env.totalProtectedRuns())

			w = env.post("/set", "Bearer "+testAPIToken, setBody(env.ids[wf]))
			assert.Equal(t, http.StatusOK, w.Code, "a valid token authorizes it: %s", w.Body.String())
			assert.Positive(t, env.totalProtectedRuns())
		})
	}
}

// /set stays usable for workflows with no protected step.
func TestBoundary_SetStillServesUnprotectedWorkflowsAnonymously(t *testing.T) {
	env := newBoundaryEnv(t)
	w := env.post("/set", "", setBody(env.ids["public"]))
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 1, env.runs["echo"])
	assert.Equal(t, 0, env.totalProtectedRuns())

	// A present but wrong token is refused even there: optional means "valid if present".
	w = env.post("/set", "Bearer wrong", setBody(env.ids["public"]))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// The same decision on every entry path: whichever route starts the work, an anonymous caller never
// reaches a protected operation.
func TestBoundary_EveryEntryPathRefusesAnonymousProtectedWork(t *testing.T) {
	routes := []struct{ name, path, body string }{
		{"/set with a signing workflow", "/set", ""},
		{"/set with a cloning workflow", "/set", ""},
		{"/v1/schema/compile", "/v1/schema/compile", `{"schema":"postgresql","version":"v0.18.dev"}`},
		{"/v1/schemas/pipeline", "/v1/schemas/pipeline", `{"repo":"https://example.invalid/x.git","branch":"main"}`},
		{"/v1/repo/ci", "/v1/repo/ci", `{"repo_url":"https://example.invalid/x.git","commit":"abc"}`},
	}
	env := newBoundaryEnv(t)
	routes[0].body = setBody(env.ids["sign"])
	routes[1].body = setBody(env.ids["clone"])
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			w := env.post(r.path, "", r.body)
			assert.Equal(t, http.StatusUnauthorized, w.Code, "%s: %s", r.path, w.Body.String())
			assert.Equal(t, 0, env.totalProtectedRuns(), "no protected operation may run for an anonymous caller")
		})
	}
}

// Defence in depth, the point of the boundary: even if a route's own authentication were
// removed or a handler were mounted without it, the execution boundary still refuses.
func TestBoundary_HandlerWithoutRouteAuth_StillRefusedAtTheBoundary(t *testing.T) {
	env := newBoundaryEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", strings.NewReader(`{"schema":"postgresql","version":"v0.18.dev"}`))
	w := httptest.NewRecorder()
	env.server.compileHandler(w, req) // bypasses newMux and its middleware entirely
	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	assert.Equal(t, 0, env.totalProtectedRuns())
}

// An unknown access class must never yield a public route.
func TestProtectRoute_UnknownAccessClassFailsClosed(t *testing.T) {
	env := newBoundaryEnv(t)
	reached := false
	h := env.server.protectRoute(apiRoute{path: "/x", access: routeAccess(99), handler: func(http.ResponseWriter, *http.Request) { reached = true }})

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, reached, "an unclassified route must require authentication")

	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+testAPIToken)
	h(w, req)
	assert.True(t, reached, "and still works with the token")
}

// Every native module is classified. A new module cannot be registered without a decision, and a
// protected ID that matches no registered module (a typo) would protect nothing.
func TestProtectedModules_ClassifyEveryNativeModule(t *testing.T) {
	unprotected := map[string]string{
		"core.test@1.0":         "test fixture, no effect",
		"cic.source.assert@1.0": "verifies a supplied assertion, read-only",
		"cic.schema.build@1.0":  "builds an unsigned artifact in memory; only signing it is protected",
		"cert.selfsigned@1.0":   "generates a self-signed certificate in memory, no relay key involved",
	}
	var registered []string
	m := new(mockCabinetService)
	m.On("PutModule", mock.Anything).Run(func(a mock.Arguments) {
		registered = append(registered, a.Get(0).(*cabinet.ModuleDescriptor).ID())
	}).Return(nil)
	m.On("PutWorkflow", mock.Anything).Return(nil)
	m.On("PutSchema", mock.Anything).Return(nil)
	m.On("GetModule", mock.Anything).Return(nil, cabinet.ErrNotFound)
	require.NoError(t, registerNativeComponents(context.Background(), m, &PKIAssets{}, merkle.BuildContext{}, nil, &config{}))
	require.NotEmpty(t, registered)

	protected := map[string]bool{}
	for _, id := range protectedModuleIDs {
		protected[id] = true
	}
	regSet := map[string]bool{}
	for _, id := range registered {
		regSet[id] = true
		_, isUnprotected := unprotected[id]
		assert.True(t, protected[id] || isUnprotected, "native module %s is neither protected nor explicitly unprotected: decide and list it", id)
		assert.False(t, protected[id] && isUnprotected, "native module %s is listed as both", id)
	}
	for id := range protected {
		assert.True(t, regSet[id], "protected ID %s matches no registered native module and protects nothing", id)
	}
	for id := range unprotected {
		assert.True(t, regSet[id], "unprotected entry %s matches no registered native module (stale)", id)
	}
}
