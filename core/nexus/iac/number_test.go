package iac

import (
	"encoding/json"
	"testing"
)

// Canonical number vectors — the source of truth to mirror in the Rust test
// vectors (spec #1).
func TestCanonicalNumber(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{4, "4"},
		{int64(4), "4"},
		{4.0, "4"},
		{4.5, "4.5"},
		{json.Number("4"), "4"},
		{json.Number("4.0"), "4"},
		{json.Number("4e0"), "4"},
		{json.Number("4.50"), "4.5"},
		{json.Number("1e3"), "1000"},
		{"16", "16"},
		{-0.0, "0"},
		{json.Number("-0"), "0"},
		{json.Number("-0.0"), "0"},
		{-7, "-7"},
		// precision: beyond float64's 53-bit mantissa, kept exact
		{json.Number("9007199254740993"), "9007199254740993"},
		{int64(9007199254740993), "9007199254740993"},
	}
	for _, c := range cases {
		got, ok := canonicalNumber(c.in)
		if !ok {
			t.Errorf("canonicalNumber(%v): not numeric", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("canonicalNumber(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalNumber_NonNumeric(t *testing.T) {
	for _, v := range []interface{}{"abc", true, nil, map[string]interface{}{}} {
		if _, ok := canonicalNumber(v); ok {
			t.Errorf("canonicalNumber(%v) reported numeric", v)
		}
	}
}

func TestNormalizeNumbers_LeavesStringsAndStructure(t *testing.T) {
	in := map[string]interface{}{
		"n":    json.Number("4.0"),
		"s":    "4",       // a string stays a string
		"b":    true,
		"list": []interface{}{json.Number("1e1"), "x"},
	}
	out := normalizeNumbers(in).(map[string]interface{})

	if out["n"] != json.Number("4") {
		t.Errorf("n = %v, want json.Number(4)", out["n"])
	}
	if out["s"] != "4" {
		t.Errorf("string s must stay a string, got %T %v", out["s"], out["s"])
	}
	if out["b"] != true {
		t.Errorf("b = %v", out["b"])
	}
	list := out["list"].([]interface{})
	if list[0] != json.Number("10") || list[1] != "x" {
		t.Errorf("list = %v", list)
	}
}

func TestCanonicalNumber_IntegerKindsKeepFullRange(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{int8(-128), "-128"}, {int16(-32768), "-32768"}, {int32(-2147483648), "-2147483648"},
		{int64(-9223372036854775808), "-9223372036854775808"}, {int64(9223372036854775807), "9223372036854775807"},
		{uint(7), "7"}, {uint8(255), "255"}, {uint16(65535), "65535"}, {uint32(4294967295), "4294967295"},
		{uint64(18446744073709551615), "18446744073709551615"},
	}
	for _, c := range cases {
		got, ok := canonicalNumber(c.in)
		if !ok || got != c.want {
			t.Errorf("canonicalNumber(%T %v) = %q, %v; want %q", c.in, c.in, got, ok, c.want)
		}
	}
}

func TestCanonicalNumber_FloatsArePlainShortestDecimal(t *testing.T) {
	a, b := 0.1, 0.2 // variables: constant arithmetic would be exact and give 0.3
	cases := []struct {
		in   interface{}
		want string
	}{
		{float32(0.5), "0.5"},
		{float32(-1.5), "-1.5"},
		{1e21, "1000000000000000000000"}, // no exponent form
		{1.5e-7, "0.00000015"},
		{a + b, "0.30000000000000004"}, // shortest form that round-trips float64
		{1234567.0, "1234567"},
	}
	for _, c := range cases {
		got, ok := canonicalNumber(c.in)
		if !ok || got != c.want {
			t.Errorf("canonicalNumber(%T %v) = %q, %v; want %q", c.in, c.in, got, ok, c.want)
		}
	}
}

func TestCanonicalNumber_NumericStrings(t *testing.T) {
	good := map[string]string{
		" 42 ": "42", "+5": "5", "007": "7", "-0": "0", "-0.0": "0",
		"1e3": "1000", "2.50": "2.5", "-7": "-7",
		"123456789012345678901234567890": "123456789012345678901234567890", // beyond float64: exact
	}
	for in, want := range good {
		if got, ok := canonicalNumber(in); !ok || got != want {
			t.Errorf("canonicalNumber(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "   ", "abc", "1.2.3", "0x10", "1e400", "12abc"} {
		if got, ok := canonicalNumber(in); ok {
			t.Errorf("canonicalNumber(%q) = %q, want not numeric", in, got)
		}
	}
}
