// Package ois implements the relay-side OpenIntentSign gate (demo §8.4): before a
// state-changing intent — notably a rollback to an earlier, verified state — is
// applied, it must be authorized by policy. This mirrors the countersign pattern
// (a content-dependent authorization seam) for declarative intents: the policy is
// where the Relay is "smart"; the applier knows nothing about it.
package ois

import "fmt"

// Action names the kind of state-changing intent.
type Action string

const (
	// Rollback restores an earlier, verified state (demo §8.4).
	Rollback Action = "rollback"
	// Apply provisions a declared desired state.
	Apply Action = "apply"
)

// Intent is a declared, content-dependent request to change state: who wants to
// do what, targeting which verified state, and why. It carries no secret.
type Intent struct {
	// Actor is the requester's identity (e.g. a countersigned CN).
	Actor string
	// Action is the kind of change (rollback / apply).
	Action Action
	// TargetRef is the verified CommitRef whose state is being restored/applied.
	TargetRef string
	// Reason is a non-secret justification recorded in the audit trail.
	Reason string
}

// Denied is a policy refusal. Its Reason is non-secret, so it is always safe to
// log and to record in the ProofTrace.
type Denied struct {
	Reason string
}

func (d *Denied) Error() string { return d.Reason }

// Policy decides whether an Intent is authorized. A real deployment plugs richer
// rules (signed intents, RBAC, quorum) into the same seam; the trivial policy is
// a per-action actor allowlist. Authorize returns nil when authorized, or a
// *Denied when refused.
type Policy interface {
	Authorize(intent Intent) error
}

// AllowlistPolicy authorizes an intent iff its Actor is on the allowlist for its
// Action — the simplest content-dependent rule. Build it with NewAllowlistPolicy
// and Allow.
type AllowlistPolicy struct {
	allowed map[Action]map[string]struct{}
}

// NewAllowlistPolicy returns an empty allowlist policy (authorizes nothing until
// Allow is called).
func NewAllowlistPolicy() *AllowlistPolicy {
	return &AllowlistPolicy{allowed: make(map[Action]map[string]struct{})}
}

// Allow authorizes the given actors for the given action and returns the policy
// for chaining.
func (p *AllowlistPolicy) Allow(action Action, actors ...string) *AllowlistPolicy {
	set := p.allowed[action]
	if set == nil {
		set = make(map[string]struct{})
		p.allowed[action] = set
	}
	for _, a := range actors {
		set[a] = struct{}{}
	}
	return p
}

// Authorize permits the intent iff its Actor is allowlisted for its Action. A
// missing actor or target ref is always refused.
func (p *AllowlistPolicy) Authorize(intent Intent) error {
	if intent.Actor == "" {
		return &Denied{Reason: "intent has no actor"}
	}
	if intent.TargetRef == "" {
		return &Denied{Reason: "intent has no target ref"}
	}
	if _, ok := p.allowed[intent.Action][intent.Actor]; !ok {
		return &Denied{Reason: fmt.Sprintf("actor %q is not authorized for %q", intent.Actor, intent.Action)}
	}
	return nil
}
