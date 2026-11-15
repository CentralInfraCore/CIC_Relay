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
}

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
