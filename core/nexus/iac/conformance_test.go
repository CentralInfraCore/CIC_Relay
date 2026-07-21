package iac

import "testing"

// The OCI vertical: a VM with 2 NICs into 2 subnets, provider-extended state,
// proving the object-level conformance model end-to-end.

func ociIntent() map[string]interface{} {
	return map[string]interface{}{
		"shape":     "VM.Standard.E4.Flex",
		"memory_gb": 16,
		"network_interfaces": []interface{}{
			map[string]interface{}{"name": "nic-0", "subnet": map[string]interface{}{"ref": "prod-subnet-a", "kind": "Subnet"}},
			map[string]interface{}{"name": "nic-1", "subnet": map[string]interface{}{"ref": "prod-subnet-b", "kind": "Subnet"}},
		},
	}
}

// ociObserved is the provider-extended state: same declared fields plus provider_id,
// lifecycle_state and per-NIC private_ip that exist only after creation.
func ociObserved() map[string]interface{} {
	return map[string]interface{}{
		"shape":           "VM.Standard.E4.Flex",
		"memory_gb":       16.0, // float from the provider JSON — numeric-equal to 16
		"provider_id":     "ocid1.instance.oc1..aaaa",
		"lifecycle_state": "RUNNING",
		"network_interfaces": []interface{}{
			map[string]interface{}{"name": "nic-0", "subnet": map[string]interface{}{"ref": "prod-subnet-a", "kind": "Subnet"}, "private_ip": "10.0.1.17"},
			map[string]interface{}{"name": "nic-1", "subnet": map[string]interface{}{"ref": "prod-subnet-b", "kind": "Subnet"}, "private_ip": "10.0.2.9"},
		},
	}
}

func ociPlan() ConformancePlan {
	return ConformancePlan{
		Scalars: []FieldPlan{
			{Path: "/shape", Compare: CompareExact},
			{Path: "/memory_gb", Compare: CompareNumeric},
		},
		Collections: []CollectionPlan{{
			Path:       "/network_interfaces",
			Collection: Collection{Topology: TopologyMap, Keys: []string{"name"}},
			Elements:   []FieldPlan{{Path: "subnet", Compare: CompareExact}},
		}},
	}
}

func ociObservation() Observation {
	return Observation{Observed: []string{
		"/shape",
		"/memory_gb",
		"/network_interfaces/{name=nic-0}/subnet",
		"/network_interfaces/{name=nic-1}/subnet",
	}}
}

func mustEvaluate(t *testing.T, intent, observed map[string]interface{}, obs Observation, plan ConformancePlan) ConformanceResult {
	t.Helper()
	res, err := Evaluate(intent, observed, obs, plan)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return res
}

func TestEvaluate_OCI_Conformant(t *testing.T) {
	res := mustEvaluate(t, ociIntent(), ociObserved(), ociObservation(), ociPlan())

	if res.Object != ObjConformant {
		t.Fatalf("object = %q, want CONFORMANT; fields=%v", res.Object, res.Fields)
	}
	for path, v := range res.Fields {
		if v != VerdictConformant {
			t.Errorf("field %s = %q, want CONFORMANT", path, v)
		}
	}
	// numeric normalization actually exercised
	if res.Fields["/memory_gb"] != VerdictConformant {
		t.Errorf("memory_gb 16 vs 16.0 should be CONFORMANT, got %q", res.Fields["/memory_gb"])
	}
	// map-collection element paths resolved by identity
	if _, ok := res.Fields["/network_interfaces/{name=nic-1}/subnet"]; !ok {
		t.Errorf("expected a verdict for nic-1 subnet; fields=%v", res.Fields)
	}
	// proof inputs present and deterministic
	if res.IntentDigest == "" || res.ObservationDigest == "" {
		t.Errorf("missing digests: %+v", res)
	}
	res2 := mustEvaluate(t, ociIntent(), ociObserved(), ociObservation(), ociPlan())
	if res.IntentDigest != res2.IntentDigest {
		t.Errorf("intent digest not deterministic: %s vs %s", res.IntentDigest, res2.IntentDigest)
	}
}

func TestEvaluate_OCI_ExtraStateFieldsAreNotDrift(t *testing.T) {
	res := mustEvaluate(t, ociIntent(), ociObserved(), ociObservation(), ociPlan())

	// provider_id / lifecycle_state / private_ip exist only in state; they must not
	// appear as verdicts and must not push the object off CONFORMANT.
	for _, stateOnly := range []string{"/provider_id", "/lifecycle_state", "/network_interfaces/{name=nic-0}/private_ip"} {
		if _, present := res.Fields[stateOnly]; present {
			t.Errorf("state-only field %s leaked into the verdict set", stateOnly)
		}
	}
	if res.Object != ObjConformant {
		t.Errorf("state-only fields turned the object non-conformant: %q", res.Object)
	}
}

func TestEvaluate_OCI_Drift(t *testing.T) {
	observed := ociObserved()
	// nic-1 landed in the wrong subnet.
	observed["network_interfaces"].([]interface{})[1].(map[string]interface{})["subnet"] =
		map[string]interface{}{"ref": "prod-subnet-WRONG", "kind": "Subnet"}

	res := mustEvaluate(t, ociIntent(), observed, ociObservation(), ociPlan())

	if got := res.Fields["/network_interfaces/{name=nic-1}/subnet"]; got != VerdictDrift {
		t.Errorf("nic-1 subnet = %q, want DRIFT", got)
	}
	if res.Object != ObjDrift {
		t.Errorf("object = %q, want DRIFT", res.Object)
	}
}

func TestEvaluate_OCI_NotComparable(t *testing.T) {
	observed := ociObserved()
	observed["memory_gb"] = "large" // provider returned a non-numeric under a numeric comparator

	res := mustEvaluate(t, ociIntent(), observed, ociObservation(), ociPlan())

	if got := res.Fields["/memory_gb"]; got != VerdictNotComparable {
		t.Errorf("memory_gb = %q, want NOT_COMPARABLE", got)
	}
	if res.Object != ObjDrift {
		t.Errorf("object = %q, want DRIFT (not-comparable is non-conformant)", res.Object)
	}
}

func TestEvaluate_OCI_Unobserved(t *testing.T) {
	// The observe never covered memory_gb.
	obs := Observation{Observed: []string{
		"/shape",
		"/network_interfaces/{name=nic-0}/subnet",
		"/network_interfaces/{name=nic-1}/subnet",
	}}

	res := mustEvaluate(t, ociIntent(), ociObserved(), obs, ociPlan())

	if got := res.Fields["/memory_gb"]; got != VerdictUnobserved {
		t.Errorf("memory_gb = %q, want UNOBSERVED", got)
	}
	if res.Object != ObjIncomplete {
		t.Errorf("object = %q, want INCOMPLETE (conformance can't be claimed for an unobserved field)", res.Object)
	}
}

func TestEvaluate_OCI_DesiredAbsentIsConformant(t *testing.T) {
	// A field the intent omits, authoritatively absent in state, under desired-absent
	// semantics: OBSERVED_ABSENT, and the object stays CONFORMANT.
	plan := ociPlan()
	plan.Scalars = append(plan.Scalars, FieldPlan{Path: "/boot_volume", Compare: CompareExact})

	obs := ociObservation()
	obs.AuthoritativeAbsent = []string{"/boot_volume"}

	res := mustEvaluate(t, ociIntent(), ociObserved(), obs, plan)

	if got := res.Fields["/boot_volume"]; got != VerdictObservedAbsent {
		t.Errorf("boot_volume = %q, want OBSERVED_ABSENT", got)
	}
	if res.Object != ObjConformant {
		t.Errorf("object = %q, want CONFORMANT (desired-absent, is absent)", res.Object)
	}
}

func TestEvaluate_OCI_SubnetIsRecognizedReference(t *testing.T) {
	// The vertical genuinely wires reference.go: a NIC's subnet is an object
	// reference (kind Subnet), not a scalar.
	nic0 := ociIntent()["network_interfaces"].([]interface{})[0].(map[string]interface{})
	ref, ok := ExpandField(nic0["subnet"]).AsRef()
	if !ok {
		t.Fatalf("nic-0 subnet not recognized as a reference")
	}
	if ref.Target != "prod-subnet-a" || ref.Kind != "Subnet" {
		t.Errorf("ref = %+v, want {prod-subnet-a Subnet}", ref)
	}
}
