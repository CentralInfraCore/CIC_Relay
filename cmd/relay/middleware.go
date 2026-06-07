package main

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// ipRateLimiter maintains per-IP token bucket limiters.
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

func newIPRateLimiter(rps int) *ipRateLimiter {
	return &ipRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rate.Limit(rps),
		burst:    20,
	}
}

func (l *ipRateLimiter) getLimiter(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.limiters[ip]
	if !ok {
		lim = rate.NewLimiter(l.rps, l.burst)
		l.limiters[ip] = lim
	}
	return lim
}

// Middleware returns an http.Handler that enforces per-IP rate limiting.
// Exceeding the limit yields 429 Too Many Requests.
func (l *ipRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !l.getLimiter(ip).Allow() {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// concurrentLimiter enforces a cap on simultaneous in-flight requests.
type concurrentLimiter struct {
	sem chan struct{}
}

func newConcurrentLimiter(max int) *concurrentLimiter {
	return &concurrentLimiter{sem: make(chan struct{}, max)}
}

// Middleware returns an http.Handler that enforces the concurrent request cap.
// When the cap is reached, 503 Service Unavailable is returned immediately.
func (l *concurrentLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case l.sem <- struct{}{}:
			defer func() { <-l.sem }()
			next.ServeHTTP(w, r)
		default:
			writeError(w, http.StatusServiceUnavailable, "overloaded", "server busy")
		}
	})
}

// buildHandler wraps the mux with rate limiting and concurrent cap middleware.
func buildHandler(s *relayServer) http.Handler {
	mux := newMux(s)
	cl := newConcurrentLimiter(s.config.MaxConcurrentRequests)
	rl := newIPRateLimiter(s.config.RateLimitRPS)
	return rl.Middleware(cl.Middleware(mux))
}
