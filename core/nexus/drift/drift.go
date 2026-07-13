// Package drift classifies the difference between a declared (desired) state and
// the observed (actual) state — the CIC "Declarative Accountability" primitive
// (demo §8.2–§8.3). The classification is deterministic (a `ProofTrace`
// prerequisite): the same desired/actual pair always yields the same type and
// the same, sorted, difference report.
package drift

import (
	"reflect"
	"sort"
)

// Type is the classification of a desired-vs-actual difference.
type Type string

const (
	// None: the actual state matches the desired state exactly.
	None Type = "NONE"
	// SoftDrift: every desired key is present with the desired value, but the
	// actual state carries extra state beyond it — reconcilable by pruning.
	SoftDrift Type = "SOFT_DRIFT"
	// ReconciliableDrift: the desired state is not fully reflected (a desired key
	// is missing, or holds a different value) — reconcilable by re-applying.
	ReconciliableDrift Type = "RECONCILIABLE_DRIFT"
	// HardDrift: there is no observable actual state (the infrastructure is gone).
	// The physical state cannot be derived from the last valid ProofTrace.
	HardDrift Type = "HARD_DRIFT"
)

// Report is the deterministic result of comparing a desired state against an
// actual one: the drift Type plus the exact keys that differ.
type Report struct {
	Type Type `json:"type"`
	// Missing lists desired keys absent from the actual state (sorted).
	Missing []string `json:"missing,omitempty"`
	// Changed lists desired keys present in actual but with a different value (sorted).
	Changed []string `json:"changed,omitempty"`
	// Extra lists actual keys not declared in the desired state (sorted).
	Extra []string `json:"extra,omitempty"`
}

// Analyze compares a desired state against an actual state and returns the
// deterministic drift Report. Both are canonicalizable maps (e.g. decoded JSON).
//
// An empty or nil actual state is HARD_DRIFT (the infrastructure is gone),
// regardless of desired. Otherwise: any missing or changed desired key makes it
// RECONCILIABLE_DRIFT; if the desired state is fully satisfied but the actual has
// extra keys, it is SOFT_DRIFT; an exact match is NONE.
func Analyze(desired, actual map[string]interface{}) Report {
	if len(actual) == 0 {
		return Report{Type: HardDrift}
	}

	var r Report
	for k, dv := range desired {
		av, ok := actual[k]
		if !ok {
			r.Missing = append(r.Missing, k)
			continue
		}
		if !reflect.DeepEqual(dv, av) {
			r.Changed = append(r.Changed, k)
		}
	}
	for k := range actual {
		if _, ok := desired[k]; !ok {
			r.Extra = append(r.Extra, k)
		}
	}
	sort.Strings(r.Missing)
	sort.Strings(r.Changed)
	sort.Strings(r.Extra)

	switch {
	case len(r.Missing) > 0 || len(r.Changed) > 0:
		r.Type = ReconciliableDrift
	case len(r.Extra) > 0:
		r.Type = SoftDrift
	default:
		r.Type = None
	}
	return r
}

// Classify is a convenience over Analyze returning only the drift Type.
func Classify(desired, actual map[string]interface{}) Type {
	return Analyze(desired, actual).Type
}
