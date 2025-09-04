package cabinet

import "testing"

func TestMemStores_Basic(t *testing.T) {
	// Schema store
	ss := NewMemSchemaStore()
	meta := BaseBlock{Name: "s", Version: "1"}
	sc, err := NewSchema(meta, SchemaDef{})
	if err != nil {
		t.Fatalf("NewSchema err: %v", err)
	}
	id := sc.ID()
	if ss.Exists(id) {
		t.Fatal("schema should not exist initially")
	}
	prev, existed := ss.Put(sc)
	if existed || prev != nil {
		t.Fatalf("schema put: unexpected existed=%v prev=%v", existed, prev)
	}
	if !ss.Exists(id) {
		t.Fatal("schema should exist after Put")
	}
	g, ok := ss.Get(id)
	if !ok || g == nil {
		t.Fatal("schema Get failed")
	}
	// overwrite
	prev, existed = ss.Put(sc)
	if !existed || prev == nil {
		t.Fatalf("schema second put: want existed with prev")
	}

	// Module store
	ms := NewMemModuleStore()
	mod := &ModuleDescriptor{Meta: BaseBlock{Name: "m", Version: "1"}, Type: "executor"}
	mid := mod.ID()
	if ms.Exists(mid) {
		t.Fatal("module should not exist initially")
	}
	mprev, mex := ms.Put(mod)
	if mex || mprev != nil {
		t.Fatalf("module put: unexpected")
	}
	if !ms.Exists(mid) {
		t.Fatal("module should exist after Put")
	}
	if g, ok := ms.Get(mid); !ok || g == nil {
		t.Fatal("module Get failed")
	}

	// Workflow store
	ws := NewMemWorkflowStore()
	wf := &Workflow{Meta: BaseBlock{Name: "w", Version: "1"}, Steps: []string{"s"}}
	wid := wf.ID()
	if ws.Exists(wid) {
		t.Fatal("workflow should not exist initially")
	}
	wprev, wex := ws.Put(wf)
	if wex || wprev != nil {
		t.Fatalf("workflow put: unexpected")
	}
	if !ws.Exists(wid) {
		t.Fatal("workflow should exist after Put")
	}
	if g, ok := ws.Get(wid); !ok || g == nil {
		t.Fatal("workflow Get failed")
	}
}
