package cabinet

import "fmt"

// This file is the Go implementation of the runtime-only ABI-profile rules the
// JSON Schema cannot express (ref 02 §7, ABI-01..04 + SS-05). The reference is
// jobs/o1-wp03-abi-profile-dispatch/ref/reference-build_o1_schemas.py.txt's
// check_profile_semantics (lines 190-217) and its 10 labelled cases in
// validation-report.txt ("abi-semantics ..."): CheckProfileSemantics below is a
// line-for-line port, same check order, same codes, so the same 10 cases apply
// (see abi_profile_semantics_test.go).

// Named violation codes, ABI-01..04 + SS-05 (ref 02 §7).
const (
	// CodeDuplicateOperation: ABI-01: two operations of one profile share a name.
	CodeDuplicateOperation = "DUPLICATE_OPERATION"
	// CodeSchemaRefUnresolved: ABI-03: a request or response schema reference is not in the operator's schema store.
	CodeSchemaRefUnresolved = "SCHEMA_REF_UNRESOLVED"
	// CodeSchemaDigestMismatch: ABI-03: the referenced schema's digest differs from the stored one.
	CodeSchemaDigestMismatch = "SCHEMA_DIGEST_MISMATCH"
	// CodeEffectFieldUnresolvable: ABI-02: the effect_resolution field is not a declared enum field of the request schema.
	CodeEffectFieldUnresolvable = "EFFECT_FIELD_UNRESOLVABLE"
	// CodeEffectMapKeyNotInFieldDomain: ABI-02: an effect_resolution map key is outside the field's value domain.
	CodeEffectMapKeyNotInFieldDomain = "EFFECT_MAP_KEY_NOT_IN_FIELD_DOMAIN"
	// CodeHostCapabilityInconsistent: ABI-04: sign+send operations and the provider-http@1 capability do not match.
	CodeHostCapabilityInconsistent = "HOST_CAPABILITY_INCONSISTENT"
	// CodeLimitExceedsOperatorCap: SS-05: a profile limit is above the operator cap.
	CodeLimitExceedsOperatorCap = "LIMIT_EXCEEDS_OPERATOR_CAP"
)

// ABIProfileError is a named, machine-checkable ABI-profile admission failure.
// errors.Is(err, ErrInvalid) holds for it, like every other Cabinet validation
// failure; Code carries the specific ABI-01..05/SS-05/registry violation.
type ABIProfileError struct {
	Code string
}

// Error renders the ErrInvalid prefix and the named code.
func (e *ABIProfileError) Error() string {
	return fmt.Sprintf("%s: %s", ErrInvalid, e.Code)
}

// Is makes errors.Is(err, ErrInvalid) hold for ABIProfileError.
func (e *ABIProfileError) Is(target error) bool { return target == ErrInvalid }

// ABIProfileErrorCode returns the named code of an ABIProfileError, if err is one.
func ABIProfileErrorCode(err error) (string, bool) {
	if e, ok := err.(*ABIProfileError); ok {
		return e.Code, true
	}
	return "", false
}

// SchemaRegistryEntry is one operator-registered schema: its pinned digest, and
// for request schemas used as an effect_resolution field source, the field's
// enum domain (ABI-02's "egy deklarált, felsorolásos mező").
type SchemaRegistryEntry struct {
	Digest string
	Fields map[string][]string // field name -> allowed values
}

// SchemaRegistry resolves a profile's schema references against the operator's
// schema store (ABI-03). The physical location of that store is an open
// question (ref 02 §6); this interface is what CheckProfileSemantics needs from
// it. InMemorySchemaRegistry below is the test/reference implementation.
type SchemaRegistry interface {
	Resolve(id string) (SchemaRegistryEntry, bool)
}

// InMemorySchemaRegistry is a fixed-map SchemaRegistry, for tests and for any
// caller that already has its schema set resolved in memory.
type InMemorySchemaRegistry map[string]SchemaRegistryEntry

// Resolve looks the schema id up in the fixed map.
func (r InMemorySchemaRegistry) Resolve(id string) (SchemaRegistryEntry, bool) {
	e, ok := r[id]
	return e, ok
}

// OperatorLimits is the operator's upper bound on a profile's limits (SS-05):
// "a profil-limitek az operátori korlátot nem lépik túl". Zero means "no
// operator cap is declared for this field", mirroring the reference's dict
// lookup: a profile field with no corresponding cap entry is a programming
// error in the caller (the reference's OPERATOR_CAPS covers all three fields),
// not something CheckProfileSemantics silently allows.
type OperatorLimits struct {
	MaxRequestBytes  int
	MaxResponseBytes int
	TimeoutMs        int
}

// cap returns the operator cap for a limit field name.
func (c OperatorLimits) cap(field string) (int, bool) {
	switch field {
	case "max_request_bytes":
		return c.MaxRequestBytes, true
	case "max_response_bytes":
		return c.MaxResponseBytes, true
	case "timeout_ms":
		return c.TimeoutMs, true
	default:
		return 0, false
	}
}

// limitFields returns a profile's declared (non-zero) limit fields in a fixed
// order, so CheckProfileSemantics's result does not depend on map iteration.
func (l *AbiLimits) limitFields() []struct {
	name  string
	value int
} {
	if l == nil {
		return nil
	}
	var out []struct {
		name  string
		value int
	}
	if l.MaxRequestBytes != 0 {
		out = append(out, struct {
			name  string
			value int
		}{"max_request_bytes", l.MaxRequestBytes})
	}
	if l.MaxResponseBytes != 0 {
		out = append(out, struct {
			name  string
			value int
		}{"max_response_bytes", l.MaxResponseBytes})
	}
	if l.TimeoutMs != 0 {
		out = append(out, struct {
			name  string
			value int
		}{"timeout_ms", l.TimeoutMs})
	}
	return out
}

// CheckProfileSemantics enforces ABI-01..04 and SS-05 (ref 02 §7) on an
// already structurally-valid AbiProfile (see ParseProfile). It returns the
// first violation as an *ABIProfileError, or nil. The check order mirrors
// check_profile_semantics exactly (reference-build_o1_schemas.py.txt:190-217):
// duplicate names, then per-operation schema refs and effect-field resolution,
// then host-capability consistency, then limits.
func CheckProfileSemantics(p *AbiProfile, registry SchemaRegistry, caps OperatorLimits) error {
	seen := make(map[string]bool, len(p.Operations))
	for _, op := range p.Operations {
		if seen[op.Name] {
			return &ABIProfileError{Code: CodeDuplicateOperation}
		}
		seen[op.Name] = true
	}

	for _, op := range p.Operations {
		for _, ref := range []AbiSchemaRef{op.RequestSchema, op.ResponseSchema} {
			entry, ok := registry.Resolve(ref.ID)
			if !ok {
				return &ABIProfileError{Code: CodeSchemaRefUnresolved}
			}
			if entry.Digest != ref.Digest {
				return &ABIProfileError{Code: CodeSchemaDigestMismatch}
			}
		}
		if op.EffectResolution != nil {
			reqEntry, _ := registry.Resolve(op.RequestSchema.ID) // already resolved above
			domain, ok := reqEntry.Fields[op.EffectResolution.Field]
			if !ok {
				return &ABIProfileError{Code: CodeEffectFieldUnresolvable}
			}
			domainSet := make(map[string]bool, len(domain))
			for _, v := range domain {
				domainSet[v] = true
			}
			for k := range op.EffectResolution.Map {
				if !domainSet[k] {
					return &ABIProfileError{Code: CodeEffectMapKeyNotInFieldDomain}
				}
			}
			// Values of the field that are not mapped fall to the fail-closed
			// default: allowed and deterministic (ABI-02).
		}
	}

	usesSend := false
	for _, op := range p.Operations {
		if op.HostBoundary == "sign+send" {
			usesSend = true
			break
		}
	}
	hasCap := false
	for _, c := range p.RequiredHostCapabilities {
		if c == SignSendHostCapability {
			hasCap = true
			break
		}
	}
	if usesSend != hasCap {
		return &ABIProfileError{Code: CodeHostCapabilityInconsistent}
	}

	for _, f := range p.Limits.limitFields() {
		capVal, ok := caps.cap(f.name)
		if ok && f.value > capVal {
			return &ABIProfileError{Code: CodeLimitExceedsOperatorCap}
		}
	}

	return nil
}
