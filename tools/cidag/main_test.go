package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"centralrelay/pkg/canonicaljson"
	"centralrelay/pkg/cidag"
)

func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// linearInput is the input of `cidag build` for the "linear" vector.
func linearInput(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "pkg", "cidag", "testdata", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := cidag.ParseStrict(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.(map[string]any)["positive"].([]any) {
		m := p.(map[string]any)
		if m["name"] == "linear" {
			b, _ := json.Marshal(map[string]any{"template": m["template"], "evidence": m["evidence"]})
			return string(b)
		}
	}
	t.Fatal("no linear vector")
	return ""
}

func TestBuildThenVerify(t *testing.T) {
	code, bundle, _ := run(t, linearInput(t), "build")
	if code != 0 || !strings.Contains(bundle, `"ci_root":"sha256:dfd4b6d0a30386b1b47c21d748fbfa095617f18317c0470bef21b1791a837ba9"`) {
		t.Fatalf("build: %d %.200s", code, bundle)
	}
	code, out, _ := run(t, bundle, "verify")
	if code != 0 || strings.TrimSpace(out) != "OK sha256:dfd4b6d0a30386b1b47c21d748fbfa095617f18317c0470bef21b1791a837ba9" {
		t.Fatalf("verify: %d %q", code, out)
	}
	// the same through files
	dir := t.TempDir()
	f := filepath.Join(dir, "bundle.json")
	if err := os.WriteFile(f, []byte(bundle), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := run(t, "", "verify", f); code != 0 || !strings.HasPrefix(out, "OK ") {
		t.Errorf("verify FILE: %d %q", code, out)
	}
	if code, out, _ := run(t, bundle, "verify", "-"); code != 0 || !strings.HasPrefix(out, "OK ") {
		t.Errorf("verify -: %d %q", code, out)
	}
	// a one-character tamper is refused with its class
	bad := strings.Replace(bundle, `"channel":"stable"`, `"channel":"beta"`, 1)
	if bad == bundle {
		t.Fatal("tamper did not apply")
	}
	if code, _, errs := run(t, bad, "verify"); code != 1 || !strings.Contains(errs, "REFUSED STEP_ROOT_MISMATCH") {
		t.Errorf("tampered bundle: %d %q", code, errs)
	}
}

func TestBuildRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"not an object":           {`[]`, "REFUSED BUNDLE_SHAPE"},
		"missing evidence":        {`{"template":{}}`, "REFUSED BUNDLE_SHAPE"},
		"extra member":            {`{"template":1,"evidence":1,"x":1}`, "REFUSED BUNDLE_SHAPE"},
		"duplicate member name":   {`{"template":1,"template":2}`, "REFUSED PARSE"},
		"float":                   {`{"template":1.5,"evidence":1}`, "REFUSED PARSE"},
		"template of wrong shape": {`{"template":1,"evidence":{}}`, "REFUSED TEMPLATE_SHAPE"},
	} {
		if code, _, errs := run(t, tc.in, "build"); code != 1 || !strings.Contains(errs, tc.want) {
			t.Errorf("%s: %d %q", name, code, errs)
		}
	}
	// a value outside the profile reaches the canonical writer only through the template or evidence checks;
	// a template that is valid but carries an outside-profile workflow id is refused as TEMPLATE_ID
	in := `{"template":{"workflow":"a<b","steps":[]},"evidence":{}}`
	if code, _, errs := run(t, in, "build"); code != 1 || !strings.Contains(errs, "REFUSED TEMPLATE_ID") {
		t.Errorf("outside the profile: %d %q", code, errs)
	}
}

func TestVerifyRefusals(t *testing.T) {
	if code, _, errs := run(t, `{"a":1}`, "verify"); code != 1 || !strings.Contains(errs, "REFUSED BUNDLE_SHAPE") {
		t.Errorf("shape: %d %q", code, errs)
	}
	if code, _, errs := run(t, `{`, "verify"); code != 1 || !strings.Contains(errs, "REFUSED PARSE") {
		t.Errorf("parse: %d %q", code, errs)
	}
}

func TestUsageAndIOErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"frobnicate"}, {"template"}, {"template", "a", "b", "c"}} {
		if code, _, errs := run(t, "", args...); code != 2 || !strings.Contains(errs, "usage:") {
			t.Errorf("%v: %d %q", args, code, errs)
		}
	}
	for _, args := range [][]string{{"build", "/nonexistent/in.json"}, {"verify", "/nonexistent/in.json"}, {"template", "/nonexistent/w.yml"}, {"build", "a", "b"}} {
		if code, _, errs := run(t, "", args...); code != 2 || !strings.Contains(errs, "error:") {
			t.Errorf("%v: %d %q", args, code, errs)
		}
	}
}

func TestTemplateCommand(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "verify.yml")
	if err := os.WriteFile(f, []byte("jobs:\n  a: {}\n  b:\n    needs: a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "", "template", f)
	if code != 0 || !strings.Contains(out, `"workflow":"verify"`) || !strings.Contains(out, `"needs":["a"]`) {
		t.Fatalf("template: %d %q", code, out)
	}
	if code, out, _ := run(t, "", "template", f, "custom-id"); code != 0 || !strings.Contains(out, `"workflow":"custom-id"`) {
		t.Errorf("template with an id: %d %q", code, out)
	}
	noext := filepath.Join(dir, "workflow")
	if err := os.WriteFile(noext, []byte("jobs:\n  a: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := run(t, "", "template", noext); code != 0 || !strings.Contains(out, `"workflow":"workflow"`) {
		t.Errorf("template of a file without an extension: %d %q", code, out)
	}
	empty := filepath.Join(dir, "empty.yml")
	if err := os.WriteFile(empty, []byte("name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := run(t, "", "template", empty); code != 1 || !strings.Contains(errs, "REFUSED WORKFLOW_NO_JOBS") {
		t.Errorf("no jobs: %d %q", code, errs)
	}
	// a workflow id outside the profile cannot be written canonically
	if code, _, errs := run(t, "", "template", f, "a<b"); code != 1 || !strings.Contains(errs, "REFUSED PROFILE") {
		t.Errorf("template id outside the profile: %d %q", code, errs)
	}
}

func TestMain_UsesRunFuncAndExit(t *testing.T) {
	oldRun, oldExit := RunFunc, osExit
	defer func() { RunFunc, osExit = oldRun, oldExit }()
	got := -1
	RunFunc = func([]string, io.Reader, io.Writer, io.Writer) int { return 7 }
	osExit = func(c int) { got = c }
	main()
	if got != 7 {
		t.Errorf("main exited with %d, want 7", got)
	}
}

// TestWriterFailure: when the canonical writer itself fails (injected), the tool reports an error of its own
// (exit 2), not a refusal of the content and never a root.
func TestWriterFailure(t *testing.T) {
	in := linearInput(t)
	calls := 0
	canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
		calls++
		if calls == 6 { // 3 steps, the template and the CI root inside Build, then the bundle of the CLI
			return nil, errors.New("injected writer failure")
		}
		return canonicaljson.ToJSONOriginal(v)
	})
	defer canonicaljson.SetToJSONMock(nil)
	code, out, errs := run(t, in, "build")
	if code != 2 || out != "" || !strings.Contains(errs, "injected writer failure") {
		t.Errorf("writer failure: %d %q %q", code, out, errs)
	}
}
