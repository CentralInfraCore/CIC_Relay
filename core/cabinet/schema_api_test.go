package cabinet_test

import (
	"reflect"
	"testing"

	"centralrelay/core/cabinet"
)

// -- ID() --

func TestSchema_ID_API_Basic(t *testing.T) {
	s := &cabinet.Schema{Meta: cabinet.BaseBlock{Name: "workflow", Version: "1.2.3"}}
	if got, want := s.ID(), "workflow@1.2.3"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestSchema_ID_API_Empty(t *testing.T) {
	s := &cabinet.Schema{Meta: cabinet.BaseBlock{Name: "", Version: ""}}
	if got, want := s.ID(), "@"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

// -- GetData() --

func TestSchema_GetData_API_EqualsSpec(t *testing.T) {
	spec := cabinet.SchemaDef{
		StateRequirement: map[string]string{"host.state": "ready"},
		Dependencies:     []string{"schema.a", "schema.b"},
		NextHops:         []string{"step1", "step2"},
	}
	s := &cabinet.Schema{Meta: cabinet.BaseBlock{Name: "x", Version: "y"}, Spec: spec}

	gotAny := s.GetData()
	got, ok := gotAny.(cabinet.SchemaDef)
	if !ok {
		t.Fatalf("GetData() dynamic type = %T, want cabinet.SchemaDef", gotAny)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("GetData() != Spec\n got:  %#v\n want: %#v", got, spec)
	}
}

// -- Checksum() --

func TestSchema_Checksum_API_DeterministicAndChangesWithSpec(t *testing.T) {
	s := &cabinet.Schema{
		Meta: cabinet.BaseBlock{Name: "sch", Version: "1.0.0"},
		Spec: cabinet.SchemaDef{
			StateRequirement: map[string]string{"a": "b"},
			Dependencies:     []string{"d1"},
			NextHops:         []string{"n1"},
		},
	}

	// determinisztikus
	c1, err := s.Checksum()
	if err != nil {
		t.Fatalf("Checksum() err: %v", err)
	}
	c2, err := s.Checksum()
	if err != nil {
		t.Fatalf("Checksum() second err: %v", err)
	}
	if c1 != c2 {
		t.Fatalf("Checksum not deterministic: first=%q second=%q", c1, c2)
	}

	// Spec változás → checksum változik
	s.Spec.Dependencies = append(s.Spec.Dependencies, "d2")
	c3, err := s.Checksum()
	if err != nil {
		t.Fatalf("Checksum() after spec change err: %v", err)
	}
	if c3 == c1 {
		t.Fatalf("Checksum did not change after spec mutation: before=%q after=%q", c1, c3)
	}
}
