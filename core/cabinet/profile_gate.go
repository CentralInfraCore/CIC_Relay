package cabinet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// INT-06/07: the runtime consumption of an admitted ABI-profile binding, before a step is dispatched.
//
// With generic dispatch on and a binding for the module, the step passes this gate: the operation must be one the manifest
// declared AND the profile has; its effect (static, or resolved from a declared request field with the profile's default)
// must be allowed by the operator's effect policy; the request must be within the profile's byte limit. The decision is
// recorded with a digest, allowed or denied alike, and reaches the ProofTrace V2 artifact. A refusal here happens BEFORE the
// execution phase: the step is REJECTED, never executed.
//
// The request and the response are also validated against the schema BODIES the profile names (id and digest), through the
// operator's PayloadValidator (WithPayloadValidator). Pinning an id and a digest proves which schema is meant; only the
// validation proves the payload conforms. It fails closed: no validator configured, a body that is not available for exactly
// that digest, or a payload that does not conform, each refuse the call with a named code.

// Effect classes of an operation (ref 02, ABI-02).
const (
	// EffectLocal changes nothing outside the module.
	EffectLocal = "local"
	// EffectExternalRead reads an external system.
	EffectExternalRead = "external-read"
	// EffectExternalWrite changes an external system.
	EffectExternalWrite = "external-write"
)

// Result codes of the profile gate (the rejection codes of the V2 step).
const (
	// CodeEffectNotAuthorized: the operation's resolved effect is not allowed by the operator's effect policy.
	CodeEffectNotAuthorized = "EFFECT_NOT_AUTHORIZED"
	// CodeRequestTooLarge: the request exceeds the profile's max_request_bytes.
	CodeRequestTooLarge = "REQUEST_TOO_LARGE"
	// CodeSchemaValidationUnconfigured: a profile-bound step needs payload validation and no validator is configured.
	CodeSchemaValidationUnconfigured = "SCHEMA_VALIDATION_UNCONFIGURED"
	// CodeSchemaUnavailable: no schema body is available for the profile's schema id and digest.
	CodeSchemaUnavailable = "SCHEMA_UNAVAILABLE"
	// CodeRequestSchemaInvalid: the request does not conform to the operation's request schema.
	CodeRequestSchemaInvalid = "REQUEST_SCHEMA_INVALID"
	// CodeResponseSchemaInvalid: the response does not conform to the operation's response schema.
	CodeResponseSchemaInvalid = "RESPONSE_SCHEMA_INVALID"
	// CodeResponseTooLarge: the response exceeds the profile's max_response_bytes.
	CodeResponseTooLarge = "RESPONSE_TOO_LARGE"
)

// ProfileGateError is a refusal of the profile gate.
type ProfileGateError struct {
	// Phase is the V2 rejection phase (policy or dispatch).
	Phase string
	// Code is the result code.
	Code   string
	Detail string
	base   error
}

// Error renders the code first.
func (e *ProfileGateError) Error() string {
	return fmt.Sprintf("%v: %s: %s", e.base, e.Code, e.Detail)
}

// Unwrap lets errors.Is see the base class (ErrInvalid).
func (e *ProfileGateError) Unwrap() error { return e.base }

// ErrSchemaUnavailable is returned by a PayloadValidator when it holds no schema body for the id and digest asked for.
var ErrSchemaUnavailable = errors.New("schema body unavailable")

// PayloadValidator validates one JSON payload against the schema body registered for exactly (schemaID, schemaDigest). It
// returns ErrSchemaUnavailable (wrapped or not) when it has no body for that pair, and any other error for a payload that
// does not conform.
type PayloadValidator interface {
	ValidatePayload(schemaID, schemaDigest string, payload []byte) error
}

// WithPayloadValidator configures the operator's payload validation for profile-bound operations. Without it, a
// profile-bound step is refused (SCHEMA_VALIDATION_UNCONFIGURED): an operation is never dispatched unvalidated.
func WithPayloadValidator(v PayloadValidator) CabinetOption {
	return func(s *cabinetService) { s.payloadValidator = v }
}

// WithEffectPolicy configures the operator's effect policy: the effect classes a profile-bound operation may have. Setting
// it also lets sign+send operations dispatch (their host boundary is then enforced host-side by the send engine); unset,
// they fail closed as before. A nil or empty list allows nothing.
func WithEffectPolicy(allowed ...string) CabinetOption {
	return func(s *cabinetService) {
		s.effectPolicy = map[string]bool{}
		for _, e := range allowed {
			s.effectPolicy[e] = true
		}
	}
}

// profileGate is the outcome of a passed gate.
type profileGate struct {
	op          AbiOperation
	maxResponse int
	timeout     time.Duration
	validator   PayloadValidator
}

// authzDecision is the recorded decision of a step.
type authzDecision struct {
	outcome string
	digest  string
}

// allowedEffects is the operator's allowed effect classes, sorted.
func (c *cabinetService) allowedEffects() []string {
	out := make([]string, 0, len(c.effectPolicy))
	for e, ok := range c.effectPolicy {
		if ok {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// resolveEffect returns the effect class of op for this input and how it was resolved.
func resolveEffect(op AbiOperation, input interface{}) (effect, via string) {
	if op.Effect != "contract-dependent" || op.EffectResolution == nil {
		return op.Effect, "static"
	}
	r := op.EffectResolution
	if m, ok := input.(map[string]interface{}); ok {
		if v, ok := m[r.Field].(string); ok {
			if e, ok := r.Map[v]; ok {
				return e, "request_field:" + r.Field + "=" + v
			}
		}
	}
	return r.Default, "default"
}

// decisionDigest is the digest of the decision record.
func decisionDigest(mod *ModuleDescriptor, b *ModuleProfileBinding, op AbiOperation, effect, via string, allowed []string, outcome, reason string) string {
	var sb strings.Builder
	sb.WriteString("CICAUTHZv1\n")
	for _, kv := range [][2]string{
		{"module", mod.ID()}, {"envelope_digest", b.EnvelopeDigest}, {"profile_id", b.ProfileID}, {"profile_digest", b.ProfileDigest},
		{"operation", op.Name}, {"host_boundary", op.HostBoundary}, {"effect", effect}, {"effect_resolved", via},
		{"policy_allowed_effects", strings.Join(allowed, ",")}, {"decision", outcome}, {"reason", reason},
	} {
		sb.WriteString(kv[0] + "=" + kv[1] + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// profileGateFor decides how a WASM step dispatches. (nil, nil): no binding, legacy dispatch. (nil, err): the step is
// refused before execution. (gate, nil): the profile-driven dispatch under the gate's limits.
func (c *cabinetService) profileGateFor(m *ModuleDescriptor, step ExecutionGraphStep, input interface{}, rec *traceV2Recorder) (*profileGate, error) {
	b, err := c.profileBindingFor(m)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	op, found := b.Operation(step.FunctionName)
	if !found {
		return nil, fmt.Errorf("%w: module %q ABI profile %q does not declare operation %q for this module", ErrInvalid, step.ComponentID, b.Profile.ProfileID, step.FunctionName)
	}
	if c.effectPolicy == nil {
		// no operator effect policy: only host-boundary "none" operations dispatch, as before INT-06/07
		if op.HostBoundary != "none" {
			return nil, fmt.Errorf("%w: operation %q has host_boundary %q, which generic dispatch does not execute without an operator effect policy", ErrInvalid, op.Name, op.HostBoundary)
		}
		return c.gateLimits(b, op, input)
	}
	effect, via := resolveEffect(op, input)
	allowed := c.allowedEffects()
	if !c.effectPolicy[effect] {
		d := decisionDigest(m, b, op, effect, via, allowed, AuthorizationDenied, "effect_not_allowed")
		rec.stepDecision(AuthorizationDenied, d)
		return nil, &ProfileGateError{Phase: RejectionPhasePolicy, Code: CodeEffectNotAuthorized, base: ErrInvalid,
			Detail: fmt.Sprintf("operation %q has effect %q (%s), which the operator's policy %v does not allow", op.Name, effect, via, allowed)}
	}
	rec.stepDecision(AuthorizationAllowed, decisionDigest(m, b, op, effect, via, allowed, AuthorizationAllowed, "effect_allowed"))
	return c.gateLimits(b, op, input)
}

// gateLimits applies the profile's request ceiling and returns the response ceiling and the timeout for the run.
func (c *cabinetService) gateLimits(b *ModuleProfileBinding, op AbiOperation, input interface{}) (*profileGate, error) {
	g := &profileGate{op: op, validator: c.payloadValidator}
	if c.payloadValidator == nil {
		return nil, &ProfileGateError{Phase: RejectionPhasePolicy, Code: CodeSchemaValidationUnconfigured, base: ErrInvalid,
			Detail: fmt.Sprintf("operation %q needs its request and response validated against the profile's schemas and no payload validator is configured", op.Name)}
	}
	reqJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("%w: the request cannot be encoded: %v", ErrInvalid, err)
	}
	if err := validatePayload(c.payloadValidator, op.RequestSchema, reqJSON, CodeRequestSchemaInvalid); err != nil {
		return nil, err
	}
	if l := b.Profile.Limits; l != nil {
		if l.MaxRequestBytes > 0 && len(reqJSON) > l.MaxRequestBytes {
			return nil, &ProfileGateError{Phase: RejectionPhaseDispatch, Code: CodeRequestTooLarge, base: ErrInvalid,
				Detail: fmt.Sprintf("the request is %d bytes, the profile allows %d", len(reqJSON), l.MaxRequestBytes)}
		}
		g.maxResponse = l.MaxResponseBytes
		g.timeout = time.Duration(l.TimeoutMs) * time.Millisecond
	}
	return g, nil
}

// validatePayload validates payload against the schema ref; invalidCode is the code of a non-conforming payload.
func validatePayload(v PayloadValidator, ref AbiSchemaRef, payload []byte, invalidCode string) error {
	err := v.ValidatePayload(ref.ID, ref.Digest, payload)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrSchemaUnavailable):
		return &ProfileGateError{Phase: RejectionPhasePolicy, Code: CodeSchemaUnavailable, base: ErrInvalid,
			Detail: fmt.Sprintf("no schema body for %s (%s): %v", ref.ID, ref.Digest, err)}
	default:
		return &ProfileGateError{Phase: RejectionPhaseDispatch, Code: invalidCode, base: ErrInvalid,
			Detail: fmt.Sprintf("the payload does not conform to %s: %v", ref.ID, err)}
	}
}

// validateResponse validates a call's response against the operation's response schema (nil: conforms).
func (g *profileGate) validateResponse(data, errorJSON string) error {
	if errorJSON != "" && errorJSON != "null" {
		return nil // the guest reported an error: there is no response document to validate
	}
	return validatePayload(g.validator, g.op.ResponseSchema, []byte(data), CodeResponseSchemaInvalid)
}
