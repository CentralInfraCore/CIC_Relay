package iac

import (
	"encoding/json"
	"math"
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
		{"int vs float same value", 16, 16.0, true, true},
		{"int vs json.Number same value", 16, json.Number("16.0"), true, true},
		{"json.Number 4e0 vs int", json.Number("4e0"), 4, true, true},
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
			name:   "observed + matched → CONFORMANT",
			obs:    Observation{Observed: []string{p}},
			intent: "E4.Flex", observed: "E4.Flex", ct: CompareExact,
			intentPresent: true, want: VerdictConformant,
		},
		{
			name:   "observed + mismatch → DRIFT",
			obs:    Observation{Observed: []string{p}},
			intent: "E4.Flex", observed: "E3.Flex", ct: CompareExact,
			intentPresent: true, want: VerdictDrift,
		},
		{
			name:   "observed + not comparable → NOT_COMPARABLE",
			obs:    Observation{Observed: []string{p}},
			intent: "16", observed: 16, ct: CompareExact,
			intentPresent: true, want: VerdictNotComparable,
		},
		{
			name:   "numeric normalization → CONFORMANT",
			obs:    Observation{Observed: []string{p}},
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

// The same quantity must compare equal whatever Go type it arrived as.
func TestCompare_Numeric_EveryNumericKindAgrees(t *testing.T) {
	five := []interface{}{
		int(5), int8(5), int16(5), int32(5), int64(5),
		uint(5), uint8(5), uint16(5), uint32(5), uint64(5),
		float32(5), float64(5), json.Number("5"), json.Number("5.0"), "5", "5e0",
	}
	for _, a := range five {
		for _, b := range five {
			m, comp := Compare(a, b, CompareNumeric)
			if !comp || !m {
				t.Errorf("Compare(%T %v, %T %v, numeric) = matched %v, comparable %v; want true/true", a, a, b, b, m, comp)
			}
		}
	}
}

func TestCompare_Numeric_DistinguishesNearbyValues(t *testing.T) {
	cases := []struct {
		name string
		a, b interface{}
	}{
		{"uint8 vs int", uint8(5), 6},
		{"uint64 max vs int64 max", uint64(18446744073709551615), int64(9223372036854775807)},
		{"uint64 max vs the same digits as a string plus one", uint64(18446744073709551615), "18446744073709551616"},
		{"float32 vs float64 decimal", float32(0.5), 0.75},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, comp := Compare(c.a, c.b, CompareNumeric)
			if !comp || m {
				t.Errorf("matched %v, comparable %v; want false/true", m, comp)
			}
		})
	}
	if m, comp := Compare(uint64(18446744073709551615), "18446744073709551615", CompareNumeric); !comp || !m {
		t.Errorf("uint64 max vs its decimal string must match exactly (no float64 rounding): %v/%v", m, comp)
	}
}

// NaN and the infinities have no rational value: under the numeric comparator they
// are not comparable (surfaced as NOT_COMPARABLE), never a spurious match or drift.
func TestCompare_Numeric_NonFiniteIsNotComparable(t *testing.T) {
	for _, v := range []interface{}{math.NaN(), math.Inf(1), math.Inf(-1), float32(math.NaN()), float32(math.Inf(1))} {
		if _, comp := Compare(v, 1, CompareNumeric); comp {
			t.Errorf("Compare(%v, 1, numeric) reported comparable", v)
		}
		if _, comp := Compare(1, v, CompareNumeric); comp {
			t.Errorf("Compare(1, %v, numeric) reported comparable", v)
		}
	}
}

func TestCompare_Exact_NumbersByCanonicalValue(t *testing.T) {
	for _, b := range []interface{}{4, int8(4), uint16(4), 4.0, float32(4), json.Number("4.0"), json.Number("4e0")} {
		if m, comp := Compare(4, b, CompareExact); !comp || !m {
			t.Errorf("Compare(4, %T %v, exact) = %v/%v; want true/true", b, b, m, comp)
		}
	}
	if m, comp := Compare(4, 5, CompareExact); !comp || m {
		t.Errorf("Compare(4, 5, exact) = %v/%v; want false/true", m, comp)
	}
	// number vs non-number is not comparable, in either direction
	if _, comp := Compare(4, "4", CompareExact); comp {
		t.Errorf("number vs string must be not comparable under exact")
	}
	if _, comp := Compare("4", 4, CompareExact); comp {
		t.Errorf("string vs number must be not comparable under exact")
	}
}

func TestCompare_Exact_StructuredValues(t *testing.T) {
	cases := []struct {
		name           string
		a, b           interface{}
		wantMatched    bool
		wantComparable bool
	}{
		{"null vs null", nil, nil, true, true},
		{"null vs string", nil, "x", false, false},
		{"bool equal", true, true, true, true},
		{"bool differ", true, false, false, true},
		{"bool vs string", true, "true", false, false},
		{"list equal", []interface{}{"a", 1}, []interface{}{"a", 1.0}, true, true},
		{"list order matters", []interface{}{"a", "b"}, []interface{}{"b", "a"}, false, true},
		{"object key order is irrelevant", map[string]interface{}{"x": 1, "y": 2}, map[string]interface{}{"y": 2, "x": 1}, true, true},
		{"object value differs", map[string]interface{}{"x": 1}, map[string]interface{}{"x": 2}, false, true},
		{"list vs object", []interface{}{}, map[string]interface{}{}, false, false},
		{"string vs list", "a", []interface{}{"a"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, comp := Compare(c.a, c.b, CompareExact)
			if comp != c.wantComparable || (comp && m != c.wantMatched) {
				t.Errorf("Compare = matched %v, comparable %v; want %v/%v", m, comp, c.wantMatched, c.wantComparable)
			}
		})
	}
}

func TestJSONKind(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{nil, "null"}, {true, "bool"}, {"s", "string"},
		{1, "number"}, {int8(1), "number"}, {uint64(1), "number"}, {float32(1), "number"}, {1.5, "number"}, {json.Number("1"), "number"},
		{[]interface{}{}, "array"}, {map[string]interface{}{}, "object"},
		{struct{}{}, ""}, {make(chan int), ""}, {[]string{"a"}, ""},
	}
	for _, c := range cases {
		if got := jsonKind(c.in); got != c.want {
			t.Errorf("jsonKind(%T) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalEqual_UnserializableNeverMatches(t *testing.T) {
	ch := make(chan int)
	if canonicalEqual(ch, ch) {
		t.Errorf("a value that cannot be encoded must not be reported equal, even to itself")
	}
	if canonicalEqual(map[string]interface{}{}, ch) || canonicalEqual(ch, map[string]interface{}{}) {
		t.Errorf("an unencodable value must not equal an encodable one")
	}
	if !canonicalEqual(map[string]interface{}{"a": 1, "b": []interface{}{1, "x"}}, map[string]interface{}{"b": []interface{}{1, "x"}, "a": 1}) {
		t.Errorf("equal structures with different key order must be equal")
	}
}
