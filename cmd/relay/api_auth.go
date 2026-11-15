package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// minAPITokenLength is the shortest accepted CIC_API_TOKEN. A short secret on an
// endpoint that signs artifacts is not a credential.
const minAPITokenLength = 16

// apiTokenFromEnv reads CIC_API_TOKEN. Unset or empty means "not configured": the
// authenticated routes then refuse every request (fail closed). A value shorter than
// minAPITokenLength is a start-up error, so a weak token is never silently accepted.
func apiTokenFromEnv() (string, error) {
	tok := strings.TrimSpace(os.Getenv("CIC_API_TOKEN"))
	if tok != "" && len(tok) < minAPITokenLength {
		return "", fmt.Errorf("CIC_API_TOKEN is too short (%d characters, minimum %d)", len(tok), minAPITokenLength)
	}
	return tok, nil
}

// authenticated wraps h so it runs only for a request carrying `Authorization: Bearer
// <CIC_API_TOKEN>`. With no token configured every request is refused. The comparison is
// constant-time over fixed-length digests, so neither the token nor its length leaks.
func (s *relayServer) authenticated(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.tokenAuthorized(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid bearer token is required")
			return
		}
		h(w, r)
	}
}

func (s *relayServer) tokenAuthorized(r *http.Request) bool {
	if s.apiToken == "" {
		return false // not configured: fail closed
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || got == "" {
		return false
	}
	want := sha256.Sum256([]byte(s.apiToken))
	have := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(want[:], have[:]) == 1
}

// routeAccess classifies an API route.
type routeAccess int

const (
	// accessPublic: no credentials required. Every public route is listed explicitly,
	// with the reason, so a new route cannot become unauthenticated by accident.
	accessPublic routeAccess = iota
	// accessAuthenticated: requires the bearer token (authenticated).
	accessAuthenticated
)

// apiRoute is one route of the relay API and how it is protected.
type apiRoute struct {
	path    string
	handler http.HandlerFunc
	access  routeAccess
	reason  string // why a route is public; required for accessPublic
}

// apiRoutes is the single place routes are declared. newMux registers exactly this
// list, and TestAPIRoutes_AreClassified fails if a route is added another way or a
// public route has no stated reason.
func apiRoutes(s *relayServer) []apiRoute {
	const knownLimitation = "unauthenticated by design of the PoC (README: Állapot és biztonsági helyzet); tracked, not decided here"
	return []apiRoute{
		{"/", s.rootHandler, accessPublic, "static status banner"},
		{"/set", s.setHandler, accessPublic, knownLimitation},
		{"/v1/schema/compile", s.compileHandler, accessAuthenticated, ""},
		{"/v1/schemas/pipeline", s.pipelineHandler, accessPublic, knownLimitation},
		{"/v1/repo/ci", s.repoCIHandler, accessPublic, knownLimitation},
		{"/v1/proof/verify", s.proofVerifyHandler, accessPublic, "read-only verification of a supplied artifact"},
		{"/healthz", s.healthzHandler, accessPublic, "liveness probe"},
		{"/readyz", s.readyzHandler, accessPublic, "readiness probe"},
		{"/metrics", s.metricsHandler, accessPublic, "scrape endpoint"},
	}
}
