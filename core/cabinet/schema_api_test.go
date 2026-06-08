package cabinet_test

import (
	"reflect"
	"testing"

	"centralrelay/core/cabinet"
)

// -- ID() --

func TestSchema_ID_API_Basic(t *testing.T) {
	s := &cabinet.Schema{
		Config:   cabinet.SchemaConfig{Name: "workflow"},
		Identity: cabinet.SchemaIdentity{Version: "1.2.3"},
	}
	if got, want := s.ID(), "workflow@1.2.3"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestSchema_ID_API_Empty(t *testing.T) {
	s := &cabinet.Schema{}
	if got, want := s.ID(), "@"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

// -- GetData() --

func TestSchema_GetData_API_EqualsConfig(t *testing.T) {
	cfg := cabinet.SchemaConfig{
		Name:             "x",
		StateRequirement: map[string]string{"host.state": "ready"},
		Dependencies:     []string{"schema.a", "schema.b"},
		NextHops:         []string{"step1", "step2"},
	}
	s := &cabinet.Schema{
		Config:   cfg,
		Identity: cabinet.SchemaIdentity{Version: "y"},
	}

	gotAny := s.GetData()
	got, ok := gotAny.(cabinet.SchemaConfig)
	if !ok {
		t.Fatalf("GetData() dynamic type = %T, want cabinet.SchemaConfig", gotAny)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("GetData() != Config\n got:  %#v\n want: %#v", got, cfg)
	}
}
