package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"context"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEgressParity_Fixture asserts the shared policy fixture, which the Rust http-executor
// asserts too (http-executor/src/lib.rs, egress_parity_fixture_matches_policy).
func TestEgressParity_Fixture(t *testing.T) {
	f, err := os.Open("../../testdata/egress-parity/policy.txt")
	require.NoError(t, err)
	defer f.Close()

	consts := map[string]int64{
		"request_timeout_secs": int64(egressRequestTimeout.Seconds()),
		"connect_timeout_secs": int64(egressConnectTimeout.Seconds()),
		"max_response_bytes":   egressMaxResponseBytes,
	}
	seen := map[string]bool{}
	ips := 0
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		switch {
		case len(fields) == 3 && fields[0] == "const":
			want, perr := strconv.ParseInt(fields[2], 10, 64)
			require.NoError(t, perr, "line %d", line)
			got, known := consts[fields[1]]
			require.True(t, known, "line %d: unknown const %q", line, fields[1])
			assert.Equal(t, want, got, "const %s", fields[1])
			seen[fields[1]] = true
		case len(fields) == 3 && fields[0] == "ip":
			ip := net.ParseIP(fields[2])
			require.NotNil(t, ip, "line %d: bad address %q", line, fields[2])
			assert.Equal(t, fields[1] == "blocked", isBlockedIP(ip), "address %s", fields[2])
			ips++
		default:
			t.Fatalf("line %d: cannot parse %q", line, text)
		}
	}
	require.NoError(t, sc.Err())
	for name := range consts {
		assert.True(t, seen[name], "fixture lacks const %s", name)
	}
	assert.Greater(t, ips, 20, "the fixture must keep its address cases")
}

// isolatedEgressClient is newEgressClient(true) whose dialer refuses, and counts, every destination
// that is not a loopback IP literal. A test that puts an external host in a redirect (a cloud
// metadata address, a public name) then cannot touch real infrastructure if a regression starts
// following redirects: the dial is refused before any name is resolved or any packet is sent, and the
// returned counter, which must stay zero, shows that the follow-up request was attempted.
func isolatedEgressClient(t *testing.T) (*http.Client, *atomic.Int32) {
	t.Helper()
	c := newEgressClient(true)
	tr, ok := c.Transport.(*http.Transport)
	require.True(t, ok)
	orig := tr.DialContext
	refused := new(atomic.Int32)
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			refused.Add(1)
			return nil, fmt.Errorf("test isolation: refusing to dial %q", addr)
		}
		return orig(ctx, network, addr)
	}
	return c, refused
}

// callActuate runs makeActuateFn against policy/doer and returns the decoded output.
func callActuate(t *testing.T, doer httpDoer, policy egressPolicy, target string) map[string]interface{} {
	t.Helper()
	mod := newMod(16384)
	_, out := invoke(makeActuateFn(cicFlowDeps{doer: doer, egress: policy}), mod, map[string]interface{}{
		"method": "GET", "url": target,
	})
	return out
}

func allowLocal(t *testing.T, srv *httptest.Server) egressPolicy {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return egressPolicy{{Host: u.Hostname(), Ports: []int{port}}}
}

// Acceptance 1: an allowed URL that answers 302 to a non-allowed host (loopback and
// link-local included) must not be followed.
func TestActuate_RedirectToNonAllowedHost_NotFollowed(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("must not be reached"))
	}))
	defer other.Close()

	for name, location := range map[string]string{
		"other loopback port": other.URL + "/secret",
		"link-local metadata": "http://169.254.169.254/latest/meta-data/",
		"other host":          "https://evil.example/",
	} {
		t.Run(name, func(t *testing.T) {
			allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, location, http.StatusFound)
			}))
			defer allowed.Close()

			// allowPrivate: the test servers are on loopback; redirect handling is what is tested.
			// The isolating client refuses any non-loopback dial, so a regression that follows the
			// redirect to the metadata address or to a public name cannot reach the network.
			client, refused := isolatedEgressClient(t)
			out := callActuate(t, client, allowLocal(t, allowed), allowed.URL+"/start")

			assert.EqualValues(t, 0, hits.Load(), "the redirect target must never be contacted")
			assert.EqualValues(t, 0, refused.Load(), "no connection beyond the allowed local server may even be attempted")
			require.Equal(t, float64(http.StatusFound), out["status"], "the 302 is returned to the caller, not followed (output: %v)", out)
			hdrs, ok := out["headers"].(map[string]interface{})
			require.True(t, ok, "the response headers must be returned: %v", out)
			assert.Equal(t, location, hdrs["location"])
		})
	}
}

// Acceptance 2: a response over the cap is a typed error and is never held in memory.
func TestActuate_ResponseOverCap_TypedError(t *testing.T) {
	t.Run("chunked, no declared length", func(t *testing.T) {
		var written atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chunk := make([]byte, 64<<10)
			for i := 0; i < 1024; i++ { // up to 64 MiB offered
				n, err := w.Write(chunk)
				written.Add(int64(n))
				if err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}))
		defer srv.Close()

		out := callActuate(t, newEgressClient(true), allowLocal(t, srv), srv.URL+"/big")
		assert.Equal(t, "response_too_large", out["code"])
		assert.Contains(t, out["error"], "byte limit")
		assert.Less(t, written.Load(), int64(16<<20), "the reader must stop near the cap, not drain the body")
	})
	t.Run("declared length over the cap", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(egressMaxResponseBytes+1))
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		out := callActuate(t, newEgressClient(true), allowLocal(t, srv), srv.URL+"/declared")
		assert.Equal(t, "response_too_large", out["code"])
	})
	t.Run("one byte over, injected doer", func(t *testing.T) {
		doer := &stubDoer{resp: okResponse(200, strings.Repeat("x", egressMaxResponseBytes+1), nil)}
		out := callActuate(t, doer, egressPolicy{{Host: "api.example.com"}}, "https://api.example.com/x")
		assert.Equal(t, "response_too_large", out["code"], "the cap applies whichever doer is injected")
	})
}

// A body exactly at the cap is accepted: the limit is on bytes over, not at.
func TestActuate_ResponseAtCap_NotRefused(t *testing.T) {
	body := strings.Repeat("x", egressMaxResponseBytes)
	doer := &stubDoer{resp: okResponse(200, body, nil)}
	mod := newMod(2 << 20)
	reqLen := putJSON(mod, 0, map[string]interface{}{"method": "GET", "url": "https://api.example.com/x"})
	n := makeActuateFn(cicFlowDeps{doer: doer, egress: egressPolicy{{Host: "api.example.com"}}})(
		context.Background(), mod, 0, reqLen, 4096, uint32(len(mod.mem.buf))-4096)
	require.Positive(t, n)
	out := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(mod.mem.buf[4096:4096+uint32(n)], &out))
	require.Nil(t, out["error"], "a body of exactly the cap must pass: %v", out["error"])
	encoded, ok := out["body_base64"].(string)
	require.True(t, ok, "the body must be returned: %v", out)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	assert.Len(t, raw, egressMaxResponseBytes)
}

// The default client must refuse to dial blocked addresses (SSRF guard, as in the Rust executor).
func TestEgressClient_DialGuard_BlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("reached"))
	}))
	defer srv.Close()

	_, err := newEgressClient(false).Get(srv.URL)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errEgressBlockedAddr), "got %v", err)

	reg := newCICFlowRegistry(nil, nil)
	out := callActuate(t, reg.doer, allowLocal(t, srv), srv.URL)
	assert.Equal(t, "blocked_address", out["code"])
}

// Ambient proxy environment must be ignored. (Go never proxies loopback destinations, so a
// request-level check against a local server could not fail; the Transport itself is asserted.)
func TestEgressClient_IgnoresProxyEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:3128")

	for _, allowPrivate := range []bool{false, true} {
		tr, ok := newEgressClient(allowPrivate).Transport.(*http.Transport)
		require.True(t, ok)
		assert.Nil(t, tr.Proxy, "no proxy function: the environment is never consulted (allowPrivate=%v)", allowPrivate)
	}
	// The contrast: the default transport does consult the environment.
	assert.NotNil(t, http.DefaultTransport.(*http.Transport).Proxy)
}

// The dial guard refuses an address it cannot parse instead of letting it through.
func TestEgressClient_DialGuard_MalformedAddressNotDialed(t *testing.T) {
	tr, ok := newEgressClient(false).Transport.(*http.Transport)
	require.True(t, ok)
	_, err := tr.DialContext(context.Background(), "tcp", "not-an-ip-literal:80")
	require.Error(t, err)
	assert.NotContains(t, fmt.Sprint(err), "reached")
}
