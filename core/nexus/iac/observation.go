package iac

import "slices"

// Observation completeness (feature-011, iac-object-model.md, spec #4). A missing
// key in an observed state object cannot, by itself, distinguish "observed and
// genuinely absent" from "never observed" — provider APIs return different field
// sets per endpoint, and a module may not implement an observation at all. So the
// observation carries an explicit coverage envelope, and a verdict must never
// claim conformance for a field the observe never saw.

// CoverageState is how a field path was (or was not) covered by an observation.
type CoverageState string

const (
	// CoverageObserved — the field was observed; its value is authoritative.
	CoverageObserved CoverageState = "observed"
	// CoverageAbsent — the field was observed and is authoritatively not present.
	CoverageAbsent CoverageState = "absent"
	// CoverageUnobserved — the field was not covered by this observation.
	CoverageUnobserved CoverageState = "unobserved"
)

// Observation is the coverage envelope accompanying an observed state object: the
// field paths the observe actually covered. Paths are canonical (e.g.
// "/interfaces/{name=nic-0}/private_ip"). A path in neither set is unobserved. The
// envelope is itself hashable and signable, so coverage becomes proof.
type Observation struct {
	Observed            []string `json:"observed" yaml:"observed"`
	AuthoritativeAbsent []string `json:"authoritative_absent" yaml:"authoritative_absent"`
}

// Coverage classifies a field path against the observation envelope.
func (o Observation) Coverage(path string) CoverageState {
	if slices.Contains(o.AuthoritativeAbsent, path) {
		return CoverageAbsent
	}
	if slices.Contains(o.Observed, path) {
		return CoverageObserved
	}
	return CoverageUnobserved
}

// FieldVerdict is the per-field conformance verdict.
type FieldVerdict string

const (
	VerdictConformant     FieldVerdict = "CONFORMANT"
	VerdictDrift          FieldVerdict = "DRIFT"
	VerdictObservedAbsent FieldVerdict = "OBSERVED_ABSENT"
	VerdictUnobserved     FieldVerdict = "UNOBSERVED"
	// VerdictNotComparable is produced by the comparison layer (Task 1) when the
	// intent and observed values are not type-compatible; ClassifyField does not
	// emit it.
	VerdictNotComparable FieldVerdict = "NOT_COMPARABLE"
)

// ClassifyField produces a per-field verdict from the observation coverage of the
// path and the comparison outcome. `intentPresent` is whether the intent declares
// the field; `matched` is whether the observed value equals the intent (only
// meaningful when the field was observed).
//
//   - unobserved            → UNOBSERVED (conformance cannot be claimed)
//   - observed + matched     → CONFORMANT
//   - observed + not matched → DRIFT
//   - absent + intent wants it   → DRIFT (missing)
//   - absent + intent omits it   → OBSERVED_ABSENT (desired-absent, is absent)
func (o Observation) ClassifyField(path string, intentPresent, matched bool) FieldVerdict {
	switch o.Coverage(path) {
	case CoverageObserved:
		if matched {
			return VerdictConformant
		}
		return VerdictDrift
	case CoverageAbsent:
		if intentPresent {
			return VerdictDrift
		}
		return VerdictObservedAbsent
	default: // CoverageUnobserved
		return VerdictUnobserved
	}
}
