package iac

import "strings"

// Stable field identity (feature-011, iac-object-model.md, spec #5). A field's
// path is where it currently lives; its field_id (`cic:namespace:object:field`) is
// what it *is*. Ownership, ACL, proof and observation key on the field_id so they
// survive a path rename: rename `/memory_gb` to `/memory_gib` in the schema and the
// field keeps `cic:compute:vm:memory`, so a proof produced before the rename is
// still interpretable after it.
//
// This slice provides the identity type and the re-keying of a conformance verdict
// from paths to field_ids. A collection element's identity is the element field's
// id plus the element key (`cic:compute:vm:subnet/{name=nic-0}`), mirroring the
// object model's element-identity rule.

// FieldID is a field's stable, path-independent identity.
type FieldID string

// RekeyByFieldID converts a path-keyed conformance verdict into a field_id-keyed
// one using this schema's field_id assignments. The result is stable across a path
// rename: two schema versions that differ only in a field's path but share its
// field_id produce the same field_id-keyed verdict for the same object.
//
// A path with no field_id in the schema is kept under a "path:<path>" key so it
// never collides with a real id.
func (s Schema) RekeyByFieldID(result ConformanceResult) map[FieldID]FieldVerdict {
	out := make(map[FieldID]FieldVerdict, len(result.Fields))
	for path, verdict := range result.Fields {
		out[s.fieldIDForPath(path)] = verdict
	}
	return out
}

// fieldIDForPath resolves a verdict path to its stable identity: a direct match for
// scalars, or the element field's id plus the element key for a collection element
// path. Falls back to "path:<path>" when no field_id is assigned.
func (s Schema) fieldIDForPath(path string) FieldID {
	for _, f := range s.Fields {
		if f.Collection != nil {
			if id, ok := elementFieldID(f, path); ok {
				return id
			}
			continue
		}
		if f.Path == path && f.FieldID != "" {
			return f.FieldID
		}
	}
	return FieldID("path:" + path)
}

// elementFieldID resolves a collection element path
// ("/network_interfaces/{name=nic-0}/subnet") to the element field's id plus the
// element key ("cic:...:subnet/{name=nic-0}").
func elementFieldID(f SchemaField, path string) (FieldID, bool) {
	prefix := f.Path + "/{"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := path[len(f.Path)+1:] // "{name=nic-0}/subnet"
	i := strings.Index(rest, "}/")
	if i < 0 {
		return "", false
	}
	elemKey := rest[:i+1] // "{name=nic-0}"
	elemField := rest[i+2:]
	for _, ef := range f.Elements {
		if ef.Path == elemField && ef.FieldID != "" {
			return FieldID(string(ef.FieldID) + "/" + elemKey), true
		}
	}
	return "", false
}
