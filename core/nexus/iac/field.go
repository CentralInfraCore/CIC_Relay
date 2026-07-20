package iac

// Field parameter expansion — the base of the IaC format (feature-011).
//
// Every parameter is, at bottom, a rich descriptor. A bare scalar
//
//	key: VALUE
//
// is shorthand for
//
//	key: {value: VALUE, default: false}
//
// which is shorthand for
//
//	key: {value: VALUE, default: false, mode: {read: true, write: true, implemented: true, visible: true}}
//
// The three forms are equivalent: [ExpandField] normalizes any of them to the same
// canonical [Field], so a compact spec and a fully-written spec are indistinguishable
// downstream (and hash identically — the same determinism contract as
// CanonicalizeSetPayload).
//
// The mode flags are the field-level capability/status surface:
//   - read / write — the state (observed) and intent (config) sides; a field can be
//     read-only (provider-computed) or writable (declared);
//   - implemented — the CIC three-status *at field granularity*: a bare field is
//     implemented; a scaffold/concept field sets it false explicitly;
//   - visible — whether the field is surfaced.
//
// The set is intentionally extensible; unknown flags in the source are ignored by
// the typed [FieldMode] but preserved conceptually for later rounds.

// FieldMode is the per-field capability/status flags. A bare scalar expands with
// every flag true; a partially-specified mode fills missing flags as true.
type FieldMode struct {
	Read        bool `json:"read" yaml:"read"`
	Write       bool `json:"write" yaml:"write"`
	Implemented bool `json:"implemented" yaml:"implemented"`
	Visible     bool `json:"visible" yaml:"visible"`
}

// defaultMode is the mode a bare scalar expands to: everything true.
func defaultMode() FieldMode {
	return FieldMode{Read: true, Write: true, Implemented: true, Visible: true}
}

// Field is the canonical (expanded) form of a parameter.
type Field struct {
	Value   interface{} `json:"value" yaml:"value"`
	Default bool        `json:"default" yaml:"default"`
	Mode    FieldMode   `json:"mode" yaml:"mode"`
}

// ExpandField normalizes a raw parameter value into a canonical [Field].
//
// A map carrying a "value" key is treated as a (partial) descriptor: its missing
// "default" and "mode" pieces are filled with the canonical defaults. Any other
// value — a scalar, a list, or a map without a "value" key — is the value itself,
// expanding to {value: raw, default: false, mode: all-true}. (Descending into a
// map-without-value is a later round: here it stays an opaque value.)
func ExpandField(raw interface{}) Field {
	if m, ok := raw.(map[string]interface{}); ok {
		if v, hasValue := m["value"]; hasValue {
			return Field{
				Value:   v,
				Default: asBool(m["default"], false),
				Mode:    modeFrom(m["mode"]),
			}
		}
	}
	return Field{Value: raw, Default: false, Mode: defaultMode()}
}

// ExpandSpec expands every top-level parameter of a resource spec into its
// canonical [Field] form.
func ExpandSpec(spec map[string]interface{}) map[string]Field {
	out := make(map[string]Field, len(spec))
	for k, v := range spec {
		out[k] = ExpandField(v)
	}
	return out
}

// modeFrom reads a mode descriptor, defaulting every missing flag to true (so a
// bare or partially-specified field is "all capabilities on" unless narrowed).
func modeFrom(raw interface{}) FieldMode {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return defaultMode()
	}
	return FieldMode{
		Read:        asBool(m["read"], true),
		Write:       asBool(m["write"], true),
		Implemented: asBool(m["implemented"], true),
		Visible:     asBool(m["visible"], true),
	}
}

// asBool coerces a raw value to bool, returning def when absent or not a bool.
func asBool(raw interface{}, def bool) bool {
	if b, ok := raw.(bool); ok {
		return b
	}
	return def
}
