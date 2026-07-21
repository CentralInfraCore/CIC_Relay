package iac

// Object references — the "hidden object behind a parameter", first deeper round
// (feature-011, iac-object-model.md). A parameter like
//
//	route_table: production-private
//
// is, at bottom, not a scalar but a pointer to another resource. The canonical
// form marks it explicitly as the field's value:
//
//	route_table:
//	  $value: { ref: production-private, kind: RouteTable }
//
// [Field.AsRef] recognizes it; the validator resolves the target against the graph
// (a dangling or wrong-kind reference is an error), the same integrity the
// top-level Dependencies get, but at field granularity.

// FieldRef is a reference from a field to another resource.
type FieldRef struct {
	Target string // the referenced resource's logical name
	Kind   string // optional: the expected kind of the target
}

// AsRef reports whether the field's value is an object reference and returns it.
// A reference is a map value carrying a non-empty "ref" string; an optional "kind"
// narrows the expected target kind.
func (f Field) AsRef() (FieldRef, bool) {
	m, ok := f.Value.(map[string]interface{})
	if !ok {
		return FieldRef{}, false
	}
	target, ok := m["ref"].(string)
	if !ok || target == "" {
		return FieldRef{}, false
	}
	kind, _ := m["kind"].(string)
	return FieldRef{Target: target, Kind: kind}, true
}
