package drift

import (
	"reflect"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name            string
		desired, actual map[string]interface{}
		want            Type
	}{
		{
			name:    "exact match is NONE",
			desired: map[string]interface{}{"network": "10.0.0.0/24", "vms": "1"},
			actual:  map[string]interface{}{"network": "10.0.0.0/24", "vms": "1"},
			want:    None,
		},
		{
			// The old engine called any extra actual key SOFT_DRIFT; under the
			// conformance model an unplanned observed key is observed state, not drift.
			name:    "extra actual key is not drift",
			desired: map[string]interface{}{"network": "10.0.0.0/24"},
			actual:  map[string]interface{}{"network": "10.0.0.0/24", "vms": "3"},
			want:    None,
		},
		{
			name:    "changed desired value is RECONCILIABLE_DRIFT",
			desired: map[string]interface{}{"network": "10.0.0.0/24"},
			actual:  map[string]interface{}{"network": "10.0.1.0/24"},
			want:    ReconciliableDrift,
		},
		{
			name:    "missing desired key is RECONCILIABLE_DRIFT",
			desired: map[string]interface{}{"network": "10.0.0.0/24", "vms": "1"},
			actual:  map[string]interface{}{"vms": "1"},
			want:    ReconciliableDrift,
		},
		{
			name:    "nil actual is HARD_DRIFT",
			desired: map[string]interface{}{"network": "10.0.0.0/24"},
			actual:  nil,
			want:    HardDrift,
		},
		{
			name:    "empty actual is HARD_DRIFT",
			desired: map[string]interface{}{"network": "10.0.0.0/24"},
			actual:  map[string]interface{}{},
			want:    HardDrift,
		},
		{
			// Nothing declared and nothing observed: not hard drift (there is no
			// declared state to be "gone").
			name:    "both empty is NONE",
			desired: map[string]interface{}{},
			actual:  map[string]interface{}{},
			want:    None,
		},
		{
			// Nothing declared, so unplanned observed keys are not drift.
			name:    "no desire but actual present is not drift",
			desired: map[string]interface{}{},
			actual:  map[string]interface{}{"unexpected": "x"},
			want:    None,
		},
		{
			name:    "changed takes precedence over extra",
			desired: map[string]interface{}{"a": "1"},
			actual:  map[string]interface{}{"a": "2", "b": "extra"},
			want:    ReconciliableDrift,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.desired, tc.actual); got != tc.want {
				t.Fatalf("Classify: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAnalyze_ReportDetails(t *testing.T) {
	r := Analyze(
		map[string]interface{}{"keep": "1", "change": "old", "gone": "x"},
		map[string]interface{}{"keep": "1", "change": "new", "added": "y"},
	)
	if r.Type != ReconciliableDrift {
		t.Fatalf("type: got %q", r.Type)
	}
	if !reflect.DeepEqual(r.Missing, []string{"gone"}) {
		t.Errorf("missing: got %v", r.Missing)
	}
	if !reflect.DeepEqual(r.Changed, []string{"change"}) {
		t.Errorf("changed: got %v", r.Changed)
	}
	if !reflect.DeepEqual(r.Extra, []string{"added"}) {
		t.Errorf("extra: got %v", r.Extra)
	}
}

func TestAnalyze_Deterministic(t *testing.T) {
	desired := map[string]interface{}{"z": "1", "a": "old", "m": "keep"}
	actual := map[string]interface{}{"m": "keep", "b": "x", "a": "changed"}
	first := Analyze(desired, actual)
	for i := 0; i < 20; i++ {
		if !reflect.DeepEqual(Analyze(desired, actual), first) {
			t.Fatal("Analyze is not deterministic")
		}
	}
	// Sorted, so order-independent maps still give sorted slices.
	if !reflect.DeepEqual(first.Missing, []string{"z"}) || !reflect.DeepEqual(first.Changed, []string{"a"}) || !reflect.DeepEqual(first.Extra, []string{"b"}) {
		t.Fatalf("report: %+v", first)
	}
}
