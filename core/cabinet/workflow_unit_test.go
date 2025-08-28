package cabinet

import (
	"errors"
	"reflect"
	"testing"
)

// -- ID() --

func TestWorkflow_ID_Basic(t *testing.T) {
	w := &Workflow{Meta: BaseBlock{Name: "flow", Version: "1.2.3"}}
	if got, want := w.ID(), "flow@1.2.3"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestWorkflow_ID_Empty(t *testing.T) {
	w := &Workflow{Meta: BaseBlock{Name: "", Version: ""}}
	if got, want := w.ID(), "@"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

// -- GetData() --

func TestWorkflow_GetData_ReturnsSteps(t *testing.T) {
	steps := []string{"schema.a", "schema.b", "schema.c"}
	w := &Workflow{Meta: BaseBlock{Name: "f", Version: "v"}, Steps: append([]string(nil), steps...)} // saját másolat

	gotAny := w.GetData()
	got, ok := gotAny.([]string)
	if !ok {
		t.Fatalf("GetData() dynamic type = %T, want []string", gotAny)
	}
	if !reflect.DeepEqual(got, steps) {
		t.Fatalf("GetData() != Steps\n got:  %#v\n want: %#v", got, steps)
	}
}

// -- Validate() --
// A ModuleDescriptor/Schema-hoz bevezetett varratot (validateMetaFunc) használjuk.

func TestWorkflow_Validate_UsesValidateMetaAndPropagatesError(t *testing.T) {
	defer func(prev func(*BaseBlock, any) error) { validateMetaFunc = prev }(validateMetaFunc)

	var called bool
	var gotMeta *BaseBlock
	var gotData any
	mockErr := errors.New("boom")

	validateMetaFunc = func(meta *BaseBlock, data any) error {
		called = true
		gotMeta = meta
		gotData = data
		return mockErr
	}

	w := &Workflow{
		Meta:  BaseBlock{Name: "flow", Version: "1.0.0"},
		Steps: []string{"s1", "s2"},
	}

	err := w.Validate()
	if !called {
		t.Fatalf("validateMetaFunc was not called")
	}
	if !errors.Is(err, mockErr) {
		t.Fatalf("Validate() error = %v, want %v", err, mockErr)
	}
	if gotMeta != &w.Meta {
		t.Fatalf("meta pointer mismatch: got=%p want=%p", gotMeta, &w.Meta)
	}

	// data pontosan a Steps legyen
	steps, ok := gotData.([]string)
	if !ok {
		t.Fatalf("validate data type = %T, want []string", gotData)
	}
	if !reflect.DeepEqual(steps, w.Steps) {
		t.Fatalf("validate data mismatch: got=%#v want=%#v", steps, w.Steps)
	}
}
