package iac

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"centralrelay/pkg/canonicaljson"
)

// Object-level conformance — the OCI vertical slice (feature-011,
// iac-object-model.md, "prove the model on one OCI vertical"). This is where the
// per-field pieces finally drive a live verdict: [Observation] coverage +
// [Compare]/[Observation.ClassifyFieldValue] over a whole object, walking scalars
// and one map-topology [Collection] of nested elements, and aggregating into a
// single object verdict plus a proof (intent + observation digests).
//
// The load-bearing facts it proves end-to-end:
//   - intent and state are NOT the same shape — a state-only field (provider_id,
//     private_ip, lifecycle_state) is simply absent from the plan, so it is never
//     drift (the thread's first drift-classification fix);
//   - a field the observation never saw yields UNOBSERVED, never a false
//     CONFORMANT (spec #4);
//   - numeric normalization (16 vs 16.0), reference equality (subnet refs), and
//     map-collection element identity ({name=nic-0}) all resolve correctly.
//
// The plan here is hand-built; compiling it from the schema's behavior annotations
// is the next round. The [Evaluate] signature is the seam that compiler targets.

// FieldPlan binds one comparable field path to its comparator. Path is canonical
// ("/shape", or relative within a collection element like "subnet").
type FieldPlan struct {
	Path    string
	Compare CompareType
}

// CollectionPlan is a map-topology list within the object: its container path, the
// topology that identifies elements, and the per-element field plans (paths
// relative to the element).
type CollectionPlan struct {
	Path       string
	Collection Collection
	Elements   []FieldPlan
}

// ConformancePlan is the compiled comparison plan for an object: flat scalar fields
// plus map-topology collections. (Compiled from schema behavior annotations in a
// later round; hand-built for the vertical slice.)
type ConformancePlan struct {
	Scalars     []FieldPlan
	Collections []CollectionPlan
}

// ObjectConformance is the aggregate object verdict.
type ObjectConformance string

const (
	// ObjConformant — every planned field is CONFORMANT or OBSERVED_ABSENT.
	ObjConformant ObjectConformance = "CONFORMANT"
	// ObjDrift — at least one field is DRIFT or NOT_COMPARABLE.
	ObjDrift ObjectConformance = "DRIFT"
	// ObjIncomplete — no drift, but at least one planned field was UNOBSERVED, so
	// full conformance cannot be claimed.
	ObjIncomplete ObjectConformance = "INCOMPLETE"
)

// ConformanceResult is the object verdict plus its proof inputs.
type ConformanceResult struct {
	Object            ObjectConformance       `json:"object"`
	Fields            map[string]FieldVerdict `json:"fields"`
	IntentDigest      string                  `json:"intent_digest"`
	ObservationDigest string                  `json:"observation_digest"`
}

// Evaluate produces the object-level conformance verdict for an intent (config)
// object against an observed (state) object, under the observation coverage and
// the compiled plan. Only planned fields are compared; unplanned state fields are
// intentionally ignored (they are observed state, not drift).
func Evaluate(intent, observed map[string]interface{}, obs Observation, plan ConformancePlan) (ConformanceResult, error) {
	fields := make(map[string]FieldVerdict)

	// Scalars.
	for _, fp := range plan.Scalars {
		iv, iPresent := resolvePath(intent, fp.Path)
		ov, _ := resolvePath(observed, fp.Path)
		fields[fp.Path] = obs.ClassifyFieldValue(fp.Path, iPresent, iv, ov, fp.Compare)
	}

	// Map-topology collections: enumerate element identities from both sides, then
	// classify each element field at its canonical path.
	for _, cp := range plan.Collections {
		for _, ek := range elementKeys(intent, observed, cp) {
			for _, ef := range cp.Elements {
				path := cp.Path + "/{" + ek + "}/" + ef.Path
				iv, iPresent := resolvePath(intent, path)
				ov, _ := resolvePath(observed, path)
				fields[path] = obs.ClassifyFieldValue(path, iPresent, iv, ov, ef.Compare)
			}
		}
	}

	intentDigest, err := SpecDigest(intent)
	if err != nil {
		return ConformanceResult{}, err
	}
	obsDigest, err := observationDigest(obs)
	if err != nil {
		return ConformanceResult{}, err
	}

	return ConformanceResult{
		Object:            aggregate(fields),
		Fields:            fields,
		IntentDigest:      intentDigest,
		ObservationDigest: obsDigest,
	}, nil
}

// aggregate reduces per-field verdicts to the object verdict. Precedence:
// DRIFT/NOT_COMPARABLE > UNOBSERVED > CONFORMANT.
func aggregate(fields map[string]FieldVerdict) ObjectConformance {
	hasDrift, hasUnobserved := false, false
	for _, v := range fields {
		switch v {
		case VerdictDrift, VerdictNotComparable:
			hasDrift = true
		case VerdictUnobserved:
			hasUnobserved = true
		}
	}
	switch {
	case hasDrift:
		return ObjDrift
	case hasUnobserved:
		return ObjIncomplete
	default:
		return ObjConformant
	}
}

// elementKeys returns the sorted union of element identities of a collection across
// the intent and observed lists, keyed by the collection's topology.
func elementKeys(intent, observed map[string]interface{}, cp CollectionPlan) []string {
	seen := map[string]struct{}{}
	for _, root := range []map[string]interface{}{intent, observed} {
		if v, ok := resolvePath(root, cp.Path); ok {
			if list, ok := v.([]interface{}); ok {
				for _, elem := range list {
					if ek := cp.Collection.ElementKey(elem); ek != "" {
						seen[ek] = struct{}{}
					}
				}
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolvePath walks a canonical path ("/a/b", or "/list/{key=val}/field") into a
// nested object, returning the value and whether it was present. A "{key=val}"
// segment selects the element of a list whose key field equals val.
func resolvePath(root interface{}, path string) (interface{}, bool) {
	cur := root
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			list, ok := cur.([]interface{})
			if !ok {
				return nil, false
			}
			key, val, _ := strings.Cut(seg[1:len(seg)-1], "=")
			found := false
			for _, elem := range list {
				em, ok := elem.(map[string]interface{})
				if ok && fmt.Sprintf("%v", em[key]) == val {
					cur, found = elem, true
					break
				}
			}
			if !found {
				return nil, false
			}
			continue
		}
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		next, ok := m[seg]
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// observationDigest is the canonical digest of the observation coverage envelope,
// so coverage itself is part of the proof (the envelope is hashable by design).
func observationDigest(obs Observation) (string, error) {
	canonical, err := canonicaljson.ToJSON(obs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
