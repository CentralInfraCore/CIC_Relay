package iac

import (
	"encoding/json"
	"testing"
)

func TestCompare_Exact(t *testing.T) {
	cases := []struct {
		name             string
		intent, observed interface{}
		wantMatched      bool
		wantComparable   bool
	}{
		{"equal strings", "prod", "prod", true, true},
		{"different strings", "prod", "dev", false, true},
		{"equal ints", 16, 16, true, true},
		{"different ints", 16, 32, false, true},
		{"equal maps", map[string]interface{}{"a": "x"}, map[string]interface{}{"a": "x"}, true, true},
		{"different maps", map[string]interface{}{"a": "x"}, map[string]interface{}{"a": "y"}, false, true},
		{"string vs number → not comparable", "16", 16, false, false},
		{"number vs object → not comparable", 16, map[string]interface{}{}, false, false},
		{"null vs string → not comparable", nil, "x", false, false},
		{"equal nulls", nil, nil, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, comp := Compare(c.intent, c.observed, CompareExact)
			if comp != c.wantComparable {
				t.Fatalf("comparable = %v, want %v", comp, c.wantComparable)
			}
			if comp && m != c.wantMatched {
				t.Fatalf("matched = %v, want %v", m, c.wantMatched)
			}
		})
	}
}

func TestCompare_Numeric(t *testing.T) {
	cases := []struct {
		name             string
		intent, observed interface{}
		wantMatched      bool
		wantComparable   bool
	}{
		{"int vs float same value", 1, 1.0, true, true},
		{"int vs numeric string", 1, "1", true, true},
		{"float vs scientific string", 100.0, "1e2", true, true},
		{"decimal string equality", "1.5", 1.5, true, true},
		{"json.Number", json.Number("42"), 42, true, true},
		{"different numbers", 1, 2, false, true},
		{"non-numeric string → not comparable", "abc", 1, false, false},
		{"bool is not numeric", true, 1, false, false},
		{"object → not comparable", map[string]interface{}{}, 1, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, comp := Compare(c.intent, c.observed, CompareNumeric)
			if comp != c.wantComparable {
				t.Fatalf("comparable = %v, want %v", comp, c.wantComparable)
			}
			if comp && m != c.wantMatched {
				t.Fatalf("matched = %v, want %v", m, c.wantMatched)
			}
		})
	}
}

func TestCompare_EmptyTypeDefaultsToExact(t *testing.T) {
	// Unknown/empty comparator must behave as exact, not panic or mis-match.
	m, comp := Compare("x", "x", CompareType(""))
	if !comp || !m {
		t.Fatalf("empty type: matched=%v comparable=%v, want true/true", m, comp)
	}
	if _, comp := Compare("1", 1, CompareType("")); comp {
		t.Fatalf("empty type: string vs number should be not comparable (exact semantics)")
	}
}

func TestClassifyFieldValue(t *testing.T) {
	const p = "/config/shape"
	cases := []struct {
		name             string
		obs              Observation
		intentPresent    bool
		intent, observed interface{}
		ct               CompareType
		want             FieldVerdict
	}{
		{
			name: "observed + matched → CONFORMANT",
			obs:  Observation{Observed: []string{p}},
			intent: "E4.Flex", observed: "E4.Flex", ct: CompareExact,
			intentPresent: true, want: VerdictConformant,
		},
		{
			name: "observed + mismatch → DRIFT",
			obs:  Observation{Observed: []string{p}},
			intent: "E4.Flex", observed: "E3.Flex", ct: CompareExact,
			intentPresent: true, want: VerdictDrift,
		},
		{
			name: "observed + not comparable → NOT_COMPARABLE",
			obs:  Observation{Observed: []string{p}},
			intent: "16", observed: 16, ct: CompareExact,
			intentPresent: true, want: VerdictNotComparable,
		},
		{
			name: "numeric normalization → CONFORMANT",
			obs:  Observation{Observed: []string{p}},
			intent: 16, observed: 16.0, ct: CompareNumeric,
			intentPresent: true, want: VerdictConformant,
		},
		{
			name:          "unobserved → UNOBSERVED (value ignored)",
			obs:           Observation{},
			intent:        "E4.Flex",
			observed:      "anything",
			ct:            CompareExact,
			intentPresent: true, want: VerdictUnobserved,
		},
		{
			name:          "authoritative absent + intent wants it → DRIFT",
			obs:           Observation{AuthoritativeAbsent: []string{p}},
			intentPresent: true, want: VerdictDrift,
		},
		{
			name:          "authoritative absent + intent omits it → OBSERVED_ABSENT",
			obs:           Observation{AuthoritativeAbsent: []string{p}},
			intentPresent: false, want: VerdictObservedAbsent,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.obs.ClassifyFieldValue(p, c.intentPresent, c.intent, c.observed, c.ct)
			if got != c.want {
				t.Fatalf("verdict = %q, want %q", got, c.want)
			}
		})
	}
}
