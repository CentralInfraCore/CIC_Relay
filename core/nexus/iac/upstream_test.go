package iac

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func silentLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(&bytes.Buffer{})
	return l
}

// jsonServer returns an httptest.Server that responds with the given body and status.
func jsonServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body)) //nolint:errcheck
	}))
}

func TestFetchConfig_JSONResponse(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"relay.yaml":"kind: Relay","hosts/h1.yaml":"kind: Host"}`)
	defer srv.Close()

	src := NewUpstreamSource(srv.URL, "", 5*time.Second, silentLogger())
	result, err := src.FetchConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []byte("kind: Relay"), result["relay.yaml"])
	assert.Equal(t, []byte("kind: Host"), result["hosts/h1.yaml"])
}

func TestFetchConfig_YAMLResponse(t *testing.T) {
	yamlBody := "relay.yaml: \"kind: Relay\"\nhosts/h1.yaml: \"kind: Host\"\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(yamlBody)) //nolint:errcheck
	}))
	defer srv.Close()

	src := NewUpstreamSource(srv.URL, "", 5*time.Second, silentLogger())
	result, err := src.FetchConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []byte("kind: Relay"), result["relay.yaml"])
}

func TestFetchConfig_BearerTokenSent(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	defer srv.Close()

	src := NewUpstreamSource(srv.URL, "my-secret-token", 5*time.Second, silentLogger())
	_, err := src.FetchConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer my-secret-token", gotAuth)
}

func TestFetchConfig_HTTP404_Error(t *testing.T) {
	srv := jsonServer(t, http.StatusNotFound, `{"error":"not found"}`)
	defer srv.Close()

	// Override backoff to zero for fast test.
	orig := retryBackoff
	retryBackoff = []time.Duration{0, 0, 0}
	defer func() { retryBackoff = orig }()

	src := NewUpstreamSource(srv.URL, "", 5*time.Second, silentLogger())
	_, err := src.FetchConfig(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestFetchConfig_Retry_SucceedsOnThirdAttempt(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok.yaml":"data"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	orig := retryBackoff
	retryBackoff = []time.Duration{0, 0, 0}
	defer func() { retryBackoff = orig }()

	src := NewUpstreamSource(srv.URL, "", 5*time.Second, silentLogger())
	result, err := src.FetchConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []byte("data"), result["ok.yaml"])
	assert.Equal(t, int32(3), atomic.LoadInt32(&calls))
}

func TestFetchConfig_AllRetriesExhausted(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	orig := retryBackoff
	retryBackoff = []time.Duration{0, 0, 0}
	defer func() { retryBackoff = orig }()

	src := NewUpstreamSource(srv.URL, "", 5*time.Second, silentLogger())
	_, err := src.FetchConfig(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "3 attempts")
	assert.Equal(t, int32(3), atomic.LoadInt32(&calls))
}

func TestFetchConfig_ContextCancelled(t *testing.T) {
	// Server that blocks until the client disconnects.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	src := NewUpstreamSource(srv.URL, "", 5*time.Second, silentLogger())
	_, err := src.FetchConfig(ctx)
	assert.Error(t, err)
}

func TestParseUpstreamResponse_JSON(t *testing.T) {
	body := `{"a.yaml":"hello","b.json":"world"}`
	result, err := parseUpstreamResponse("application/json", []byte(body))
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), result["a.yaml"])
	assert.Equal(t, []byte("world"), result["b.json"])
}

func TestParseUpstreamResponse_YAML(t *testing.T) {
	body := "a.yaml: hello\nb.yaml: world\n"
	result, err := parseUpstreamResponse("text/yaml", []byte(body))
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), result["a.yaml"])
}

func TestParseUpstreamResponse_InvalidJSON(t *testing.T) {
	_, err := parseUpstreamResponse("application/json", []byte("{not json"))
	assert.Error(t, err)
}

func TestParseUpstreamResponse_EmptyBody(t *testing.T) {
	result, err := parseUpstreamResponse("application/json", []byte(`{}`))
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestNewUpstreamSourceFromEnv_NoURL(t *testing.T) {
	t.Setenv("CIC_UPSTREAM_URL", "")
	src, err := NewUpstreamSourceFromEnv(silentLogger())
	assert.NoError(t, err)
	assert.Nil(t, src, "nil returned when CIC_UPSTREAM_URL is absent")
}

func TestNewUpstreamSourceFromEnv_WithURL(t *testing.T) {
	t.Setenv("CIC_UPSTREAM_URL", "http://relay.example.com/config")
	t.Setenv("CIC_UPSTREAM_TOKEN", "tok-abc")
	t.Setenv("CIC_UPSTREAM_FETCH_TIMEOUT", "")

	src, err := NewUpstreamSourceFromEnv(silentLogger())
	require.NoError(t, err)
	require.NotNil(t, src)
	assert.Equal(t, "http://relay.example.com/config", src.url)
	assert.Equal(t, "tok-abc", src.token)
}

func TestNewUpstreamSourceFromEnv_InvalidTimeout(t *testing.T) {
	t.Setenv("CIC_UPSTREAM_URL", "http://relay.example.com/config")
	t.Setenv("CIC_UPSTREAM_FETCH_TIMEOUT", "notaduration")

	_, err := NewUpstreamSourceFromEnv(silentLogger())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CIC_UPSTREAM_FETCH_TIMEOUT")
}