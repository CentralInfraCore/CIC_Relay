package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestIPRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := newIPRateLimiter(100)
	handler := rl.Middleware(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestIPRateLimiter_BlocksWhenExhausted(t *testing.T) {
	// Use burst=20 and rps=1 — after 20 requests the limiter exhausts immediately.
	rl := newIPRateLimiter(1)
	handler := rl.Middleware(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:9999"

	// Drain the burst (20 requests succeed), next one should be rate-limited.
	got429 := false
	for i := 0; i < 30; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	assert.True(t, got429, "expected at least one 429 after burst exhausted")
}

func TestIPRateLimiter_IsolatesPerIP(t *testing.T) {
	rl := newIPRateLimiter(1)
	handler := rl.Middleware(okHandler)

	// Exhaust IP A
	reqA := httptest.NewRequest(http.MethodGet, "/", nil)
	reqA.RemoteAddr = "192.168.1.1:1111"
	for i := 0; i < 25; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), reqA)
	}

	// IP B should still be allowed (different limiter bucket)
	reqB := httptest.NewRequest(http.MethodGet, "/", nil)
	reqB.RemoteAddr = "192.168.1.2:2222"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, reqB)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestConcurrentLimiter_AllowsUnderCap(t *testing.T) {
	cl := newConcurrentLimiter(10)
	handler := cl.Middleware(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestConcurrentLimiter_BlocksWhenFull(t *testing.T) {
	cl := newConcurrentLimiter(2)

	// Hold 2 slots open with a blocking handler.
	barrier := make(chan struct{})
	blocking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-barrier
		w.WriteHeader(http.StatusOK)
	})
	handler := cl.Middleware(blocking)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			handler.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}

	// Give goroutines time to acquire slots.
	// Poll until semaphore is full.
	for len(cl.sem) < 2 {
		// busy wait; cap is 2 so this is safe
	}

	// Now the cap is full — next request must get 503.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	cl.Middleware(okHandler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

	close(barrier)
	wg.Wait()
}

func TestConcurrentLimiter_ReleasesSlotAfterRequest(t *testing.T) {
	cl := newConcurrentLimiter(1)
	handler := cl.Middleware(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req)
	assert.Equal(t, http.StatusOK, rr1.Code)

	// Slot should be released — second request must succeed too.
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req)
	assert.Equal(t, http.StatusOK, rr2.Code)
}