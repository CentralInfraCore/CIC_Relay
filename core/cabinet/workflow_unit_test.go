package cabinet

import (
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
	w := &Workflow{Meta: BaseBlock{Name: "f", Version: "v"}, Steps: append([]string(nil), steps...)} // create a distinct copy

	gotAny := w.GetData()
	got, ok := gotAny.([]string)
	if !ok {
		t.Fatalf("GetData() dynamic type = %T, want []string", gotAny)
	}
	if !reflect.DeepEqual(got, steps) {
		t.Fatalf("GetData() != Steps\n got:  %#v\n want: %#v", got, steps)
	}
}
