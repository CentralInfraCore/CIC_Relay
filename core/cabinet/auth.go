package cabinet

import (
	"context"
	"errors"
)

// ErrUnauthorized: the call reached a protected operation without an authenticated
// principal in its context.
var ErrUnauthorized = errors.New("unauthorized")

// IsUnauthorized checks if an error is an ErrUnauthorized.
func IsUnauthorized(err error) bool { return errors.Is(err, ErrUnauthorized) }

// Principal identifies the authenticated caller of a workflow execution. Whoever
// authenticates the caller (the HTTP layer, an operator, a test) puts it into the
// context with WithPrincipal; Setx only checks that one is present.
type Principal struct {
	ID     string // who: a stable identifier, never a secret
	Method string // how it was established, for example "bearer"
	// Type is what kind of identity this is (PrincipalType*). It is set by the authentication layer, never taken from a
	// request payload.
	Type string
}

// Principal types. A human and a service and a delegated agent are all valid identities (a CIC execution need not have a
// natural person behind it); a shared bearer token is a restricted service identity, not an individual.
const (
	// PrincipalTypeHuman is an interactive person.
	PrincipalTypeHuman = "human"
	// PrincipalTypeService is a dedicated, authenticated service identity (automated infrastructure management).
	PrincipalTypeService = "service"
	// PrincipalTypeAgent is a delegated agent acting for someone else.
	PrincipalTypeAgent = "agent"
	// PrincipalTypeSharedBearer is the shared CIC_API_TOKEN identity: a restricted service identity that does not
	// distinguish callers.
	PrincipalTypeSharedBearer = "shared-bearer-token"
)

// ValidPrincipalType reports whether t is one of the known principal types.
func ValidPrincipalType(t string) bool {
	switch t {
	case PrincipalTypeHuman, PrincipalTypeService, PrincipalTypeAgent, PrincipalTypeSharedBearer:
		return true
	}
	return false
}

// principalKey is the unexported context key under which WithPrincipal stores the Principal.
type principalKey struct{}

// WithPrincipal returns a context that carries p as the authenticated principal.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the authenticated principal of ctx, if there is one.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
