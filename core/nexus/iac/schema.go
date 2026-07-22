package iac

import "sort"

// Schema-compiled conformance plan (feature-011, iac-object-model.md: "no
// hand-written ConformanceSurface — the conformance plan is compiled from the
// schema's behavior annotations"). This is the last seam of the object model: the
// ConformancePlan that [Evaluate] consumes is no longer hand-built, it is compiled
// from a schema whose fields carry behavior annotations.
//
// The load-bearing rule is the mode.read/write split: a *writable* field is a
// config/intent field and becomes a comparison; a *read-only* field is
// provider-computed observed state and is omitted — so provider_id / assigned IPs
// never enter the plan, and the "extra state is not drift" property falls out of
// the schema itself rather than an ad-hoc exclusion list. `behavior.compare`
// selects the comparator; `collection` carries list topology.

// SchemaField is one field's behavior surface as authored in a schema: its
// canonical path, comparator, mode (read/write/…), and — for a list field — the
// collection topology plus the per-element field behaviors (relative paths).
type SchemaField struct {
	Path       string
	FieldID    FieldID // stable identity (schema.field_id); survives a path rename
	Compare    CompareType
	Mode       FieldMode
	Sensitive  SensitivePolicy // secret placement/custody (behavior.sensitive)
	Collection *Collection
	Elements   []SchemaField
}

// Schema is the authored field-behavior surface a [ConformancePlan] compiles from.
type Schema struct {
	Fields []SchemaField
}

// CompilePlan compiles a schema into the [ConformancePlan] that [Evaluate] runs.
// Only writable fields become comparisons: a writable scalar → a [FieldPlan]; a
// writable collection → a [CollectionPlan] carrying its writable element fields.
// Read-only fields are omitted — they are observed state, never compared against
// intent. Output order is deterministic (fields are pre-sorted by [ParseSchema]).
func CompilePlan(s Schema) ConformancePlan {
	var plan ConformancePlan
	for _, f := range s.Fields {
		if !f.Mode.Write {
			continue // read-only / computed → state-only, not compared
		}
		if f.Collection != nil {
			cp := CollectionPlan{Path: f.Path, Collection: *f.Collection}
			for _, ef := range f.Elements {
				if ef.Mode.Write {
					cp.Elements = append(cp.Elements, FieldPlan{Path: ef.Path, Compare: ef.Compare})
				}
			}
			plan.Collections = append(plan.Collections, cp)
			continue
		}
		plan.Scalars = append(plan.Scalars, FieldPlan{Path: f.Path, Compare: f.Compare})
	}
	return plan
}

// ParseSchema reads a schema document into a [Schema]. Each top-level key is a
// field; its value carries the behavior annotations:
//
//	shape:      { behavior: { compare: { type: exact } } }
//	memory_gb:  { behavior: { compare: { type: numeric } } }
//	provider_id:{ behavior: { mode: { write: false } } }        # computed → state-only
//	interfaces: { collection: { topology: map, keys: [name] },
//	              element: { subnet: { behavior: { compare: { type: exact } } } } }
//
// A missing `behavior.mode` defaults every flag to true (writable), matching the
// field model's scalar-sugar. Fields (and collection elements) are sorted by path
// so the compiled plan — and any digest over it — is deterministic.
func ParseSchema(doc map[string]interface{}) Schema {
	var s Schema
	for name, raw := range doc {
		s.Fields = append(s.Fields, parseSchemaField("/"+name, raw))
	}
	sort.Slice(s.Fields, func(i, j int) bool { return s.Fields[i].Path < s.Fields[j].Path })
	return s
}

// parseSchemaField parses one field descriptor at the given (already-prefixed)
// path. Element fields are parsed with their relative name as path.
func parseSchemaField(path string, raw interface{}) SchemaField {
	f := SchemaField{Path: path, Mode: defaultMode()}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return f // bare/unknown descriptor → writable, exact
	}

	if schema, ok := m["schema"].(map[string]interface{}); ok {
		if id, ok := schema["field_id"].(string); ok {
			f.FieldID = FieldID(id)
		}
	}

	if behavior, ok := m["behavior"].(map[string]interface{}); ok {
		if _, hasMode := behavior["mode"]; hasMode {
			f.Mode = modeFrom(behavior["mode"])
		}
		if compare, ok := behavior["compare"].(map[string]interface{}); ok {
			if t, ok := compare["type"].(string); ok {
				f.Compare = CompareType(t)
			}
		}
		if sensitive, ok := behavior["sensitive"].(map[string]interface{}); ok {
			f.Sensitive = parseSensitive(sensitive)
		}
	}

	if coll, ok := m["collection"].(map[string]interface{}); ok {
		c := Collection{Topology: CollectionTopology(asString(coll["topology"])), Keys: asStringSlice(coll["keys"])}
		f.Collection = &c
		if elems, ok := m["element"].(map[string]interface{}); ok {
			names := make([]string, 0, len(elems))
			for en := range elems {
				names = append(names, en)
			}
			sort.Strings(names) // deterministic element order
			for _, en := range names {
				f.Elements = append(f.Elements, parseSchemaField(en, elems[en]))
			}
		}
	}
	return f
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func asStringSlice(v interface{}) []string {
	list, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
