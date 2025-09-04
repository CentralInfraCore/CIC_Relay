package cabinet

import (
	"errors"
	"testing"
)

func TestCabinetService_Get_NotFound(t *testing.T) {
	s := NewMemSchemaStore()
	m := NewMemModuleStore()
	w := NewMemWorkflowStore()
	c := NewCabinetService(s, m, w)

	if _, err := c.GetSchema("nope@1"); err == nil || !IsNotFound(err) {
		t.Fatalf("GetSchema: want ErrNotFound, got %v", err)
	}
	if _, err := c.GetModule("nope@1"); err == nil || !IsNotFound(err) {
		t.Fatalf("GetModule: want ErrNotFound, got %v", err)
	}
	if _, err := c.GetWorkflow("nope@1"); err == nil || !IsNotFound(err) {
		t.Fatalf("GetWorkflow: want ErrNotFound, got %v", err)
	}
}

func TestCabinetService_Put_ValidateErrors(t *testing.T) {
	s := NewMemSchemaStore()
	m := NewMemModuleStore()
	w := NewMemWorkflowStore()
	c := NewCabinetService(s, m, w)

	// invalid schema (missing name/version) -> *ValidationError
	sc := &Schema{Meta: BaseBlock{}, Spec: SchemaDef{}}
	if err := c.PutSchema(sc); err == nil {
		t.Fatalf("PutSchema: want error")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("PutSchema: want ValidationError, got %T: %v", err, err)
		}
	}

	// invalid module -> *ValidationError
	mod := &ModuleDescriptor{Meta: BaseBlock{}}
	if err := c.PutModule(mod); err == nil {
		t.Fatalf("PutModule: want error")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("PutModule: want ValidationError, got %T: %v", err, err)
		}
	}

	// invalid workflow -> *ValidationError
	wf := &Workflow{Meta: BaseBlock{}}
	if err := c.PutWorkflow(wf); err == nil {
		t.Fatalf("PutWorkflow: want error")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("PutWorkflow: want ValidationError, got %T: %v", err, err)
		}
	}
}

func TestCabinetService_Get_FoundWhenPreStored(t *testing.T) {
	s := NewMemSchemaStore()
	m := NewMemModuleStore()
	w := NewMemWorkflowStore()
	c := NewCabinetService(s, m, w)

	// Pre-store valid entities directly in stores, then get via service.
	meta := BaseBlock{Name: "sch", Version: "1"}
	spec := SchemaDef{}
	sc, err := NewSchema(meta, spec)
	if err != nil {
		t.Fatalf("NewSchema err: %v", err)
	}
	sid := sc.ID()
	if prev, existed := s.Put(sc); existed || prev != nil {
		t.Fatalf("unexpected existed for schema")
	}
	gotS, err := c.GetSchema(sid)
	if err != nil || gotS == nil {
		t.Fatalf("GetSchema error: %v", err)
	}

	mod := &ModuleDescriptor{Meta: BaseBlock{Name: "mod", Version: "1"}, Type: "executor"}
	mid := mod.ID()
	m.Put(mod)
	gotM, err := c.GetModule(mid)
	if err != nil || gotM == nil {
		t.Fatalf("GetModule error: %v", err)
	}

	wf := &Workflow{Meta: BaseBlock{Name: "wf", Version: "1"}, Steps: []string{"sch"}}
	wid := wf.ID()
	w.Put(wf)
	gotW, err := c.GetWorkflow(wid)
	if err != nil || gotW == nil {
		t.Fatalf("GetWorkflow error: %v", err)
	}
}
