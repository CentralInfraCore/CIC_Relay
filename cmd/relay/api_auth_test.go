package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"centralrelay/core/cabinet"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testAPIToken = "0123456789abcdef-test-token"

func newAuthTestServer(t *testing.T, token string) (*relayServer, *mockCabinetService) {
	t.Helper()
	api := new(mockCabinetService)
	api.On("Setx", mock.Anything, mock.Anything).Return(&cabinet.SetResponse{
		Results: map[string]any{"signed_artifact": map[string]any{"ok": true}},
	}, nil)
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	return &relayServer{api: api, config: &config{MaxJSONInputSize: 1 << 20}, logger: logger, apiToken: token}, api
}

func postCompile(t *testing.T, s *relayServer, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/compile", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	newMux(s).ServeHTTP(w, req)
	return w
}

const validCompileBody = `{"schema":"postgresql","version":"v0.18.dev"}`

// Acceptance 1: a request without credentials -> 401, and nothing runs.
func TestCompile_Authentication(t *testing.T) {
	cases := map[string]struct {
		token string
		auth  string
	}{
		"no credentials":              {testAPIToken, ""},
		"wrong token":                 {testAPIToken, "Bearer " + testAPIToken + "x"},
		"prefix of the token":         {testAPIToken, "Bearer " + testAPIToken[:8]},
		"other scheme":                {testAPIToken, "Basic " + testAPIToken},
		"no space after Bearer":       {testAPIToken, "Bearer" + testAPIToken},
		"lower-case scheme":           {testAPIToken, "bearer " + testAPIToken},
		"empty bearer":                {testAPIToken, "Bearer "},
		"not configured, any token":   {"", "Bearer " + testAPIToken},
		"not configured, empty token": {"", "Bearer "},
		"not configured, none":        {"", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s, api := newAuthTestServer(t, c.token)
			w := postCompile(t, s, c.auth, validCompileBody)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Equal(t, "Bearer", w.Header().Get("WWW-Authenticate"))
			assertErrorJSON(t, w, "unauthorized")
			api.AssertNotCalled(t, "Setx", mock.Anything, mock.Anything)
		})
	}

	t.Run("correct token reaches the handler", func(t *testing.T) {
		s, api := newAuthTestServer(t, testAPIToken)
		w := postCompile(t, s, "Bearer "+testAPIToken, validCompileBody)
		assert.Equal(t, http.StatusOK, w.Code)
		api.AssertCalled(t, "Setx", mock.Anything, mock.Anything)
	})
}

// Acceptance 2: control characters and anything outside the format -> 400.
func TestCompile_RejectsMalformedSchemaAndVersion(t *testing.T) {
	bad := map[string]string{
		"newline in schema":      `{"schema":"pg\nkind: Evil","version":"v1.0.0"}`,
		"newline in version":     `{"schema":"postgresql","version":"v1.0.0\n  checksum: x"}`,
		"carriage return":        `{"schema":"pg\r","version":"v1.0.0"}`,
		"NUL":                    `{"schema":"pg\u0000","version":"v1.0.0"}`,
		"tab":                    `{"schema":"pg\tx","version":"v1.0.0"}`,
		"space":                  `{"schema":"pg x","version":"v1.0.0"}`,
		"colon (YAML key)":       `{"schema":"a: b","version":"v1.0.0"}`,
		"slash (path)":           `{"schema":"../../etc","version":"v1.0.0"}`,
		"leading dot":            `{"schema":".hidden","version":"v1.0.0"}`,
		"curly (YAML flow)":      `{"schema":"{a}","version":"v1.0.0"}`,
		"over-long schema":       `{"schema":"` + strings.Repeat("a", 129) + `","version":"v1.0.0"}`,
		"over-long version":      `{"schema":"postgresql","version":"` + strings.Repeat("1", 65) + `"}`,
		"unicode letter":         `{"schema":"pgé","version":"v1.0.0"}`,
		"version with semicolon": `{"schema":"postgresql","version":"v1.0;x"}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			s, api := newAuthTestServer(t, testAPIToken)
			w := postCompile(t, s, "Bearer "+testAPIToken, body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrorJSON(t, w, "invalid_field")
			api.AssertNotCalled(t, "Setx", mock.Anything, mock.Anything)
		})
	}
	for _, body := range []string{
		`{"schema":"postgresql","version":"v0.18.dev"}`,
		`{"schema":"some.schema","version":"v1.0.0"}`,
		`{"schema":"cic-compute","version":"1.2.3+build.5"}`,
	} {
		s, _ := newAuthTestServer(t, testAPIToken)
		assert.Equal(t, http.StatusOK, postCompile(t, s, "Bearer "+testAPIToken, body).Code, body)
	}
}

// Acceptance 3: every route is authenticated or on an explicit, reasoned public allow-list,
// and routes cannot be registered any other way.
func TestAPIRoutes_AreClassified(t *testing.T) {
	s, _ := newAuthTestServer(t, testAPIToken)
	seen := map[string]bool{}
	for _, rt := range apiRoutes(s) {
		require.False(t, seen[rt.path], "route %s declared twice", rt.path)
		seen[rt.path] = true
		require.NotNil(t, rt.handler, "route %s has no handler", rt.path)
		switch rt.access {
		case accessPublic:
			assert.NotEmpty(t, strings.TrimSpace(rt.reason), "public route %s needs a stated reason", rt.path)
		case accessAuthenticated:
			// Through the real mux, without credentials: must be refused before the handler.
			req := httptest.NewRequest(http.MethodPost, rt.path, strings.NewReader("{}"))
			w := httptest.NewRecorder()
			newMux(s).ServeHTTP(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code, "authenticated route %s accepted a request without credentials", rt.path)
		default:
			t.Fatalf("route %s has an unknown access class", rt.path)
		}
	}
	assert.True(t, seen["/v1/schema/compile"], "the compile route must be declared")
}

// newMux must register routes only through the classified table: a direct
// mux.Handle/HandleFunc elsewhere in it would bypass the classification.
func TestNewMux_RegistersOnlyThroughTheRouteTable(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	var calls int
	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "newMux" {
			return true
		}
		found = true
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			if call, ok := m.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Handle" || sel.Sel.Name == "HandleFunc") {
					calls++
				}
			}
			return true
		})
		return false
	})
	require.True(t, found, "newMux not found")
	assert.Equal(t, 1, calls, "newMux must register through the apiRoutes loop only (one Handle call); a second call bypasses the classification")
}

func TestAPITokenFromEnv(t *testing.T) {
	t.Setenv("CIC_API_TOKEN", "")
	tok, err := apiTokenFromEnv()
	require.NoError(t, err)
	assert.Empty(t, tok, "unset means not configured, not an error")

	t.Setenv("CIC_API_TOKEN", " "+testAPIToken+" ")
	tok, err = apiTokenFromEnv()
	require.NoError(t, err)
	assert.Equal(t, testAPIToken, tok)

	t.Setenv("CIC_API_TOKEN", "short")
	_, err = apiTokenFromEnv()
	require.Error(t, err, "a weak token must be a start-up error")
	assert.Contains(t, err.Error(), "too short")
}

func TestCompileJSONErrorShapeUnchangedForValidRequests(t *testing.T) {
	s, _ := newAuthTestServer(t, testAPIToken)
	w := postCompile(t, s, "Bearer "+testAPIToken, validCompileBody)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body, "artifact")
}
