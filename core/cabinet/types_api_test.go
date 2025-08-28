package cabinet_test

import (
	"reflect"
	"testing"

	"centralrelay/core/cabinet"
)

func TestBaseBlock_API_Shape(t *testing.T) {
	typ := reflect.TypeOf(cabinet.BaseBlock{})
	if got, want := typ.NumField(), 10; got != want {
		t.Fatalf("BaseBlock field count = %d, want %d", got, want)
	}
	want := []struct {
		name string
		kind reflect.Kind
	}{
		{"Name", reflect.String},
		{"Version", reflect.String},
		{"Description", reflect.String},
		{"CreatedBy", reflect.String},
		{"Timestamp", reflect.String},
		{"Checksum", reflect.String},
		{"Signature", reflect.String},
		{"Certificate", reflect.String},
		{"CRLRef", reflect.String},
		{"Valid", reflect.Bool},
	}
	for i, w := range want {
		f := typ.Field(i)
		if f.Name != w.name {
			t.Fatalf("BaseBlock field[%d] name = %q, want %q", i, f.Name, w.name)
		}
		if f.Type.Kind() != w.kind {
			t.Fatalf("BaseBlock field[%d] %q kind = %v, want %v", i, f.Name, f.Type.Kind(), w.kind)
		}
	}

	// zero value sanity (black-box)
	var bb cabinet.BaseBlock
	if bb.Valid {
		t.Fatalf("BaseBlock zero value: Valid should be false")
	}
	if bb.Checksum != "" || bb.Name != "" || bb.Version != "" {
		t.Fatalf("BaseBlock zero value strings should be empty, got: %+v", bb)
	}
}

func TestSchemaDef_API_ShapeAndZeroValue(t *testing.T) {
	typ := reflect.TypeOf(cabinet.SchemaDef{})
	if got, want := typ.NumField(), 3; got != want {
		t.Fatalf("SchemaDef field count = %d, want %d", got, want)
	}
	want := []struct {
		name string
		kind reflect.Kind
	}{
		{"StateRequirement", reflect.Map},
		{"Dependencies", reflect.Slice},
		{"NextHops", reflect.Slice},
	}
	for i, w := range want {
		f := typ.Field(i)
		if f.Name != w.name || f.Type.Kind() != w.kind {
			t.Fatalf("SchemaDef field[%d] = (%q, %v), want (%q, %v)", i, f.Name, f.Type.Kind(), w.name, w.kind)
		}
	}

	var sd cabinet.SchemaDef
	if sd.StateRequirement != nil {
		t.Fatalf("SchemaDef zero value: StateRequirement should be nil")
	}
	if sd.Dependencies != nil || sd.NextHops != nil {
		t.Fatalf("SchemaDef zero value: slices should be nil")
	}
}

func TestSchema_API_Shape(t *testing.T) {
	typ := reflect.TypeOf(cabinet.Schema{})
	if got, want := typ.NumField(), 2; got != want {
		t.Fatalf("Schema field count = %d, want %d", got, want)
	}
	f0 := typ.Field(0)
	f1 := typ.Field(1)
	if f0.Name != "Meta" || f0.Type != reflect.TypeOf(cabinet.BaseBlock{}) {
		t.Fatalf("Schema.Meta type/name mismatch: %+v", f0)
	}
	if f1.Name != "Spec" || f1.Type != reflect.TypeOf(cabinet.SchemaDef{}) {
		t.Fatalf("Schema.Spec type/name mismatch: %+v", f1)
	}
}

func TestModuleDescriptor_API_Shape(t *testing.T) {
	typ := reflect.TypeOf(cabinet.ModuleDescriptor{})
	if got, want := typ.NumField(), 4; got != want {
		t.Fatalf("ModuleDescriptor field count = %d, want %d", got, want)
	}
	want := []struct {
		name string
		typ  reflect.Type
	}{
		{"Meta", reflect.TypeOf(cabinet.BaseBlock{})},
		{"Type", reflect.TypeOf("")},
		{"Accepts", reflect.TypeOf([]string(nil))},
		{"OutputKeys", reflect.TypeOf([]string(nil))},
	}
	for i, w := range want {
		f := typ.Field(i)
		if f.Name != w.name || f.Type != w.typ {
			t.Fatalf("ModuleDescriptor field[%d] = (%q, %v), want (%q, %v)", i, f.Name, f.Type, w.name, w.typ)
		}
	}

	var md cabinet.ModuleDescriptor
	if md.Accepts != nil || md.OutputKeys != nil {
		t.Fatalf("ModuleDescriptor zero value: slices should be nil")
	}
}

func TestWorkflow_API_Shape(t *testing.T) {
	typ := reflect.TypeOf(cabinet.Workflow{})
	if got, want := typ.NumField(), 2; got != want {
		t.Fatalf("Workflow field count = %d, want %d", got, want)
	}
	f0 := typ.Field(0)
	f1 := typ.Field(1)
	if f0.Name != "Meta" || f0.Type != reflect.TypeOf(cabinet.BaseBlock{}) {
		t.Fatalf("Workflow.Meta type/name mismatch: %+v", f0)
	}
	if f1.Name != "Steps" || f1.Type != reflect.TypeOf([]string(nil)) {
		t.Fatalf("Workflow.Steps type/name mismatch: %+v", f1)
	}

	var wf cabinet.Workflow
	if wf.Steps != nil {
		t.Fatalf("Workflow zero value: Steps slice should be nil")
	}
}
