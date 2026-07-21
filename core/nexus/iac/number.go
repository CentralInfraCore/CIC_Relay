package iac

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// Number normalization — the first, load-bearing piece of CIC Canonical Object
// Encoding v1 (feature-011, iac-object-model.md, spec #1). Representation must not
// change identity: `4`, `4.0`, `4e0` and (as an authored literal) `"4"` are the
// same number, `-0` is `0`, and `4.50` is `4.5`. Two guarantees follow:
//
//   - the digest is stable across those forms (the determinism contract), and —
//     crucially — precision-safe: an integer beyond float64's 53-bit mantissa
//     digests as its exact digits, not the nearest double (the old digest collapsed
//     everything through float64 and silently lost large integers);
//   - the `exact` comparator compares numbers by value, so it no longer depends on
//     the incidental Go type (int vs float64 vs json.Number) a value arrived as.
//
// Rust test vectors mirror these (spec #1's language-neutral requirement) — see the
// Go vectors in number_test.go as the source of truth to port.

// canonicalNumber returns the canonical decimal form of a numeric value and whether
// v was numeric. Integer types (and integer literals) keep full precision via
// big.Int; floats and non-integer literals normalize through float64's shortest
// round-trip form. Plain strings are accepted here (for numeric comparison), but
// [normalizeNumbers] does not treat them as numbers.
func canonicalNumber(v interface{}) (string, bool) {
	switch n := v.(type) {
	case int:
		return strconv.FormatInt(int64(n), 10), true
	case int8:
		return strconv.FormatInt(int64(n), 10), true
	case int16:
		return strconv.FormatInt(int64(n), 10), true
	case int32:
		return strconv.FormatInt(int64(n), 10), true
	case int64:
		return strconv.FormatInt(n, 10), true
	case uint:
		return strconv.FormatUint(uint64(n), 10), true
	case uint8:
		return strconv.FormatUint(uint64(n), 10), true
	case uint16:
		return strconv.FormatUint(uint64(n), 10), true
	case uint32:
		return strconv.FormatUint(uint64(n), 10), true
	case uint64:
		return strconv.FormatUint(n, 10), true
	case float32:
		return canonicalFloat(float64(n)), true
	case float64:
		return canonicalFloat(n), true
	case json.Number:
		return canonicalNumericString(string(n))
	case string:
		return canonicalNumericString(n)
	default:
		return "", false
	}
}

// canonicalFloat renders a float in plain (non-exponent) decimal, shortest form,
// mapping negative zero to "0".
func canonicalFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if s == "-0" {
		return "0"
	}
	return s
}

// canonicalNumericString canonicalizes a numeric literal. Integer literals keep
// full precision (big.Int); decimal/scientific literals normalize through float64.
// Returns false if s is not a number.
func canonicalNumericString(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if i, ok := new(big.Int).SetString(s, 10); ok {
		return i.String(), true // exact digits; big.Int normalizes -0 and leading +
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return "", false
	}
	return canonicalFloat(f), true
}

// isNumeric reports whether v is a numeric Go type (not a numeric string).
func isNumeric(v interface{}) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return true
	}
	return false
}

// normalizeNumbers deep-copies a decoded value tree, rewriting every numeric leaf
// to its canonical form (as a json.Number so it stays a number, not a string).
// Strings, bools and null are left untouched — a JSON string "4" stays distinct
// from the number 4.
func normalizeNumbers(v interface{}) interface{} {
	switch vv := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(vv))
		for k, e := range vv {
			out[k] = normalizeNumbers(e)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(vv))
		for i, e := range vv {
			out[i] = normalizeNumbers(e)
		}
		return out
	default:
		if isNumeric(vv) {
			if tok, ok := canonicalNumber(vv); ok {
				return json.Number(tok)
			}
		}
		return vv
	}
}
