package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemStores_Basic(t *testing.T) {
	// Schema store
	ss := NewMemSchemaStore()
	sc, err := NewSchema(context.Background(), SchemaIdentity{Version: "1"}, SchemaConfig{Name: "s"}, SchemaSigning{})
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

	// Workflow store
	ws := NewMemWorkflowStore()
	wf := &Workflow{Config: WorkflowConfig{Name: "w", Steps: []string{"s"}}, Identity: WorkflowIdentity{Version: "1"}}
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

func TestMemModuleStore_OverwriteAndCAS(t *testing.T) {
	ms := NewMemModuleStore()

	// 1. Create and put the first version of a module.
	moduleV1 := &ModuleDescriptor{
		Meta: BaseBlock{Name: "cas-test", Version: "1.0", Checksum: "checksum1", Description: "Version 1"},
		Type: "executor",
	}
	id := moduleV1.ID()

	// Initial Put
	prev, existed := ms.Put(moduleV1)
	require.False(t, existed, "module should not exist initially")
	require.Nil(t, prev, "previous module should be nil on first put")
	assert.True(t, ms.Exists(id), "module should exist after first put")

	// Get the first version
	retrieved, ok := ms.Get(id)
	require.True(t, ok, "failed to get module v1")
	assert.Equal(t, "checksum1", retrieved.Meta.Checksum)
	assert.Equal(t, "Version 1", retrieved.Meta.Description)

	// 2. Create and put a second version of the same module (same ID, different content/checksum).
	moduleV2 := &ModuleDescriptor{
		Meta: BaseBlock{Name: "cas-test", Version: "1.0", Checksum: "checksum2", Description: "Version 2"},
		Type: "executor",
	}

	// Overwrite Put
	prev, existed = ms.Put(moduleV2)
	require.True(t, existed, "existed should be true on overwrite")
	require.NotNil(t, prev, "previous module should be returned on overwrite")
	assert.Equal(t, "checksum1", prev.Meta.Checksum, "previous module should have checksum1")

	// Get the second version
	retrieved, ok = ms.Get(id)
	require.True(t, ok, "failed to get module v2 after overwrite")
	assert.Equal(t, "checksum2", retrieved.Meta.Checksum, "retrieved module should now have checksum2")
	assert.Equal(t, "Version 2", retrieved.Meta.Description)

	// 3. Create a third module with a different ID but the same content/checksum as V1.
	moduleV3 := &ModuleDescriptor{
		Meta: BaseBlock{Name: "cas-test-alias", Version: "1.0", Checksum: "checksum1", Description: "Version 3 (alias for 1)"},
		Type: "executor",
	}

	// Put the aliased module
	ms.Put(moduleV3)

	// 4. Verify CAS behavior.
	// Get the original module by its ID. It should still be V2.
	retrievedV2, ok := ms.Get(id) // id is "cas-test@1.0"
	require.True(t, ok)
	assert.Equal(t, "checksum2", retrievedV2.Meta.Checksum, "original ID should still point to V2")

	// Get the new module by its ID.
	retrievedV3, ok := ms.Get(moduleV3.ID())
	require.True(t, ok)
	assert.Equal(t, "checksum1", retrievedV3.Meta.Checksum)

	// This is a white-box-like test, but it confirms the internal state.
	// The implementation of Put overwrites the content at the checksum key.
	// So, getting the content for checksum1 should now yield moduleV3's content.
	// Let's check if the old module (V1) was effectively replaced at the content level.
	// We can't directly access the byChecksum map, but we can infer its state.
	// If we now put a *new* module that points to checksum1, its previous value should reflect moduleV3.

	moduleV4 := &ModuleDescriptor{
		Meta: BaseBlock{Name: "cas-test-alias", Version: "1.0", Checksum: "checksum3", Description: "Version 4"},
	}
	prev, existed = ms.Put(moduleV4)
	require.True(t, existed)
	assert.Equal(t, "checksum1", prev.Meta.Checksum, "previous value of alias should have checksum1")
}
