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
		"s":    "4", // a string stays a string
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
