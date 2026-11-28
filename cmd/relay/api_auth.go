package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"centralrelay/core/cabinet"
)

// minAPITokenLength is the shortest accepted CIC_API_TOKEN. A short secret on an
// endpoint that signs artifacts is not a credential.
const minAPITokenLength = 16

// minAPITokenDistinct is the fewest distinct characters a token may have. A length alone does
// not make a secret: "aaaaaaaaaaaaaaaa" is long and worthless. This is a cheap sanity check, not
// a proof of entropy; generate the token randomly (for example `openssl rand -hex 32`).
const minAPITokenDistinct = 8

// apiTokenFromEnv reads CIC_API_TOKEN. Unset or empty means "not configured": the
// authenticated routes then refuse every request (fail closed). A value shorter than
// minAPITokenLength is a start-up error, so a weak token is never silently accepted.
func apiTokenFromEnv() (string, error) {
	tok := strings.TrimSpace(os.Getenv("CIC_API_TOKEN"))
	if tok != "" && len(tok) < minAPITokenLength {
		return "", fmt.Errorf("CIC_API_TOKEN is too short (%d characters, minimum %d)", len(tok), minAPITokenLength)
	}
	if tok != "" {
		distinct := map[rune]bool{}
		for _, r := range tok {
			distinct[r] = true
		}
		if len(distinct) < minAPITokenDistinct {
			return "", fmt.Errorf("CIC_API_TOKEN is too uniform (%d distinct characters, minimum %d): generate it randomly", len(distinct), minAPITokenDistinct)
		}
	}
	return tok, nil
}

// authenticated wraps h so it runs only for a request carrying `Authorization: Bearer
// <CIC_API_TOKEN>`. With no token configured every request is refused. The comparison is
// constant-time over fixed-length digests, so neither the token nor its length leaks.
func (s *relayServer) authenticated(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.tokenAuthorized(r) {
			unauthorized(w, "A valid bearer token is required")
			return
		}
		h(w, withAPIPrincipal(r))
	}
}

// optionallyAuthenticated is for a route that serves callers without credentials but must let a
// credentialed caller through to the protected operations: no Authorization header passes as
// anonymous; a header that is present must be valid (a wrong token is refused, never downgraded
// to anonymous); a valid token puts the principal into the request context. What an anonymous
// caller may do is decided at the execution boundary (cabinet.WithProtectedModules), not here.
func (s *relayServer) optionallyAuthenticated(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			h(w, r)
			return
		}
		if !s.tokenAuthorized(r) {
			unauthorized(w, "The bearer token is not valid")
			return
		}
		h(w, withAPIPrincipal(r))
	}
}

// unauthorized answers 401 with a `WWW-Authenticate: Bearer` challenge and the JSON error body msg.
func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, "unauthorized", msg)
}

// apiPrincipal is the identity the bearer token stands for. Default: the shared-token service identity. With
// CIC_API_PRINCIPAL_ID the operator names a dedicated principal (human, service or agent) for the token; the identity always
// comes from here, from the authentication layer, never from the request.
var apiPrincipal = cabinet.Principal{ID: "api-token", Method: "bearer", Type: cabinet.PrincipalTypeSharedBearer}

// apiPrincipalIDRE bounds a principal id: a stable identifier, not free text.
var apiPrincipalIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)

// apiPrincipalFromEnv reads CIC_API_PRINCIPAL_ID and CIC_API_PRINCIPAL_TYPE (human, service or agent; default service).
// Unset id: the shared-bearer default. A type without an id, an unknown type or a malformed id is a start-up error.
func apiPrincipalFromEnv() (cabinet.Principal, error) {
	id := strings.TrimSpace(os.Getenv("CIC_API_PRINCIPAL_ID"))
	typ := strings.TrimSpace(os.Getenv("CIC_API_PRINCIPAL_TYPE"))
	if id == "" {
		if typ != "" {
			return cabinet.Principal{}, fmt.Errorf("CIC_API_PRINCIPAL_TYPE is set without CIC_API_PRINCIPAL_ID")
		}
		return apiPrincipal, nil
	}
	if !apiPrincipalIDRE.MatchString(id) {
		return cabinet.Principal{}, fmt.Errorf("CIC_API_PRINCIPAL_ID is not a valid principal id")
	}
	if typ == "" {
		typ = cabinet.PrincipalTypeService
	}
	if typ == cabinet.PrincipalTypeSharedBearer || !cabinet.ValidPrincipalType(typ) {
		return cabinet.Principal{}, fmt.Errorf("CIC_API_PRINCIPAL_TYPE must be human, service or agent")
	}
	return cabinet.Principal{ID: id, Method: "bearer", Type: typ}, nil
}

// withAPIPrincipal returns r with the authenticated API principal in its context.
func withAPIPrincipal(r *http.Request) *http.Request {
	return r.WithContext(cabinet.WithPrincipal(r.Context(), apiPrincipal))
}

// protectRoute wraps a route's handler by its access class. Any class that is not explicitly
// public or optional is treated as authenticated: an unknown value must never yield a public route.
func (s *relayServer) protectRoute(rt apiRoute) http.HandlerFunc {
	switch rt.access {
	case accessPublic:
		return rt.handler
	case accessOptional:
		return s.optionallyAuthenticated(rt.handler)
	default: // accessAuthenticated and anything unknown
		return s.authenticated(rt.handler)
	}
}

// tokenAuthorized reports whether r carries `Authorization: Bearer <CIC_API_TOKEN>`. It is false when no
// token is configured (fail closed) or the header is missing or empty; the comparison is constant-time over
// SHA-256 digests of both values, so neither the token nor its length leaks.
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
	// accessOptional: serves anonymous callers, and a valid bearer token authorizes the protected
	// operations (optionallyAuthenticated). The protected operations themselves are enforced at
	// the execution boundary, so an anonymous caller still cannot reach them.
	accessOptional
)

// protectedModuleIDs are the components only an authenticated principal may run, enforced by
// cabinet.Setx for every entry path, whichever route started the workflow. They are the
// operations behind the authenticated endpoints: signing (the operation that uses the signing
// key), and the components that clone a caller-supplied repository or run its make targets.
// Authenticating the endpoints alone would let /set chain the same components and walk around
// that. TestProtectedModules_ClassifyEveryNativeModule fails when a native module is registered
// that is neither listed here nor on the explicit unprotected list.
var protectedModuleIDs = []string{
	"cic.artifact.sign@1.0",  // signs with the relay's key
	"cic.pipeline.start@1.0", // git clone of a caller-supplied repository
	"cic.pipeline.test@1.0",  // make target of that repository
	"cic.pipeline.validate@1.0",
	"cic.pipeline.release@1.0",
	"cic.repo.build@1.0",
	"ci.build@1.0", // make <target>
}

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
	return []apiRoute{
		{"/", s.rootHandler, accessPublic, "static status banner"},
		// /set starts any workflow, so it cannot be authenticated as a whole; a valid token
		// authorizes the protected steps, and without one Setx refuses a workflow that contains them.
		{"/set", s.setHandler, accessOptional, "starts any workflow: anonymous callers may run unprotected ones; a valid token authorizes protected steps, which Setx refuses without one"},
		{"/v1/schema/compile", s.compileHandler, accessAuthenticated, ""},
		{"/v1/schemas/pipeline", s.pipelineHandler, accessAuthenticated, ""},
		{"/v1/repo/ci", s.repoCIHandler, accessAuthenticated, ""},
		{"/v1/proof/verify", s.proofVerifyHandler, accessPublic, "read-only verification of a supplied artifact"},
		{"/healthz", s.healthzHandler, accessPublic, "liveness probe"},
		{"/readyz", s.readyzHandler, accessPublic, "readiness probe"},
		{"/metrics", s.metricsHandler, accessPublic, "scrape endpoint"},
	}
}
