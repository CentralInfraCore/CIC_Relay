package cidag

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"centralrelay/pkg/canonicaljson"
)

func loadVectors(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := ParseStrict(data) // the vector file itself must pass the strict reader
	if err != nil {
		t.Fatalf("vectors.json is refused by the strict reader: %v", err)
	}
	return v.(map[string]any)
}

func kindOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	if err == nil {
		return ""
	}
	return "NOT-AN-Error:" + err.Error()
}

func asList(v any) []any { return v.([]any) }
func asMap(v any) map[string]any {
	return v.(map[string]any)
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, e := range x {
			a[i] = deepCopy(e)
		}
		return a
	}
	return v
}

func applyOps(t *testing.T, root any, ops []any) any {
	t.Helper()
	root = deepCopy(root)
	for _, o := range ops {
		op := asMap(o)
		path := asList(op["path"])
		var tgt any = root
		for _, p := range path[:len(path)-1] {
			tgt = step(tgt, p)
		}
		last := path[len(path)-1]
		del, _ := op["delete"].(bool)
		switch c := tgt.(type) {
		case map[string]any:
			if del {
				delete(c, last.(string))
			} else {
				c[last.(string)] = deepCopy(op["set"])
			}
		case []any:
			c[int(last.(int64))] = deepCopy(op["set"])
		default:
			t.Fatalf("bad path %v", path)
		}
	}
	return root
}

func step(tgt, p any) any {
	if m, ok := tgt.(map[string]any); ok {
		return m[p.(string)]
	}
	return tgt.([]any)[int(p.(int64))]
}

func rootsOf(g *Graph) map[string]any {
	m := map[string]any{}
	for id, r := range g.StepRoots {
		m[id] = r
	}
	return m
}

func bundleOf(tpl any, g *Graph) map[string]any {
	objs := map[string]any{}
	for id, o := range g.Objects {
		objs[id] = o
	}
	return map[string]any{"template": tpl, "objects": objs, "step_roots": rootsOf(g), "ci_root": g.CIRoot}
}

func TestCanonVectors(t *testing.T) {
	for _, c := range asList(loadVectors(t)["canon"]) {
		m := asMap(c)
		b, err := Canonical(m["value"])
		if err != nil || string(b) != m["canonical"].(string) {
			t.Errorf("%v: got %q err %v, want %q", m["name"], b, err, m["canonical"])
		}
	}
}

func TestPositiveVectors(t *testing.T) {
	for _, p := range asList(loadVectors(t)["positive"]) {
		m := asMap(p)
		name := m["name"].(string)
		g, err := Build(m["template"], m["evidence"])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		exp := asMap(m["expect"])
		for id, want := range asMap(exp["canonical_objects"]) {
			if string(g.Canonical[id]) != want.(string) {
				t.Errorf("%s/%s: canonical object\n got  %s\n want %s", name, id, g.Canonical[id], want)
			}
		}
		if len(g.Canonical) != len(asMap(exp["canonical_objects"])) {
			t.Errorf("%s: %d objects, want %d", name, len(g.Canonical), len(asMap(exp["canonical_objects"])))
		}
		if !jsonEqual(rootsOf(g), exp["step_roots"]) {
			t.Errorf("%s: step roots differ from the oracle's", name)
		}
		if g.TemplateDigest != exp["template_digest"].(string) {
			t.Errorf("%s: template digest %s, want %s", name, g.TemplateDigest, exp["template_digest"])
		}
		if g.CIRoot != exp["ci_root"].(string) {
			t.Errorf("%s: CI root %s, want %s", name, g.CIRoot, exp["ci_root"])
		}
		if _, err := Verify(bundleOf(m["template"], g)); err != nil {
			t.Errorf("%s: the bundle built here is refused by Verify: %v", name, err)
		}
	}
}

func TestNegativeBuildVectors(t *testing.T) {
	cases := asList(loadVectors(t)["negative_build"])
	if len(cases) < 40 {
		t.Fatalf("only %d negative build vectors", len(cases))
	}
	for _, c := range cases {
		m := asMap(c)
		g, err := Build(m["template"], m["evidence"])
		if g != nil || kindOf(err) != m["error"].(string) {
			t.Errorf("%v: got %q (graph %v), want %v", m["name"], kindOf(err), g != nil, m["error"])
		}
	}
}

func TestVerifyVectors(t *testing.T) {
	v := loadVectors(t)
	base := asMap(asMap(v["verify_base"])["bundle"])
	if _, err := Verify(base); err != nil {
		t.Fatalf("the oracle's own bundle is refused: %v", err)
	}
	cases := asList(v["negative_verify"])
	if len(cases) < 25 {
		t.Fatalf("only %d negative verify vectors", len(cases))
	}
	for _, c := range cases {
		m := asMap(c)
		g, err := Verify(applyOps(t, base, asList(m["ops"])))
		if g != nil || kindOf(err) != m["error"].(string) {
			t.Errorf("%v: got %q, want %v", m["name"], kindOf(err), m["error"])
		}
	}
}

func TestParseVectors(t *testing.T) {
	v := loadVectors(t)
	for _, c := range asList(v["parse"]) {
		m := asMap(c)
		if _, err := ParseStrict([]byte(m["text"].(string))); kindOf(err) != m["error"].(string) {
			t.Errorf("%v: got %q, want %v", m["name"], kindOf(err), m["error"])
		}
	}
	for _, c := range asList(v["parse_ok"]) {
		m := asMap(c)
		val, err := ParseStrict([]byte(m["text"].(string)))
		if err != nil {
			t.Fatal(err)
		}
		if b, err := Canonical(val); err != nil || string(b) != m["canonical"].(string) {
			t.Errorf("%v: %q %v", m["name"], b, err)
		}
	}
	if _, err := ParseStrict([]byte("{} {}")); kindOf(err) != KindParse {
		t.Errorf("data after the document: %v", err)
	}
	if _, err := ParseStrict([]byte(`{"a":1`)); kindOf(err) != KindParse {
		t.Errorf("unterminated object: %v", err)
	}
	if _, err := ParseStrict([]byte(`[1,2`)); kindOf(err) != KindParse {
		t.Errorf("unterminated array: %v", err)
	}
	if _, err := ParseStrict([]byte(`{"a":-9223372036854775808}`)); kindOf(err) != KindParse {
		t.Errorf("int64 minimum must be refused like the independent implementation: %v", err)
	}
	if v, err := ParseStrict([]byte(`{"a":-0}`)); err != nil || asMap(v)["a"].(int64) != 0 {
		t.Errorf("-0: %v %v", v, err)
	}
	for _, s := range []string{`"x"`, `null`, `true`, `7`} {
		if _, err := ParseStrict([]byte(s)); err != nil {
			t.Errorf("scalar document %s: %v", s, err)
		}
	}
	if _, err := ParseStrict([]byte(`{1:2}`)); kindOf(err) != KindParse {
		t.Errorf("non-string member name: %v", err)
	}
	if _, err := ParseStrict([]byte(``)); kindOf(err) != KindParse {
		t.Errorf("empty input: %v", err)
	}
	deep := strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth)
	if _, err := ParseStrict([]byte(deep)); err != nil {
		t.Errorf("exactly %d levels must be accepted: %v", MaxDepth, err)
	}
}

// ---- properties that do not depend on the vectors

func linear(t *testing.T) (any, any) {
	t.Helper()
	for _, p := range asList(loadVectors(t)["positive"]) {
		if m := asMap(p); m["name"] == "linear" {
			return m["template"], m["evidence"]
		}
	}
	t.Fatal("no linear vector")
	return nil, nil
}

func TestOrderIndependence(t *testing.T) {
	for _, p := range asList(loadVectors(t)["positive"]) {
		m := asMap(p)
		want := m["expect"].(map[string]any)["ci_root"].(string)
		tpl := deepCopy(m["template"]).(map[string]any)
		steps := asList(tpl["steps"])
		for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
			steps[i], steps[j] = steps[j], steps[i]
		}
		for _, s := range steps {
			needs := asList(asMap(s)["needs"])
			for i, j := 0, len(needs)-1; i < j; i, j = i+1, j-1 {
				needs[i], needs[j] = needs[j], needs[i]
			}
		}
		g, err := Build(tpl, m["evidence"])
		if err != nil || g.CIRoot != want {
			t.Errorf("%v: reversed declaration order changed the root (%v)", m["name"], err)
		}
		g2, _ := Build(m["template"], m["evidence"])
		g3, _ := Build(m["template"], m["evidence"])
		if g2.CIRoot != g3.CIRoot {
			t.Errorf("%v: two builds of one input differ", m["name"])
		}
	}
}

// TestChangesPropagate measures what a change does: it changes the CI root, the root of the changed step and of
// every step that depends on it, and nothing else.
func TestChangesPropagate(t *testing.T) {
	tpl, ev := linear(t)
	base, err := Build(tpl, ev)
	if err != nil {
		t.Fatal(err)
	}
	type change struct {
		name    string
		mut     func(tpl, ev map[string]any)
		changed []string
	}
	in := func(ev map[string]any, id string) map[string]any { return asMap(asMap(ev[id])["inputs"]) }
	res := func(ev map[string]any, id string) map[string]any { return asMap(asMap(ev[id])["result"]) }
	steps := func(tpl map[string]any) []any { return asList(tpl["steps"]) }
	changes := []change{
		{"input value of the last step", func(_, ev map[string]any) { in(ev, "release")["channel"] = "beta" }, []string{"release"}},
		{"input value of a middle step", func(_, ev map[string]any) { in(ev, "test")["mode"] = "delta" }, []string{"test", "release"}},
		{"input value of the first step", func(_, ev map[string]any) { in(ev, "build")["compiler"] = "go1.0" }, []string{"build", "test", "release"}},
		{"an input added", func(_, ev map[string]any) { in(ev, "test")["extra"] = int64(1) }, []string{"test", "release"}},
		{"an input removed", func(_, ev map[string]any) { delete(in(ev, "test"), "retries") }, []string{"test", "release"}},
		{"an integer input changed", func(_, ev map[string]any) { in(ev, "test")["retries"] = int64(3) }, []string{"test", "release"}},
		{"a boolean input changed", func(_, ev map[string]any) { in(ev, "test")["gate_enabled"] = false }, []string{"test", "release"}},
		{"result digest", func(_, ev map[string]any) { res(ev, "test")["digest"] = "sha256:" + strings.Repeat("0", 64) }, []string{"test", "release"}},
		{"result status", func(_, ev map[string]any) { res(ev, "test")["status"] = "neutral" }, []string{"test", "release"}},
		{"a result turned into a skip", func(_, ev map[string]any) {
			res(ev, "release")["status"] = "skipped"
			res(ev, "release")["digest"] = nil
		}, []string{"release"}},
		{"definition digest of a step", func(tpl, _ map[string]any) {
			asMap(steps(tpl)[1])["definition_digest"] = "sha256:" + strings.Repeat("1", 64)
		}, []string{"test", "release"}},
		{"an edge added", func(tpl, _ map[string]any) { asMap(steps(tpl)[2])["needs"] = []any{"test", "build"} }, []string{"release"}},
		{"an edge removed", func(tpl, _ map[string]any) { asMap(steps(tpl)[2])["needs"] = []any{} }, []string{"release"}},
		{"the workflow renamed", func(tpl, _ map[string]any) { tpl["workflow"] = "other" }, nil},
	}
	for _, c := range changes {
		tp, e := deepCopy(tpl).(map[string]any), deepCopy(ev).(map[string]any)
		c.mut(tp, e)
		g, err := Build(tp, e)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if g.CIRoot == base.CIRoot {
			t.Errorf("%s: the CI root did not change", c.name)
		}
		var diff []string
		for id, r := range g.StepRoots {
			if r != base.StepRoots[id] {
				diff = append(diff, id)
			}
		}
		sort.Strings(diff)
		want := append([]string(nil), c.changed...)
		sort.Strings(want)
		if strings.Join(diff, ",") != strings.Join(want, ",") {
			t.Errorf("%s: roots changed for %v, want %v", c.name, diff, want)
		}
	}
	// a step id renamed everywhere: every root of the renamed step and of its dependents changes
	tp, e := deepCopy(tpl).(map[string]any), deepCopy(ev).(map[string]any)
	asMap(steps(tp)[0])["id"] = "compile"
	asMap(steps(tp)[1])["needs"] = []any{"compile"}
	e["compile"] = e["build"]
	delete(e, "build")
	g, err := Build(tp, e)
	if err != nil || g.CIRoot == base.CIRoot {
		t.Errorf("a renamed step must change the CI root (%v)", err)
	}
}

func TestDomainSeparation(t *testing.T) {
	v := map[string]any{"a": "b"}
	s, _, _ := digest(stepPrefix, v)
	tp, _, _ := digest(templatePrefix, v)
	r, _, _ := digest(rootPrefix, v)
	if s == tp || s == r || tp == r {
		t.Errorf("one object hashes equal under two domains: %s %s %s", s, tp, r)
	}
}

// TestCanonicalEqualsStandardJSON: on the profile, pkg/canonicaljson.ToJSON, encoding/json (which sorts map keys
// by byte order) and the independent implementation give the same bytes.
func TestCanonicalEqualsStandardJSON(t *testing.T) {
	for _, p := range asList(loadVectors(t)["positive"]) {
		m := asMap(p)
		g, err := Build(m["template"], m["evidence"])
		if err != nil {
			t.Fatal(err)
		}
		for id, o := range g.Objects {
			std, err := json.Marshal(o)
			if err != nil || !bytes.Equal(std, g.Canonical[id]) {
				t.Errorf("%v/%s: encoding/json and the canonical form differ", m["name"], id)
			}
		}
	}
}

func TestProfile(t *testing.T) {
	ok := []any{nil, true, int(3), int64(-3), int64(maxInt), int64(-maxInt), "a b/c", []any{}, map[string]any{}, []any{map[string]any{"k": []any{nil}}}}
	for _, v := range ok {
		if _, err := Canonical(v); err != nil {
			t.Errorf("%#v: %v", v, err)
		}
	}
	bad := []any{1.5, float64(2), []string{"a"}, map[string]string{"a": "b"}, int(maxInt) + 1, int64(-maxInt) - 1, "a<b", "a\x7f", "é", "line\nbreak",
		map[string]any{"a&b": 1}, map[string]any{"k": 1.5}, []any{struct{}{}}, uint(3)}
	for _, v := range bad {
		if _, err := Canonical(v); kindOf(err) != KindProfile {
			t.Errorf("%#v: got %v, want PROFILE", v, err)
		}
	}
	if (&Error{Kind: "K", Detail: "d"}).Error() != "K: d" {
		t.Error("Error()")
	}
}

func TestBuildRefusesWhatCannotBeCanonicalized(t *testing.T) {
	tpl, ev := linear(t)
	e := deepCopy(ev).(map[string]any)
	asMap(asMap(e["build"])["inputs"])["k"] = int64(1)
	if _, err := Build(tpl, e); err != nil {
		t.Fatalf("a valid change was refused: %v", err)
	}
	if _, err := Build(tpl, []any{}); kindOf(err) != KindEvidenceShape {
		t.Errorf("evidence not an object: %v", err)
	}
	if _, err := Build("template", ev); kindOf(err) != KindTemplateShape {
		t.Errorf("template not an object: %v", err)
	}
	if _, err := Build(map[string]any{"workflow": "w", "steps": []any{"x"}}, ev); kindOf(err) != KindTemplateShape {
		t.Errorf("step not an object: %v", err)
	}
	if _, err := Build(map[string]any{"workflow": "w", "steps": "x"}, ev); kindOf(err) != KindTemplateEmpty {
		t.Errorf("steps not a list: %v", err)
	}
	bad := deepCopy(ev).(map[string]any)
	asMap(bad["build"])["inputs"] = "x"
	if _, err := Build(tpl, bad); kindOf(err) != KindEvidenceShape {
		t.Errorf("inputs not an object: %v", err)
	}
	bad = deepCopy(ev).(map[string]any)
	asMap(bad["build"])["result"] = map[string]any{"status": 5, "digest": nil}
	if _, err := Build(tpl, bad); kindOf(err) != KindResultStatus {
		t.Errorf("status not a string: %v", err)
	}
	bad = deepCopy(ev).(map[string]any)
	asMap(asMap(bad["build"])["inputs"])["k"] = 1.5
	if _, err := Build(tpl, bad); kindOf(err) != KindInputValue {
		t.Errorf("float input: %v", err)
	}
	bad = deepCopy(ev).(map[string]any)
	asMap(asMap(bad["build"])["inputs"])["k"] = int(5)
	if _, err := Build(tpl, bad); err != nil {
		t.Errorf("a Go int input is a valid integer: %v", err)
	}
}

func TestVerifyShapes(t *testing.T) {
	v := loadVectors(t)
	base := asMap(asMap(v["verify_base"])["bundle"])
	for name, mut := range map[string]func(map[string]any){
		"objects not an object": func(b map[string]any) { b["objects"] = []any{} },
		"object not an object":  func(b map[string]any) { asMap(b["objects"])["build"] = "x" },
		"ci_root not a string":  func(b map[string]any) { b["ci_root"] = int64(1) },
	} {
		b := deepCopy(base).(map[string]any)
		mut(b)
		g, err := Verify(b)
		want := map[string]string{"objects not an object": KindBundleShape, "object not an object": KindObjectShape, "ci_root not a string": KindCIRootMismatch}[name]
		if g != nil || kindOf(err) != want {
			t.Errorf("%s: got %q, want %s", name, kindOf(err), want)
		}
	}
	if _, err := Verify("x"); kindOf(err) != KindBundleShape {
		t.Errorf("bundle not an object: %v", err)
	}
	b := deepCopy(base).(map[string]any)
	b["template"] = "x"
	if _, err := Verify(b); kindOf(err) != KindTemplateShape {
		t.Errorf("template not an object: %v", err)
	}
}

func TestJSONEqual(t *testing.T) {
	eq := [][2]any{{nil, nil}, {true, true}, {"a", "a"}, {int(1), int64(1)}, {[]any{int64(1)}, []any{int(1)}}, {map[string]any{"a": nil}, map[string]any{"a": nil}}}
	ne := [][2]any{{nil, false}, {true, int64(1)}, {int64(1), true}, {"a", "b"}, {"1", int64(1)}, {[]any{}, []any{nil}}, {[]any{int64(1)}, []any{int64(2)}},
		{[]any{}, map[string]any{}}, {map[string]any{"a": 1}, map[string]any{"b": 1}}, {map[string]any{"a": int64(1)}, map[string]any{"a": int64(2)}},
		{map[string]any{"a": 1}, map[string]any{}}, {1.5, 1.5}, {false, "x"}, {map[string]any{}, []any{}}}
	for _, p := range eq {
		if !jsonEqual(p[0], p[1]) {
			t.Errorf("%#v should equal %#v", p[0], p[1])
		}
	}
	for _, p := range ne {
		if jsonEqual(p[0], p[1]) {
			t.Errorf("%#v must not equal %#v", p[0], p[1])
		}
	}
}

// ---- the workflow adapter

const sampleWorkflow = `
name: sample
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
    steps: [{run: make}]
  test:
    needs: build
    runs-on: ubuntu-latest
  lint:
    needs: [build]
  release:
    needs: [test, lint]
`

func TestTemplateFromWorkflow(t *testing.T) {
	tpl, err := TemplateFromWorkflow([]byte(sampleWorkflow), "sample")
	if err != nil {
		t.Fatal(err)
	}
	pt, err := parseTemplate(tpl)
	if err != nil {
		t.Fatal(err)
	}
	order, err := pt.order()
	if err != nil || strings.Join(order, ",") != "build,lint,test,release" {
		t.Errorf("order %v %v", order, err)
	}
	if got := strings.Join(pt.byID["release"].needs, ","); got != "test,lint" {
		t.Errorf("needs of release: %s", got)
	}
	again, _ := TemplateFromWorkflow([]byte(sampleWorkflow), "sample")
	a, _ := Canonical(tpl)
	b, _ := Canonical(again)
	if !bytes.Equal(a, b) {
		t.Error("the template is not reproducible from the same definition")
	}
	other, _ := TemplateFromWorkflow([]byte(sampleWorkflow+"# a change\n"), "sample")
	c, _ := Canonical(other)
	if bytes.Equal(a, c) {
		t.Error("a change of the workflow file must change the definition digests")
	}
	for name, src := range map[string]string{
		"not yaml":             "jobs: [",
		"no jobs":              "name: x\n",
		"needs is a number":    "jobs:\n  a:\n    needs: 3\n",
		"needs holds a number": "jobs:\n  a:\n    needs: [1]\n",
		"duplicate job key":    "jobs:\n  a: {}\n  a: {}\n",
	} {
		want := map[string]string{"not yaml": KindWorkflowParse, "no jobs": KindWorkflowEmpty, "needs is a number": KindWorkflowShape,
			"needs holds a number": KindWorkflowShape, "duplicate job key": KindWorkflowParse}[name]
		if _, err := TemplateFromWorkflow([]byte(src), "w"); kindOf(err) != want {
			t.Errorf("%s: got %v, want %s", name, err, want)
		}
	}
	// a template with an invalid job id is refused when it is used, not silently repaired
	bad, _ := TemplateFromWorkflow([]byte("jobs:\n  'a b': {}\n"), "w")
	if _, err := Build(bad, map[string]any{}); kindOf(err) != KindTemplateID {
		t.Errorf("job id with a space: %v", err)
	}
}

// TestRelayWorkflowsAreStructural runs the adapter on the real workflow files of this repository: each one gives
// a valid, acyclic template, the same one every time, and a complete evidence set builds a root.
func TestRelayWorkflowsAreStructural(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if len(files) == 0 {
		t.Skip("no workflow files next to the package")
	}
	withEdges := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tpl, err := TemplateFromWorkflow(data, strings.TrimSuffix(filepath.Base(f), ".yml"))
		if kindOf(err) == KindWorkflowEmpty {
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		ev := map[string]any{}
		for _, s := range asList(asMap(tpl)["steps"]) {
			id := asMap(s)["id"].(string)
			if len(asList(asMap(s)["needs"])) > 0 {
				withEdges++
			}
			ev[id] = map[string]any{"inputs": map[string]any{}, "result": map[string]any{"status": "skipped", "digest": nil}}
		}
		g1, err := Build(tpl, ev)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		g2, _ := Build(tpl, ev)
		if g1.CIRoot != g2.CIRoot || fmt.Sprint(g1.StepRoots) != fmt.Sprint(g2.StepRoots) {
			t.Errorf("%s: not reproducible", f)
		}
	}
	if withEdges == 0 {
		t.Error("no workflow job of this repository has a `needs` edge: the adapter was not exercised on a real dependency")
	}
}

// failingToJSON makes the nth call of the canonical writer fail, like pkg/canonicaljson's own mock hook
// is meant to be used: it reaches the error returns that validation makes unreachable with real input.
func failingToJSON(n int) (restore func()) {
	calls := 0
	canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
		calls++
		if calls == n {
			return nil, errors.New("injected writer failure")
		}
		return canonicaljson.ToJSONOriginal(v)
	})
	return func() { canonicaljson.SetToJSONMock(nil) }
}

func TestAWriterFailureIsAnErrorNotARoot(t *testing.T) {
	tpl, ev := linear(t) // 3 steps: calls 1-3 hash the steps, 4 the template, 5 the CI root
	for _, n := range []int{1, 2, 3, 4, 5} {
		restore := failingToJSON(n)
		g, err := Build(tpl, ev)
		restore()
		if g != nil || err == nil || !strings.Contains(err.Error(), "injected writer failure") {
			t.Errorf("failure at call %d: graph %v err %v", n, g != nil, err)
		}
	}
	if g, err := Build(tpl, ev); err != nil || g == nil {
		t.Errorf("the hook was not restored: %v", err)
	}
}
