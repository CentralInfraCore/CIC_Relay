// Package drift classifies the difference between a declared (desired) state and
// the observed (actual) state — the CIC "Declarative Accountability" primitive
// (demo §8.2–§8.3). The classification is deterministic (a `ProofTrace`
// prerequisite): the same desired/actual pair always yields the same type and
// the same, sorted, difference report.
//
// Classification is delegated to the feature-011 conformance model
// (core/nexus/iac): each desired key becomes an exact-compared field, `actual` is
// treated as a complete observation, and the per-field verdicts are folded back
// into this package's Report. This is the "rewire the drift engine onto the new
// model" step — the bridge the object model was missing. Two consequences follow
// directly from that model (both intentional corrections of the old reflect-based
// engine):
//
//   - Extra keys in the observed state are observed state, not drift. A desired
//     state that is fully satisfied is NONE even when actual carries more (a
//     provider_id, an assigned IP, a timestamp). SOFT_DRIFT is reserved for when a
//     schema/plan says an extra field must not exist — not emitted by the default,
//     schema-less classification here.
//   - Empty desired + empty actual is NONE, not HARD_DRIFT: nothing is declared
//     and nothing is there.
package drift

import (
	"sort"

	"centralrelay/core/nexus/iac"
)

// Type is the classification of a desired-vs-actual difference.
type Type string

const (
	// None: the actual state satisfies the desired state (extra observed state is
	// not counted against it).
	None Type = "NONE"
	// SoftDrift: the desired state is satisfied but a schema/plan forbids an extra
	// field that is present — reconcilable by pruning. Requires a plan that marks
	// the field forbidden; the default schema-less classification never emits it.
	SoftDrift Type = "SOFT_DRIFT"
	// ReconciliableDrift: the desired state is not fully reflected (a desired key
	// is missing, or holds a different/incomparable value) — reconcilable by
	// re-applying.
	ReconciliableDrift Type = "RECONCILIABLE_DRIFT"
	// HardDrift: a state was declared but nothing is observed (the infrastructure
	// is gone). The physical state cannot be derived from the last valid ProofTrace.
	HardDrift Type = "HARD_DRIFT"
)

// Report is the deterministic result of comparing a desired state against an
// actual one: the drift Type plus the exact keys that differ.
type Report struct {
	Type Type `json:"type"`
	// Missing lists desired keys absent from the actual state (sorted).
	Missing []string `json:"missing,omitempty"`
	// Changed lists desired keys present in actual but with a different or
	// incomparable value (sorted).
	Changed []string `json:"changed,omitempty"`
	// Extra lists actual keys not declared in the desired state (sorted). Reported
	// for visibility; not counted as drift by the default classification.
	Extra []string `json:"extra,omitempty"`
}

// Analyze compares a desired state against an actual state and returns the
// deterministic drift Report, classified through the iac conformance model. Both
// are canonicalizable maps (e.g. decoded JSON).
//
// An empty actual against a non-empty desired is HARD_DRIFT (the infrastructure is
// gone). Empty against empty is NONE. Otherwise each desired key is compared
// (exact) against the observed value: a missing or mismatched key makes it
// RECONCILIABLE_DRIFT; a fully-satisfied desired state is NONE regardless of extra
// observed keys.
func Analyze(desired, actual map[string]interface{}) Report {
	if len(actual) == 0 {
		if len(desired) == 0 {
			return Report{Type: None}
		}
		return Report{Type: HardDrift}
	}

	// Compile a default plan + observation from the flat maps: every desired key is
	// an exact-compared scalar (numeric/semantic comparators need schema behavior
	// annotations, absent here); `actual` is a complete observation — its keys are
	// observed, and desired keys missing from it are authoritatively absent.
	plan := iac.ConformancePlan{Scalars: make([]iac.FieldPlan, 0, len(desired))}
	obs := iac.Observation{Observed: make([]string, 0, len(actual))}
	for k := range desired {
		plan.Scalars = append(plan.Scalars, iac.FieldPlan{Path: "/" + k, Compare: iac.CompareExact})
		if _, ok := actual[k]; !ok {
			obs.AuthoritativeAbsent = append(obs.AuthoritativeAbsent, "/"+k)
		}
	}
	for k := range actual {
		obs.Observed = append(obs.Observed, "/"+k)
	}

	res, err := iac.Evaluate(desired, actual, obs, plan)
	if err != nil {
		// A canonicalization failure is not a drift signal; do not fabricate a
		// verdict from it.
		return Report{Type: None}
	}

	var r Report
	for k := range desired {
		switch res.Fields["/"+k] {
		case iac.VerdictDrift, iac.VerdictNotComparable:
			if _, present := actual[k]; present {
				r.Changed = append(r.Changed, k)
			} else {
				r.Missing = append(r.Missing, k)
			}
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

	if len(r.Missing) > 0 || len(r.Changed) > 0 {
		r.Type = ReconciliableDrift
	} else {
		r.Type = None // desired satisfied; extra observed keys are not drift
	}
	return r
}

// Classify is a convenience over Analyze returning only the drift Type.
func Classify(desired, actual map[string]interface{}) Type {
	return Analyze(desired, actual).Type
}
