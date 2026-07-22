package iac

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"centralrelay/pkg/canonicaljson"
)

// The canonical Node IR (feature-011, iac-object-model.md, spec #5 cont. — "rich
// ValueKind / ObjectRef IR" and the "$value / $cic namespace split"). Where the
// rest of the model works over `interface{}`, this is the typed, closed value tree
// the object model ultimately targets: a discriminated [Value] (null / bool /
// number / string / list / object / object-ref / secret-ref) whose fields are
// private, so a value's kind cannot be confused and — with [Node] — the secret
// type boundary is enforced structurally rather than by inspecting a map shape.
//
// This lands the IR as a self-contained, tested type with [FromRaw] / [Value.Raw]
// interop, so the existing `interface{}`-based layers keep working unchanged (the
// canonical digest bytes are untouched). Migrating those layers onto the Node IR is
// the deferred final round the doc's scheduling calls out.

// ValueKind is the discriminant of a [Value].
type ValueKind uint8

const (
	// KindNull is an explicit null (distinct from an absent object key).
	KindNull ValueKind = iota
	// KindBool is a boolean.
	KindBool
	// KindNumber is a number, stored as its canonical decimal token.
	KindNumber
	// KindString is a string.
	KindString
	// KindList is an ordered list of values.
	KindList
	// KindObject is a string-keyed map of values.
	KindObject
	// KindObjectRef is a reference to another resource ([FieldRef]).
	KindObjectRef
	// KindSecretRef is a capability handle to a secret ([SecretRef]) — never plaintext.
	KindSecretRef
)

// Value is a node in the canonical value tree. Its fields are private: a Value is
// built only through the constructors, so its kind and payload stay consistent.
type Value struct {
	kind      ValueKind
	boolean   bool
	number    string // canonical decimal token (see number.go)
	text      string
	list      []Value
	object    map[string]Value
	objectRef FieldRef
	secretRef SecretRef
}

// NullValue returns an explicit null.
func NullValue() Value { return Value{kind: KindNull} }

// BoolValue returns a boolean value.
func BoolValue(b bool) Value { return Value{kind: KindBool, boolean: b} }

// StringValue returns a string value.
func StringValue(s string) Value { return Value{kind: KindString, text: s} }

// NumberValue returns a number value from any numeric literal, or false if it is
// not a number. The value is stored in canonical form.
func NumberValue(n interface{}) (Value, bool) {
	c, ok := canonicalNumber(n)
	if !ok {
		return Value{}, false
	}
	return Value{kind: KindNumber, number: c}, true
}

// ListValue returns a list value.
func ListValue(vs []Value) Value { return Value{kind: KindList, list: vs} }

// ObjectValue returns an object value.
func ObjectValue(m map[string]Value) Value { return Value{kind: KindObject, object: m} }

// ObjectRefValue returns an object-reference value.
func ObjectRefValue(r FieldRef) Value { return Value{kind: KindObjectRef, objectRef: r} }

// SecretRefValue returns a secret-reference value.
func SecretRefValue(r SecretRef) Value { return Value{kind: KindSecretRef, secretRef: r} }

// Kind returns the value's discriminant.
func (v Value) Kind() ValueKind { return v.kind }

// Bool returns the boolean and true when the kind is [KindBool].
func (v Value) Bool() (bool, bool) { return v.boolean, v.kind == KindBool }

// Number returns the canonical number token and true when the kind is [KindNumber].
func (v Value) Number() (string, bool) { return v.number, v.kind == KindNumber }

// Str returns the string and true when the kind is [KindString].
func (v Value) Str() (string, bool) { return v.text, v.kind == KindString }

// List returns the list and true when the kind is [KindList].
func (v Value) List() ([]Value, bool) { return v.list, v.kind == KindList }

// Object returns the object and true when the kind is [KindObject].
func (v Value) Object() (map[string]Value, bool) { return v.object, v.kind == KindObject }

// Ref returns the object reference and true when the kind is [KindObjectRef].
func (v Value) Ref() (FieldRef, bool) { return v.objectRef, v.kind == KindObjectRef }

// Secret returns the secret reference and true when the kind is [KindSecretRef].
func (v Value) Secret() (SecretRef, bool) { return v.secretRef, v.kind == KindSecretRef }

// FromRaw builds a [Value] from a decoded (interface{}) value, discriminating a
// secret reference ({secret_ref}) and an object reference ({ref, kind}) from an
// ordinary object, and canonicalizing numbers.
func FromRaw(raw interface{}) Value {
	switch x := raw.(type) {
	case nil:
		return NullValue()
	case bool:
		return BoolValue(x)
	case string:
		return StringValue(x)
	case []interface{}:
		list := make([]Value, len(x))
		for i, e := range x {
			list[i] = FromRaw(e)
		}
		return ListValue(list)
	case map[string]interface{}:
		if sr, ok := asSecretRef(x); ok {
			return SecretRefValue(sr)
		}
		if fr, ok := asObjectRef(x); ok {
			return ObjectRefValue(fr)
		}
		obj := make(map[string]Value, len(x))
		for k, e := range x {
			obj[k] = FromRaw(e)
		}
		return ObjectValue(obj)
	default:
		if v, ok := NumberValue(raw); ok {
			return v
		}
		return StringValue(fmt.Sprintf("%v", raw))
	}
}

// Raw converts a [Value] back into a plain interface{} tree, for interop with the
// existing layers. Numbers become json.Number in canonical form.
func (v Value) Raw() interface{} {
	switch v.kind {
	case KindNull:
		return nil
	case KindBool:
		return v.boolean
	case KindNumber:
		return json.Number(v.number)
	case KindString:
		return v.text
	case KindList:
		out := make([]interface{}, len(v.list))
		for i, e := range v.list {
			out[i] = e.Raw()
		}
		return out
	case KindObject:
		out := make(map[string]interface{}, len(v.object))
		for k, e := range v.object {
			out[k] = e.Raw()
		}
		return out
	case KindObjectRef:
		m := map[string]interface{}{"ref": v.objectRef.Target}
		if v.objectRef.Kind != "" {
			m["kind"] = v.objectRef.Kind
		}
		return m
	case KindSecretRef:
		return map[string]interface{}{"secret_ref": v.secretRef.Path}
	default:
		return nil
	}
}

// Digest returns the canonical SHA-256 digest of the value — deterministic across
// map key order and number representation.
func (v Value) Digest() (string, error) {
	canonical, err := canonicaljson.ToJSON(normalizeNumbers(v.Raw()))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Meta is the $cic side of a [Node]: a field's metadata, physically separate from
// its $value. It aggregates the per-field surfaces built elsewhere in the model.
type Meta struct {
	FieldID   FieldID
	Mode      FieldMode
	Compare   CompareType
	Sensitive SensitivePolicy
	ACL       ACL
}

// Node is the $value/$cic split made concrete: a value and its metadata, held
// separately so a module can read the value without the metadata leaking into it,
// and the metadata cannot be forged from the value side.
type Node struct {
	Value Value
	Meta  Meta
}

// Validate enforces the secret type boundary at the IR level: a secret field's
// value must be a [KindSecretRef] (never inline data), and a non-secret field's
// value must not be a secret reference. This is the structural form of
// [SensitivePolicy.ValidateValue] — the kind cannot lie.
func (n Node) Validate() error {
	isSecretVal := n.Value.kind == KindSecretRef
	switch n.Meta.Sensitive.Class {
	case SensitiveSecret:
		if !isSecretVal {
			return fmt.Errorf("secret field must hold a secret reference, not %s", n.Value.kind)
		}
	default:
		if isSecretVal {
			return fmt.Errorf("non-secret field must not hold a secret reference")
		}
	}
	return nil
}

// String renders a ValueKind for diagnostics.
func (k ValueKind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "bool"
	case KindNumber:
		return "number"
	case KindString:
		return "string"
	case KindList:
		return "list"
	case KindObject:
		return "object"
	case KindObjectRef:
		return "object-ref"
	case KindSecretRef:
		return "secret-ref"
	default:
		return "unknown"
	}
}
