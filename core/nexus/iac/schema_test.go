package iac

import (
	"reflect"
	"testing"
)

// ociSchemaDoc is the OCI VM's field-behavior surface, authored as a schema. The
// compiled plan must reproduce ociPlan() (the hand-built one), and provider_id —
// marked read-only — must be excluded as observed state.
func ociSchemaDoc() map[string]interface{} {
	return map[string]interface{}{
		"shape": map[string]interface{}{
			"behavior": map[string]interface{}{"compare": map[string]interface{}{"type": "exact"}},
		},
		"memory_gb": map[string]interface{}{
			"behavior": map[string]interface{}{"compare": map[string]interface{}{"type": "numeric"}},
		},
		"provider_id": map[string]interface{}{
			"behavior": map[string]interface{}{"mode": map[string]interface{}{"write": false}},
		},
		"network_interfaces": map[string]interface{}{
			"collection": map[string]interface{}{"topology": "map", "keys": []interface{}{"name"}},
			"element": map[string]interface{}{
				"subnet": map[string]interface{}{
					"behavior": map[string]interface{}{"compare": map[string]interface{}{"type": "exact"}},
				},
			},
		},
	}
}

func TestCompilePlan_OCISchema(t *testing.T) {
	plan := CompilePlan(ParseSchema(ociSchemaDoc()))

	// Writable scalars only, sorted by path; provider_id (read-only) excluded.
	wantScalars := []FieldPlan{
		{Path: "/memory_gb", Compare: CompareNumeric},
		{Path: "/shape", Compare: CompareExact},
	}
	if !reflect.DeepEqual(plan.Scalars, wantScalars) {
		t.Errorf("scalars = %+v, want %+v", plan.Scalars, wantScalars)
	}
	for _, fp := range plan.Scalars {
		if fp.Path == "/provider_id" {
			t.Errorf("read-only provider_id leaked into the plan")
		}
	}

	// One map-topology collection with the writable subnet element.
	if len(plan.Collections) != 1 {
		t.Fatalf("collections = %d, want 1", len(plan.Collections))
	}
	cp := plan.Collections[0]
	if cp.Path != "/network_interfaces" || cp.Collection.Topology != TopologyMap ||
		!reflect.DeepEqual(cp.Collection.Keys, []string{"name"}) {
		t.Errorf("collection = %+v", cp)
	}
	if !reflect.DeepEqual(cp.Elements, []FieldPlan{{Path: "subnet", Compare: CompareExact}}) {
		t.Errorf("collection elements = %+v", cp.Elements)
	}
}

func TestCompilePlan_MatchesHandBuiltVerdict(t *testing.T) {
	compiled := CompilePlan(ParseSchema(ociSchemaDoc()))

	fromSchema := mustEvaluate(t, ociIntent(), ociObserved(), ociObservation(), compiled)
	handBuilt := mustEvaluate(t, ociIntent(), ociObserved(), ociObservation(), ociPlan())

	if fromSchema.Object != ObjConformant {
		t.Errorf("compiled-plan object = %q, want CONFORMANT", fromSchema.Object)
	}
	if !reflect.DeepEqual(fromSchema.Fields, handBuilt.Fields) {
		t.Errorf("compiled plan gave different verdicts:\n compiled=%v\n hand=%v", fromSchema.Fields, handBuilt.Fields)
	}
}

func TestParseSchema_DefaultsWritableExact(t *testing.T) {
	// A field with no behavior block: writable, exact.
	s := ParseSchema(map[string]interface{}{"region": map[string]interface{}{}})
	if len(s.Fields) != 1 {
		t.Fatalf("fields = %d, want 1", len(s.Fields))
	}
	f := s.Fields[0]
	if f.Path != "/region" || !f.Mode.Write || f.Compare != "" {
		t.Errorf("field = %+v, want writable exact /region", f)
	}
	// And it compiles to a comparison (default exact is honoured by Compare).
	if plan := CompilePlan(s); len(plan.Scalars) != 1 || plan.Scalars[0].Path != "/region" {
		t.Errorf("compiled scalars = %+v, want one /region", plan.Scalars)
	}
}

func TestCompilePlan_ReadOnlyCollectionExcluded(t *testing.T) {
	// A collection marked read-only is observed state, not compared.
	doc := map[string]interface{}{
		"attached_disks": map[string]interface{}{
			"behavior":   map[string]interface{}{"mode": map[string]interface{}{"write": false}},
			"collection": map[string]interface{}{"topology": "set"},
		},
	}
	if plan := CompilePlan(ParseSchema(doc)); len(plan.Collections) != 0 || len(plan.Scalars) != 0 {
		t.Errorf("read-only collection compiled into a plan: %+v", plan)
	}
}
