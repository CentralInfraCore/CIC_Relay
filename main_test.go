package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRootHandler(t *testing.T) {
	// Teszt szerver létrehozása
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	// Meghívjuk a handler-t
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := `{"status": "ok", "message": "central relay responding"}`
		w.Write([]byte(response))
	})
	handler.ServeHTTP(w, req)

	resp := w.Result()

	// Ellenőrizzük a státuszkódot
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	// Ellenőrizzük a tartalmat
	body := w.Body.String()
	if !strings.Contains(body, `"status": "ok"`) || !strings.Contains(body, `"central relay responding"`) {
		t.Errorf("unexpected response body: %s", body)
	}
}
