package iac

import (
	"bytes"
	"encoding/json"
	"math/big"

	"centralrelay/pkg/canonicaljson"
)

// Intent↔state comparison — the conformance plan's first slice (feature-011,
// iac-object-model.md, "Task 1"). [Observation.ClassifyField] takes `matched` as a
// given; this is what computes it. A field's `behavior.compare` annotation
// (exact | numeric) selects a comparator, and the comparator answers two things:
// is the intent value comparable to the observed value at all, and — if so — do
// they match. The "not comparable" case is what lets a verdict be honestly
// NOT_COMPARABLE instead of a false DRIFT (thread: "not the same shape").
//
// This is deliberately the minimal slice: two comparators (exact, numeric), no
// map-subset / reference-resolution / set semantics yet, and no normalization
// beyond numeric equivalence. Those are later rounds of the same plan; the seam
// here ([Compare] + [Observation.ClassifyFieldValue]) is what they extend.

// CompareType is the semantic-equality rule for a field, sourced from the field's
// `behavior.compare.type`. Unknown/empty defaults to [CompareExact].
type CompareType string

const (
	// CompareExact — structural canonical equality: the two values match iff their
	// canonical-JSON encodings are identical. Values of different JSON kinds
	// (string vs object, number vs list, …) are NOT comparable.
	CompareExact CompareType = "exact"
	// CompareNumeric — numeric equality with representation normalization: 1, 1.0,
	// 1e0 and "1" are equal. Values that are not numeric on both sides are NOT
	// comparable.
	CompareNumeric CompareType = "numeric"
)

// Compare answers whether `observed` conforms to `intent` under the comparator
// `ct`. It returns:
//
//   - comparable — false when the two values are not type-compatible under `ct`
//     (the caller surfaces this as NOT_COMPARABLE, never as DRIFT);
//   - matched — meaningful only when comparable, true when the values are equal.
//
// An empty/unknown `ct` is treated as [CompareExact].
func Compare(intent, observed interface{}, ct CompareType) (matched, comparable bool) {
	switch ct {
	case CompareNumeric:
		in, iok := asRat(intent)
		ob, ook := asRat(observed)
		if !iok || !ook {
			return false, false
		}
		return in.Cmp(ob) == 0, true
	default: // CompareExact
		if jsonKind(intent) != jsonKind(observed) {
			return false, false
		}
		return canonicalEqual(intent, observed), true
	}
}

// ClassifyFieldValue is the full per-field verdict: it joins observation coverage
// (this method's receiver) with the value comparison. For an observed field it
// runs [Compare] and maps the result to CONFORMANT / DRIFT / NOT_COMPARABLE; for
// absent or unobserved fields it defers to [Observation.ClassifyField], where the
// value never matters.
//
// This is the seam the compiled conformance plan drives: one call per field path,
// carrying the field's comparator and both sides' values.
func (o Observation) ClassifyFieldValue(path string, intentPresent bool, intent, observed interface{}, ct CompareType) FieldVerdict {
	if o.Coverage(path) != CoverageObserved {
		// Absent / unobserved: the observed value is not authoritative, so the
		// comparison is irrelevant — coverage alone decides.
		return o.ClassifyField(path, intentPresent, false)
	}
	matched, comparable := Compare(intent, observed, ct)
	if !comparable {
		return VerdictNotComparable
	}
	return o.ClassifyField(path, intentPresent, matched)
}

// canonicalEqual reports whether two values have identical canonical-JSON forms.
func canonicalEqual(a, b interface{}) bool {
	ab, err := canonicaljson.ToJSON(a)
	if err != nil {
		return false
	}
	bb, err := canonicaljson.ToJSON(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// jsonKind names the JSON kind of a value for exact type-compatibility: one of
// "null", "bool", "number", "string", "array", "object", or "" (unknown). All
// integer and floating kinds collapse to "number".
func jsonKind(v interface{}) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return "number"
	case string:
		return "string"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return ""
	}
}

// asRat coerces a numeric value (or a numeric string / json.Number) to a big.Rat
// for representation-independent equality. bool is deliberately not numeric.
func asRat(v interface{}) (*big.Rat, bool) {
	switch n := v.(type) {
	case int:
		return new(big.Rat).SetInt64(int64(n)), true
	case int8:
		return new(big.Rat).SetInt64(int64(n)), true
	case int16:
		return new(big.Rat).SetInt64(int64(n)), true
	case int32:
		return new(big.Rat).SetInt64(int64(n)), true
	case int64:
		return new(big.Rat).SetInt64(n), true
	case uint, uint8, uint16, uint32, uint64:
		return new(big.Rat).SetUint64(reflectUint(n)), true
	case float32:
		r := new(big.Rat)
		if r.SetFloat64(float64(n)) == nil { // nil for Inf/NaN
			return nil, false
		}
		return r, true
	case float64:
		r := new(big.Rat)
		if r.SetFloat64(n) == nil { // nil for Inf/NaN
			return nil, false
		}
		return r, true
	case json.Number:
		return ratFromString(n.String())
	case string:
		return ratFromString(n)
	default:
		return nil, false
	}
}

// ratFromString parses a decimal or scientific-notation string into a big.Rat.
func ratFromString(s string) (*big.Rat, bool) {
	r, ok := new(big.Rat).SetString(s)
	return r, ok
}

// reflectUint widens any unsigned integer kind to uint64.
func reflectUint(v interface{}) uint64 {
	switch n := v.(type) {
	case uint:
		return uint64(n)
	case uint8:
		return uint64(n)
	case uint16:
		return uint64(n)
	case uint32:
		return uint64(n)
	case uint64:
		return n
	default:
		return 0
	}
}
