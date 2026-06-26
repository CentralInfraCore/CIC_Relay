package cabinet_test

import (
	"reflect"
	"testing"

	"centralrelay/core/cabinet"
)

// -- ID() --

func TestWorkflow_ID_API_Basic(t *testing.T) {
	w := &cabinet.Workflow{
		Config:   cabinet.WorkflowConfig{Name: "flow"},
		Identity: cabinet.WorkflowIdentity{Version: "1.2.3"},
	}
	if got, want := w.ID(), "flow@1.2.3"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestWorkflow_ID_API_Empty(t *testing.T) {
	w := &cabinet.Workflow{}
	if got, want := w.ID(), "@"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

// -- GetData() --

func TestWorkflow_GetData_API_ReturnsConfig(t *testing.T) {
	steps := []string{"schema.a", "schema.b", "schema.c"}
	cfg := cabinet.WorkflowConfig{
		Name:  "f",
		Steps: append([]string(nil), steps...),
	}
	w := &cabinet.Workflow{
		Config:   cfg,
		Identity: cabinet.WorkflowIdentity{Version: "v"},
	}

	gotAny := w.GetData()
	got, ok := gotAny.(cabinet.WorkflowConfig)
	if !ok {
		t.Fatalf("GetData() dynamic type = %T, want cabinet.WorkflowConfig", gotAny)
	}
	if !reflect.DeepEqual(got.Steps, steps) {
		t.Fatalf("GetData().Steps != Config.Steps\n got:  %#v\n want: %#v", got.Steps, steps)
	}
}
