package cidag

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"gopkg.in/yaml.v3"
)

// Workflow adapter error classes (these are not part of the cross-language vectors: the adapter is Go only).
const (
	// KindWorkflowParse: the workflow file is not YAML the reader accepts.
	KindWorkflowParse = "WORKFLOW_PARSE"
	// KindWorkflowShape: a job's needs is neither a string nor a list of strings.
	KindWorkflowShape = "WORKFLOW_SHAPE"
	// KindWorkflowEmpty: the workflow has no jobs.
	KindWorkflowEmpty = "WORKFLOW_NO_JOBS"
)

// stepDef is a step of the template: its id, definition digest and the steps it needs.
type stepDef struct {
	id        string
	defDigest string
	needs     []string
}

// template is the validated structure of a CI process: its steps and the dependency edges between them. It is
// a derived representation (see TemplateFromWorkflow): the same versioned definitions give the same template.
type template struct {
	workflow string
	steps    []*stepDef
	byID     map[string]*stepDef
}

// keysAre reports whether m has exactly the given member names.
func keysAre(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// sortedKeys returns the member names of m in ascending byte order.
func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// parseID returns v if it is an identifier of the permitted form, else an error of the given class.
func parseID(v any, kind string) (string, error) {
	s, ok := v.(string)
	if !ok || !idRe.MatchString(s) {
		return "", fail(kind, "bad identifier %v", v)
	}
	return s, nil
}

// parseDigest returns v if it is a digest of the permitted form, else an error of the given class.
func parseDigest(v any, kind string) (string, error) {
	s, ok := v.(string)
	if !ok || !digestRe.MatchString(s) {
		return "", fail(kind, "bad digest %v", v)
	}
	return s, nil
}

// parseTemplate validates a template of the form {"workflow": id, "steps": [{"id","definition_digest","needs"}]}.
// The order of the checks is part of the contract (the first refusal names the class).
func parseTemplate(v any) (*template, error) {
	m, ok := v.(map[string]any)
	if !ok || !keysAre(m, "steps", "workflow") {
		return nil, fail(KindTemplateShape, "template must have exactly workflow and steps")
	}
	wf, err := parseID(m["workflow"], KindTemplateID)
	if err != nil {
		return nil, err
	}
	raw, ok := m["steps"].([]any)
	if !ok || len(raw) == 0 {
		return nil, fail(KindTemplateEmpty, "no steps")
	}
	t := &template{workflow: wf, byID: map[string]*stepDef{}}
	rawNeeds := map[string][]any{}
	for _, r := range raw {
		sm, ok := r.(map[string]any)
		if !ok || !keysAre(sm, "definition_digest", "id", "needs") {
			return nil, fail(KindTemplateShape, "a step must have exactly id, definition_digest and needs")
		}
		id, err := parseID(sm["id"], KindTemplateID)
		if err != nil {
			return nil, err
		}
		dd, err := parseDigest(sm["definition_digest"], KindTemplateDigest)
		if err != nil {
			return nil, err
		}
		if _, dup := t.byID[id]; dup {
			return nil, fail(KindTemplateDuplicateID, "%s", id)
		}
		needs, ok := sm["needs"].([]any)
		if !ok {
			return nil, fail(KindTemplateShape, "needs must be a list")
		}
		s := &stepDef{id: id, defDigest: dd}
		t.byID[id] = s
		t.steps = append(t.steps, s)
		rawNeeds[id] = needs
	}
	for _, s := range t.steps {
		seen := map[string]bool{}
		for _, n := range rawNeeds[s.id] {
			nid, err := parseID(n, KindTemplateID)
			if err != nil {
				return nil, err
			}
			if _, known := t.byID[nid]; !known {
				return nil, fail(KindTemplateNeedUnknown, "%s needs %s", s.id, nid)
			}
			if seen[nid] {
				return nil, fail(KindTemplateNeedDuplicate, "%s needs %s twice", s.id, nid)
			}
			seen[nid] = true
			s.needs = append(s.needs, nid)
		}
	}
	return t, nil
}

// order is Kahn's algorithm with the ready steps taken in ascending id order: one deterministic order, or a
// TEMPLATE_CYCLE refusal.
func (t *template) order() ([]string, error) {
	indeg := map[string]int{}
	users := map[string][]string{}
	for _, s := range t.steps {
		indeg[s.id] = len(s.needs)
		for _, n := range s.needs {
			users[n] = append(users[n], s.id)
		}
	}
	var ready []string
	for id, d := range indeg {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	out := make([]string, 0, len(t.steps))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		out = append(out, id)
		for _, u := range users[id] {
			indeg[u]--
			if indeg[u] == 0 {
				ready = append(ready, u)
			}
		}
		sort.Strings(ready)
	}
	if len(out) != len(t.steps) {
		return nil, fail(KindTemplateCycle, "the dependency graph has a cycle")
	}
	return out, nil
}

// object is the canonical object whose digest is the template digest: the step ids with their definition
// digests and their (sorted) needs, so that a missing step or edge changes the CI root.
func (t *template) object() map[string]any {
	steps := map[string]any{}
	for _, s := range t.steps {
		needs := make([]string, len(s.needs))
		copy(needs, s.needs)
		sort.Strings(needs)
		na := make([]any, len(needs))
		for i, n := range needs {
			na[i] = n
		}
		steps[s.id] = map[string]any{"definition_digest": s.defDigest, "needs": na}
	}
	return map[string]any{"contract": TemplateContract, "steps": steps, "workflow": t.workflow}
}

// TemplateFromWorkflow derives the template of a GitHub Actions workflow file: one step per job, the edges from
// `needs`. It reads nothing else of the YAML (no `if`, no matrix, no reusable workflows): a job that may not run
// is a step whose result can be `skipped`. The definition digest of every job is the SHA-256 of the raw bytes of
// the workflow file, which is coarse (a change anywhere in the file changes every digest) and safe: no YAML
// semantics decide what a definition is. The result is in the generic form Build and Verify take.
func TemplateFromWorkflow(data []byte, workflowID string) (map[string]any, error) {
	var doc struct {
		Jobs map[string]struct {
			Needs any `yaml:"needs"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fail(KindWorkflowParse, "%v", err)
	}
	if len(doc.Jobs) == 0 {
		return nil, fail(KindWorkflowEmpty, "the workflow has no jobs")
	}
	sum := sha256.Sum256(data)
	dd := "sha256:" + hex.EncodeToString(sum[:])
	ids := make([]string, 0, len(doc.Jobs))
	for id := range doc.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	steps := make([]any, 0, len(ids))
	for _, id := range ids {
		needs := []any{}
		switch n := doc.Jobs[id].Needs.(type) {
		case nil:
		case string:
			needs = append(needs, n)
		case []any:
			for _, e := range n {
				s, ok := e.(string)
				if !ok {
					return nil, fail(KindWorkflowShape, "job %s: needs holds a non-string", id)
				}
				needs = append(needs, s)
			}
		default:
			return nil, fail(KindWorkflowShape, "job %s: needs is neither a string nor a list", id)
		}
		steps = append(steps, map[string]any{"id": id, "definition_digest": dd, "needs": needs})
	}
	return map[string]any{"workflow": workflowID, "steps": steps}, nil
}
