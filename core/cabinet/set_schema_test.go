package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalizeSetPayload(t *testing.T) {
	in := &SetPayload{
		WorkflowID: "  wf-1  ",
		Modules:    []ModuleID{"m2", "m1", "m2"},
		Options:    &SetOptions{},
	}
	CanonicalizeSetPayload(in)

	if got, want := in.WorkflowID, "wf-1"; got != want {
		t.Fatalf("workflow trim failed: got %q want %q", got, want)
	}
	if len(in.Modules) != 2 || string(in.Modules[0]) != "m1" || string(in.Modules[1]) != "m2" {
		t.Fatalf("modules dedup/sort failed: %+v", in.Modules)
	}
	if in.Options.Priority != "normal" {
		t.Fatalf("default priority failed: %q", in.Options.Priority)
	}
}

func TestValidateSet_Required(t *testing.T) {
	// NOTE: This test doesn't need a fully initialized API/Cabinet service
	// as it tests the initial nil/empty checks.
	api := &API{}
	ctx := context.Background()

	testCases := []struct {
		name    string
		payload *SetPayload
	}{
		{
			name: "empty workflow_id",
			payload: &SetPayload{
				WorkflowID: "",
				Modules:    []ModuleID{"m1"},
			},
		},
		{
			name: "empty modules",
			payload: &SetPayload{
				WorkflowID: "wf",
				Modules:    nil,
			},
		},
		{
			name: "invalid priority",
			payload: &SetPayload{
				WorkflowID: "wf",
				Modules:    []ModuleID{"m1"},
				Options:    &SetOptions{Priority: "weird"},
			},
		},
	}

	for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				if err := api.ValidateSet(ctx, tc.payload); err == nil {
					t.Fatalf("expected error, got nil")
				}
			})
	}
}

func TestValidateSet_NotFound(t *testing.T) {
	// Setup: create stores and populate them with valid objects.
	wfStore := NewMemWorkflowStore()
	wf, err := NewWorkflow(context.Background(), BaseBlock{Name: "wf-exists", Version: "v1"}, []string{"step1"})
	require.NoError(t, err)
	wfStore.Put(wf)

	modStore := NewMemModuleStore()
	mod, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: "mod-exists", Version: "v1"}, "native/test", nil, nil, nil)
	require.NoError(t, err)
	modStore.Put(mod)

	schemaStore := NewMemSchemaStore()
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := NewCabinetService(context.Background(), schemaStore, modStore, wfStore, config)
	require.NoError(t, err)

	api := NewAPI(svc)
	ctx := context.Background()

	testCases := []struct {
		name    string
		payload *SetPayload
	}{
		{
			name: "nonexistent workflow_id",
			payload: &SetPayload{
				WorkflowID: "wf-nonexistent",
				Modules:    []ModuleID{"mod-exists@v1"},
			},
		},
		{
			name: "nonexistent module_id",
			payload: &SetPayload{
				WorkflowID: "wf-exists@v1",
				Modules:    []ModuleID{"mod-nonexistent"},
			},
		},
	}

	for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				err := api.ValidateSet(ctx, tc.payload)
				require.Error(t, err)
				t.Logf("Received error: %v (type: %T), ErrNotFound address: %p, received error address: %p", err, err, ErrNotFound, err)
				require.True(t, IsNotFound(err), "Expected error to be ErrNotFound")
			})
	}
}
