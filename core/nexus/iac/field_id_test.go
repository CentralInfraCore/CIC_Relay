package iac

import (
	"reflect"
	"testing"
)

func vmSchemaDoc(memPath string) map[string]interface{} {
	return map[string]interface{}{
		memPath: map[string]interface{}{
			"schema":   map[string]interface{}{"field_id": "cic:compute:vm:memory"},
			"behavior": map[string]interface{}{"compare": map[string]interface{}{"type": "numeric"}},
		},
		"shape": map[string]interface{}{
			"schema":   map[string]interface{}{"field_id": "cic:compute:vm:shape"},
			"behavior": map[string]interface{}{"compare": map[string]interface{}{"type": "exact"}},
		},
	}
}

// The headline of spec #5: identity survives a path rename. Two schema versions
// that differ only in a field's path but share its field_id yield different
// path-keyed verdicts but the SAME field_id-keyed verdict for the same object.
func TestRekeyByFieldID_SurvivesPathRename(t *testing.T) {
	eval := func(memPath string) (Schema, ConformanceResult) {
		schema := ParseSchema(vmSchemaDoc(memPath))
		plan := CompilePlan(schema)
		intent := map[string]interface{}{memPath: 16, "shape": "E4.Flex"}
		observed := map[string]interface{}{memPath: 16.0, "shape": "E4.Flex"}
		obs := Observation{Observed: []string{"/" + memPath, "/shape"}}
		return schema, mustEvaluate(t, intent, observed, obs, plan)
	}

	s1, r1 := eval("memory_gb")
	s2, r2 := eval("memory_gib")

	if reflect.DeepEqual(r1.Fields, r2.Fields) {
		t.Fatalf("path-keyed verdicts should differ across a rename: %v", r1.Fields)
	}

	k1 := s1.RekeyByFieldID(r1)
	k2 := s2.RekeyByFieldID(r2)
	if !reflect.DeepEqual(k1, k2) {
		t.Fatalf("field_id-keyed verdicts changed across rename:\n v1=%v\n v2=%v", k1, k2)
	}
	if k1["cic:compute:vm:memory"] != VerdictConformant || k1["cic:compute:vm:shape"] != VerdictConformant {
		t.Errorf("field_id verdicts = %v", k1)
	}
}

func TestRekeyByFieldID_ElementKey(t *testing.T) {
	doc := map[string]interface{}{
		"network_interfaces": map[string]interface{}{
			"collection": map[string]interface{}{"topology": "map", "keys": []interface{}{"name"}},
			"element": map[string]interface{}{
				"subnet": map[string]interface{}{
					"schema":   map[string]interface{}{"field_id": "cic:compute:vm:subnet"},
					"behavior": map[string]interface{}{"compare": map[string]interface{}{"type": "exact"}},
				},
			},
		},
	}
	schema := ParseSchema(doc)
	obj := map[string]interface{}{"network_interfaces": []interface{}{
		map[string]interface{}{"name": "nic-0", "subnet": map[string]interface{}{"ref": "prod-subnet-a", "kind": "Subnet"}},
	}}
	obs := Observation{Observed: []string{"/network_interfaces/{name=nic-0}/subnet"}}

	res := mustEvaluate(t, obj, obj, obs, CompilePlan(schema))
	keyed := schema.RekeyByFieldID(res)

	// element identity = element field id + element key
	if v := keyed["cic:compute:vm:subnet/{name=nic-0}"]; v != VerdictConformant {
		t.Fatalf("element field_id keying = %v (full: %v)", v, keyed)
	}
}

func TestRekeyByFieldID_FallbackToPath(t *testing.T) {
	// A field with no field_id is keyed by "path:<path>", never colliding with an id.
	schema := ParseSchema(map[string]interface{}{"region": map[string]interface{}{}})
	obj := map[string]interface{}{"region": "eu-1"}
	obs := Observation{Observed: []string{"/region"}}

	res := mustEvaluate(t, obj, obj, obs, CompilePlan(schema))
	keyed := schema.RekeyByFieldID(res)

	if keyed["path:/region"] != VerdictConformant {
		t.Fatalf("fallback keying = %v", keyed)
	}
}
