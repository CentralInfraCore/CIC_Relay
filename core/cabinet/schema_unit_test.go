package cabinet

import (
	"errors"
	"reflect"
	"testing"
)

// -- ID() --

func TestSchema_ID_Basic(t *testing.T) {
	s := &Schema{Meta: BaseBlock{Name: "workflow", Version: "1.2.3"}}
	if got, want := s.ID(), "workflow@1.2.3"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestSchema_ID_Empty(t *testing.T) {
	s := &Schema{Meta: BaseBlock{Name: "", Version: ""}}
	if got, want := s.ID(), "@"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

// -- GetData() --

func TestSchema_GetData_EqualsSpecValue(t *testing.T) {
	spec := SchemaDef{
		StateRequirement: map[string]string{"host.state": "ready"},
		Dependencies:     []string{"schema.a", "schema.b"},
		NextHops:         []string{"step1", "step2"},
	}
	s := &Schema{Meta: BaseBlock{Name: "x", Version: "y"}, Spec: spec}

	gotAny := s.GetData()
	got, ok := gotAny.(SchemaDef)
	if !ok {
		t.Fatalf("GetData() dynamic type = %T, want SchemaDef", gotAny)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("GetData() != Spec\n got:  %#v\n want: %#v", got, spec)
	}
}

// -- Validate() --
// A ModuleDescriptor-hoz bevezetett test-varratot (validateMetaFunc) használjuk.

func TestSchema_Validate_UsesValidateMetaAndPropagatesError(t *testing.T) {
	// állapot-visszaállítás a teszt végén
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

	spec := SchemaDef{
		StateRequirement: map[string]string{"k": "v"},
		Dependencies:     []string{"dep"},
		NextHops:         []string{"nh"},
	}
	s := &Schema{
		Meta: BaseBlock{Name: "sch", Version: "1.0.0"},
		Spec: spec,
	}

	err := s.Validate()
	if !called {
		t.Fatalf("validateMetaFunc was not called")
	}
	if !errors.Is(err, mockErr) {
		t.Fatalf("Validate() error = %v, want %v", err, mockErr)
	}
	if gotMeta != &s.Meta {
		t.Fatalf("meta pointer mismatch: got=%p want=%p", gotMeta, &s.Meta)
	}

	// data pontosan a Spec legyen
	if gd, ok := gotData.(SchemaDef); !ok || !reflect.DeepEqual(gd, s.Spec) {
		t.Fatalf("validate data mismatch: got=%#v want=%#v", gotData, s.Spec)
	}
}

// -- Checksum() --

func TestSchema_Checksum_ForwardsAndChangesWithSpec(t *testing.T) {
	s := &Schema{
		Meta: BaseBlock{Name: "sch", Version: "1.0.0"},
		Spec: SchemaDef{
			StateRequirement: map[string]string{"a": "b"},
			Dependencies:     []string{"d1"},
			NextHops:         []string{"n1"},
		},
	}

	// 1) forward equivalence & determinism
	c1, err := s.Checksum()
	if err != nil {
		t.Fatalf("Checksum() err: %v", err)
	}
	c2, err := calculateChecksum(&s.Meta, s.Spec)
	if err != nil {
		t.Fatalf("calculateChecksum err: %v", err)
	}
	if c1 != c2 {
		t.Fatalf("Checksum() != calculateChecksum(): %q vs %q", c1, c2)
	}
	c1b, err := s.Checksum()
	if err != nil || c1b != c1 {
		t.Fatalf("Checksum determinism failed: first=%q second=%q err=%v", c1, c1b, err)
	}

	// 2) Spec változás → checksum változás
	s.Spec.Dependencies = append(s.Spec.Dependencies, "d2")
	c3, err := s.Checksum()
	if err != nil {
		t.Fatalf("Checksum() after spec change err: %v", err)
	}
	if c3 == c1 {
		t.Fatalf("Checksum did not change after spec mutation: before=%q after=%q", c1, c3)
	}
}
