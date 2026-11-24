package cidag

import (
	"sort"
)

// Graph is the result of Build: the canonical object of every step, its root, the digest of the template and
// the CI root.
type Graph struct {
	Objects        map[string]map[string]any
	Canonical      map[string][]byte
	StepRoots      map[string]string
	TemplateDigest string
	CIRoot         string
}

// checkEvidence validates the execution evidence {step id: {"inputs": {...}, "result": {"status","digest"}}}
// against the template: every step has evidence (a step that did not run is `skipped`, not absent), no other
// step has any, and every value is inside the profile.
func checkEvidence(t *template, v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fail(KindEvidenceShape, "evidence must be an object keyed by step id")
	}
	ids := make([]string, 0, len(t.byID))
	for id := range t.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, ok := m[id]; !ok {
			return fail(KindEvidenceMissing, "%s", id)
		}
	}
	keys := sortedKeys(m)
	for _, id := range keys {
		if _, ok := t.byID[id]; !ok {
			return fail(KindEvidenceExtra, "%s", id)
		}
	}
	for _, id := range keys {
		em, ok := m[id].(map[string]any)
		if !ok || !keysAre(em, "inputs", "result") {
			return fail(KindEvidenceShape, "%s: exactly inputs and result", id)
		}
		inputs, ok := em["inputs"].(map[string]any)
		if !ok {
			return fail(KindEvidenceShape, "%s: inputs must be an object", id)
		}
		if err := checkInputs(id, inputs); err != nil {
			return err
		}
		if err := checkResult(id, em["result"]); err != nil {
			return err
		}
	}
	return nil
}

// checkInputs validates the inputs of one step: names of the permitted form, values that are booleans, escape-free strings or integers in range.
func checkInputs(id string, inputs map[string]any) error {
	for _, name := range sortedKeys(inputs) {
		if !idRe.MatchString(name) {
			return fail(KindInputName, "%s: %q", id, name)
		}
		switch val := inputs[name].(type) {
		case bool:
		case string:
			if len(val) == 0 || len(val) > maxParam || !paramString(val) {
				return fail(KindInputValue, "%s.%s", id, name)
			}
		default:
			n, ok := asInt(val)
			if !ok || n > maxInt || n < -maxInt {
				return fail(KindInputValue, "%s.%s", id, name)
			}
		}
	}
	return nil
}

// checkResult validates the result of one step: its status, and a digest that is null exactly for skipped and cancelled.
func checkResult(id string, v any) error {
	r, ok := v.(map[string]any)
	if !ok || !keysAre(r, "digest", "status") {
		return fail(KindResultShape, "%s: exactly status and digest", id)
	}
	status, ok := r["status"].(string)
	if !ok || !statuses[status] {
		return fail(KindResultStatus, "%s: %v", id, r["status"])
	}
	if nullDigestStatuses[status] {
		if r["digest"] != nil {
			return fail(KindResultDigest, "%s: %s must have a null digest", id, status)
		}
		return nil
	}
	_, err := parseDigest(r["digest"], KindResultDigest)
	return err
}

// Build derives the step objects, their roots and the CI root from a template and the execution evidence.
// Both arguments are in the generic JSON model (see ParseStrict). The result does not depend on the order of
// the template's steps, of its needs, or of any map.
func Build(templateV, evidenceV any) (*Graph, error) {
	t, err := parseTemplate(templateV)
	if err != nil {
		return nil, err
	}
	order, err := t.order()
	if err != nil {
		return nil, err
	}
	if err := checkEvidence(t, evidenceV); err != nil {
		return nil, err
	}
	evidence := evidenceV.(map[string]any)
	g := &Graph{
		Objects:   map[string]map[string]any{},
		Canonical: map[string][]byte{},
		StepRoots: map[string]string{},
	}
	for _, id := range order {
		s := t.byID[id]
		deps := make([]string, len(s.needs))
		for i, n := range s.needs {
			deps[i] = g.StepRoots[n]
		}
		sort.Strings(deps)
		da := make([]any, len(deps))
		for i, d := range deps {
			da[i] = d
		}
		e := evidence[id].(map[string]any)
		r := e["result"].(map[string]any)
		obj := map[string]any{
			"contract":     StepContract,
			"dependencies": da,
			"inputs":       e["inputs"],
			"result":       map[string]any{"digest": r["digest"], "status": r["status"]},
			"step":         map[string]any{"definition_digest": s.defDigest, "id": id},
		}
		root, canon, err := digest(stepPrefix, obj)
		if err != nil {
			return nil, err
		}
		g.Objects[id], g.Canonical[id], g.StepRoots[id] = obj, canon, root
	}
	g.TemplateDigest, _, err = digest(templatePrefix, t.object())
	if err != nil {
		return nil, err
	}
	roots := map[string]any{}
	for id, r := range g.StepRoots {
		roots[id] = r
	}
	g.CIRoot, _, err = digest(rootPrefix, map[string]any{
		"contract": RootContract, "steps": roots, "template_digest": g.TemplateDigest, "workflow": t.workflow,
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

// jsonEqual compares two values of the generic JSON model structurally (an int and an int64 of the same value
// are equal; a bool never equals a number).
func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case int, int64:
		nx, _ := asInt(x)
		ny, ok := asInt(b)
		return ok && nx == ny
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !jsonEqual(xv, yv) {
				return false
			}
		}
		return true
	}
	return false
}

// Verify recomputes everything from a published bundle {"template","objects","step_roots","ci_root"}. A published
// object is never trusted: only its inputs and result are read (they are the evidence); its contract, step and
// dependencies, every step root, the template digest and the CI root are recomputed and compared. A change to a
// step is therefore caught at that step's own root if it is the last, and one hop downstream otherwise, in the
// dependencies of the step that depends on it.
func Verify(bundleV any) (*Graph, error) {
	m, ok := bundleV.(map[string]any)
	if !ok || !keysAre(m, "ci_root", "objects", "step_roots", "template") {
		return nil, fail(KindBundleShape, "exactly template, objects, step_roots and ci_root")
	}
	t, err := parseTemplate(m["template"])
	if err != nil {
		return nil, err
	}
	objs, ok := m["objects"].(map[string]any)
	if !ok {
		return nil, fail(KindBundleShape, "objects must be an object keyed by step id")
	}
	evidence := map[string]any{}
	for _, id := range sortedKeys(objs) {
		o, ok := objs[id].(map[string]any)
		if !ok || !keysAre(o, "contract", "dependencies", "inputs", "result", "step") {
			return nil, fail(KindObjectShape, "%s", id)
		}
		evidence[id] = map[string]any{"inputs": o["inputs"], "result": o["result"]}
	}
	g, err := Build(m["template"], evidence)
	if err != nil {
		return nil, err
	}
	order, _ := t.order()
	for _, id := range order {
		got, want := objs[id].(map[string]any), g.Objects[id]
		if !jsonEqual(got["contract"], want["contract"]) {
			return nil, fail(KindObjectContractMismatch, "%s", id)
		}
		if !jsonEqual(got["step"], want["step"]) {
			return nil, fail(KindObjectStepMismatch, "%s", id)
		}
		if !jsonEqual(got["dependencies"], want["dependencies"]) {
			return nil, fail(KindObjectDependenciesMismatch, "%s", id)
		}
	}
	roots := map[string]any{}
	for id, r := range g.StepRoots {
		roots[id] = r
	}
	if !jsonEqual(m["step_roots"], roots) {
		return nil, fail(KindStepRootMismatch, "the published step roots differ from the recomputed ones")
	}
	if got, ok := m["ci_root"].(string); !ok || got != g.CIRoot {
		return nil, fail(KindCIRootMismatch, "the published CI root differs from the recomputed one")
	}
	return g, nil
}
