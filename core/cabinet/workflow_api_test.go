package cabinet_test

import (
	"reflect"
	"testing"

	"centralrelay/core/cabinet"
)

// -- ID() --

func TestWorkflow_ID_API_Basic(t *testing.T) {
	w := &cabinet.Workflow{Meta: cabinet.BaseBlock{Name: "flow", Version: "1.2.3"}}
	if got, want := w.ID(), "flow@1.2.3"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestWorkflow_ID_API_Empty(t *testing.T) {
	w := &cabinet.Workflow{Meta: cabinet.BaseBlock{Name: "", Version: ""}}
	if got, want := w.ID(), "@"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

// -- GetData() --

func TestWorkflow_GetData_API_ReturnsSteps(t *testing.T) {
	steps := []string{"schema.a", "schema.b", "schema.c"}
	w := &cabinet.Workflow{
		Meta:  cabinet.BaseBlock{Name: "f", Version: "v"},
		Steps: append([]string(nil), steps...),
	}

	gotAny := w.GetData()
	got, ok := gotAny.([]string)
	if !ok {
		t.Fatalf("GetData() dynamic type = %T, want []string", gotAny)
	}
	if !reflect.DeepEqual(got, steps) {
		t.Fatalf("GetData() != Steps\n got:  %#v\n want: %#v", got, steps)
	}
}
