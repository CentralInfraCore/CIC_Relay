package cabinet

import (
	"context"
	"errors"
	"testing"
)

// TestSetx_SchemaValidation_Integration verifies that Setx enforces the Accepts
// schema constraint on each workflow step's input data.
func TestSetx_SchemaValidation_Integration(t *testing.T) {
	ctx := context.Background()

	// helper: build a minimal cabinetService backed by mem stores + mock wasm manager
	newSvc := func() *cabinetService {
		mockWM := new(MockWasmManager)
		return &cabinetService{
			schemas:     NewMemSchemaStore(),
			modules:     NewMemModuleStore(),
			workflows:   NewMemWorkflowStore(),
			wasmManager: mockWM,
		}
	}

	t.Run("module with no Accepts passes any input", func(t *testing.T) {
		svc := newSvc()

		// Native echo module with no Accepts restriction.
		native := make(NativeFunctions)
		native["echo"] = CoreEcho
		mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "echo", Version: "1.0"}, "native", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		mod.NativeImpl = native
		svc.modules.Put(mod)

		wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.noschema", Version: "1.0"}, []string{"echo@1.0.echo(input)"})
		if err != nil {
			t.Fatal(err)
		}
		svc.workflows.Put(wf)

		_, err = svc.Setx(ctx, &SetPayload{
			WorkflowID: wf.ID(),
			Payload:    map[string]any{"input": map[string]any{"msg": "hello"}},
		})
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	})

	t.Run("module with Accepts rejects input without $schema", func(t *testing.T) {
		svc := newSvc()

		native := make(NativeFunctions)
		native["echo"] = CoreEcho
		mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "typed", Version: "1.0"}, "native", []string{"deploy@1.0"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		mod.NativeImpl = native
		svc.modules.Put(mod)

		wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.typed", Version: "1.0"}, []string{"typed@1.0.echo(input)"})
		if err != nil {
			t.Fatal(err)
		}
		svc.workflows.Put(wf)

		_, err = svc.Setx(ctx, &SetPayload{
			WorkflowID: wf.ID(),
			Payload:    map[string]any{"input": map[string]any{"msg": "hello"}},
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("module with Accepts passes input with matching $schema", func(t *testing.T) {
		svc := newSvc()

		native := make(NativeFunctions)
		native["echo"] = CoreEcho
		mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "typed2", Version: "1.0"}, "native", []string{"deploy@1.0"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		mod.NativeImpl = native
		svc.modules.Put(mod)

		wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.typed2", Version: "1.0"}, []string{"typed2@1.0.echo(input)"})
		if err != nil {
			t.Fatal(err)
		}
		svc.workflows.Put(wf)

		// The SetPayload.Payload is stored under "input" in the executionContext,
		// so "$schema" must live at the top level of Payload (not nested).
		_, err = svc.Setx(ctx, &SetPayload{
			WorkflowID: wf.ID(),
			Payload:    map[string]any{"$schema": "deploy@1.0", "host": "prod"},
		})
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	})

	t.Run("module with Accepts rejects non-matching $schema", func(t *testing.T) {
		svc := newSvc()

		native := make(NativeFunctions)
		native["echo"] = CoreEcho
		mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "typed3", Version: "1.0"}, "native", []string{"deploy@1.0"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		mod.NativeImpl = native
		svc.modules.Put(mod)

		wf, err := NewWorkflow(ctx, BaseBlock{Name: "wf.typed3", Version: "1.0"}, []string{"typed3@1.0.echo(input)"})
		if err != nil {
			t.Fatal(err)
		}
		svc.workflows.Put(wf)

		_, err = svc.Setx(ctx, &SetPayload{
			WorkflowID: wf.ID(),
			Payload:    map[string]any{"$schema": "infra@2.0", "host": "prod"},
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})
}