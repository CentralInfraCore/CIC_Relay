package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRun_UsesGivenPort_AndServesRoot(t *testing.T) {
	// stub ListenAndServe: ne nyisson portot, csak ellenőrizzük a paramétereket
	called := false
	var gotAddr string
	var gotHandler http.Handler

	defer func(prev func(addr string, handler http.Handler) error) { listenAndServe = prev }(listenAndServe)

	listenAndServe = func(addr string, handler http.Handler) error {
		called = true
		gotAddr = addr
		gotHandler = handler
		// ne blokkoljon: azonnal térjen vissza sikerrel
		return nil
	}

	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)

	err := run(":12345", logger)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !called {
		t.Fatalf("listenAndServe was not called")
	}
	if gotAddr != ":12345" {
		t.Fatalf("listenAndServe addr = %q, want %q", gotAddr, ":12345")
	}

	// Ellenőrizzük, hogy a handler valóban kiszolgálja a "/"-t a várt JSON-nal
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	gotHandler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	body := strings.TrimSuffix(w.Body.String(), "\n") // Fprintln miatt \n a végén
	var m map[string]string
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("response not JSON: %v; body=%q", err, body)
	}
	if m["status"] != "ok" || m["message"] != "central relay responding" {
		t.Fatalf("unexpected JSON: %+v", m)
	}

	// Log sor ellenőrzése
	if !strings.Contains(logBuf.String(), "Central relay API started on :12345") {
		t.Fatalf("startup log missing/incorrect: %q", logBuf.String())
	}
}

func TestRun_PropagatesListenError(t *testing.T) {
	defer func(prev func(addr string, handler http.Handler) error) { listenAndServe = prev }(listenAndServe)

	wantErr := errors.New("bind failed")
	listenAndServe = func(addr string, handler http.Handler) error {
		return wantErr
	}

	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)

	err := run(":0", logger)
	if !errors.Is(err, wantErr) {
		t.Fatalf("run err = %v, want %v", err, wantErr)
	}
}
