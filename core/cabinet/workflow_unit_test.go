package cabinet

import (
	"reflect"
	"testing"
)

// -- ID() --

func TestWorkflow_ID_Basic(t *testing.T) {
	w := &Workflow{
		Config:   WorkflowConfig{Name: "flow"},
		Identity: WorkflowIdentity{Version: "1.2.3"},
	}
	if got, want := w.ID(), "flow@1.2.3"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestWorkflow_ID_Empty(t *testing.T) {
	w := &Workflow{}
	if got, want := w.ID(), "@"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

// -- GetData() --

func TestWorkflow_GetData_ReturnsConfig(t *testing.T) {
	steps := []string{"schema.a", "schema.b", "schema.c"}
	cfg := WorkflowConfig{Name: "f", Steps: append([]string(nil), steps...)}
	w := &Workflow{
		Config:   cfg,
		Identity: WorkflowIdentity{Version: "v"},
	}

	gotAny := w.GetData()
	got, ok := gotAny.(WorkflowConfig)
	if !ok {
		t.Fatalf("GetData() dynamic type = %T, want WorkflowConfig", gotAny)
	}
	if !reflect.DeepEqual(got.Steps, steps) {
		t.Fatalf("GetData().Steps != Config.Steps\n got:  %#v\n want: %#v", got.Steps, steps)
	}
}
