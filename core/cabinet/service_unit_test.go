package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCabinetService_PutAndGet_Success verifies the core Put/Get functionality of the service.
// It uses the correct New... constructors to create valid domain objects, ensuring that
// the service correctly stores and retrieves objects that adhere to the new architecture.
func TestCabinetService_PutAndGet_Success(t *testing.T) {
	svc := NewCabinetService(NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore())

	t.Run("Schema Put/Get", func(t *testing.T) {
		schema, err := NewSchema(context.Background(), BaseBlock{Name: "s1", Version: "v1"}, SchemaDef{})
		require.NoError(t, err, "NewSchema should succeed for valid input")

		err = svc.PutSchema(schema)
		assert.NoError(t, err, "PutSchema should succeed for a valid schema")

		retrieved, err := svc.GetSchema(schema.ID())
		require.NoError(t, err, "GetSchema should find the stored schema")
		assert.Equal(t, schema.ID(), retrieved.ID())
		assert.Equal(t, schema.Checksum(), retrieved.Checksum(), "Checksum of retrieved object should match")
	})

	t.Run("Module Put/Get", func(t *testing.T) {
		module, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: "m1", Version: "v1"}, "executor", nil, nil, nil)
		require.NoError(t, err, "NewModuleDescriptor should succeed for valid input")

		err = svc.PutModule(module)
		assert.NoError(t, err, "PutModule should succeed for a valid module")

		retrieved, err := svc.GetModule(module.ID())
		require.NoError(t, err, "GetModule should find the stored module")
		assert.Equal(t, module.ID(), retrieved.ID())
		assert.Equal(t, module.Checksum(), retrieved.Checksum(), "Checksum of retrieved object should match")
	})

	t.Run("Workflow Put/Get", func(t *testing.T) {
		workflow, err := NewWorkflow(context.Background(), BaseBlock{Name: "wf1", Version: "v1"}, nil)
		require.NoError(t, err, "NewWorkflow should succeed for valid input")

		err = svc.PutWorkflow(workflow)
		assert.NoError(t, err, "PutWorkflow should succeed for a valid workflow")

		retrieved, err := svc.GetWorkflow(workflow.ID())
		require.NoError(t, err, "GetWorkflow should find the stored workflow")
		assert.Equal(t, workflow.ID(), retrieved.ID())
		assert.Equal(t, workflow.Checksum(), retrieved.Checksum(), "Checksum of retrieved object should match")
	})
}

func TestCabinetService_Get_NotFound(t *testing.T) {
	svc := NewCabinetService(NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore())

	_, err := svc.GetSchema("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound, "GetSchema should return ErrNotFound for nonexistent ID")

	_, err = svc.GetModule("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound, "GetModule should return ErrNotFound for nonexistent ID")

	_, err = svc.GetWorkflow("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound, "GetWorkflow should return ErrNotFound for nonexistent ID")
}

func TestCabinetService_Set_Success(t *testing.T) {
	svc := NewCabinetService(NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore())

	// 1. Setup test data: workflow, schema, module
	workflowID := "wf.test@1.0"
	schemaID := "schema.step1@1.0"
	moduleID := "mod.test@1.0"

	// Create and put schema
	schema, err := NewSchema(context.Background(), BaseBlock{Name: "schema.step1", Version: "1.0"}, SchemaDef{})
	require.NoError(t, err)
	require.NoError(t, svc.PutSchema(schema))

	// Create and put module that accepts the schema
	module, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: "mod.test", Version: "1.0"}, "executor", []string{schemaID}, nil, nil)
	require.NoError(t, err)
	require.NoError(t, svc.PutModule(module))

	// Create and put workflow that uses the schema
	workflow, err := NewWorkflow(context.Background(), BaseBlock{Name: "wf.test", Version: "1.0"}, []string{schemaID})
	require.NoError(t, err)
	require.NoError(t, svc.PutWorkflow(workflow))

	// 2. Create SetPayload
	payload := &SetPayload{
		WorkflowID: workflowID,
		Modules:    []ModuleID{ModuleID(moduleID)},
		Payload:    map[string]any{"key": "value"},
	}

	// 3. Call Set method
	resp, err := svc.Set(context.Background(), payload)

	// 4. Assert results
	assert.NoError(t, err, "Set should not return an error for a valid payload")
	assert.NotNil(t, resp, "SetResponse should not be nil")
	assert.Empty(t, resp.Results, "Results should be empty for current Set implementation")
	assert.Empty(t, resp.Warnings, "Warnings should be empty for current Set implementation")
}

func TestCabinetService_Set_Errors(t *testing.T) {
	svc := NewCabinetService(NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore())

	// Setup common test data for successful cases
	workflowID := "wf.test@1.0"
	schemaID := "schema.step1@1.0"
	moduleID := "mod.test@1.0"

	// Create and put schema
	schema, err := NewSchema(context.Background(), BaseBlock{Name: "schema.step1", Version: "1.0"}, SchemaDef{})
	require.NoError(t, err)
	require.NoError(t, svc.PutSchema(schema))

	// Create and put module that accepts the schema
	module, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: "mod.test", Version: "1.0"}, "executor", []string{schemaID}, nil, nil)
	require.NoError(t, err)
	require.NoError(t, svc.PutModule(module))

	// Create and put workflow that uses the schema
	workflow, err := NewWorkflow(context.Background(), BaseBlock{Name: "wf.test", Version: "1.0"}, []string{schemaID})
	require.NoError(t, err)
	require.NoError(t, svc.PutWorkflow(workflow))

	t.Run("workflow not found", func(t *testing.T) {
		payload := &SetPayload{
			WorkflowID: "nonexistent.wf@1.0",
			Modules:    []ModuleID{ModuleID(moduleID)},
			Payload:    map[string]any{"key": "value"},
		}
		_, err := svc.Set(context.Background(), payload)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to resolve workflow", "Error message should indicate workflow not found")
		assert.ErrorIs(t, err, ErrNotFound, "Error should wrap ErrNotFound")
	})

	t.Run("schema not found in workflow steps", func(t *testing.T) {
		// Create a workflow that references a nonexistent schema
		badWorkflow, err := NewWorkflow(context.Background(), BaseBlock{Name: "wf.bad", Version: "1.0"}, []string{"nonexistent.schema@1.0"})
		require.NoError(t, err)
		require.NoError(t, svc.PutWorkflow(badWorkflow))

		payload := &SetPayload{
			WorkflowID: badWorkflow.ID(),
			Modules:    []ModuleID{ModuleID(moduleID)},
			Payload:    map[string]any{"key": "value"},
		}
		_, err = svc.Set(context.Background(), payload)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "could not resolve step schema", "Error message should indicate schema not found")
		assert.ErrorIs(t, err, ErrNotFound, "Error should wrap ErrNotFound")
	})

	t.Run("module not found in payload", func(t *testing.T) {
		payload := &SetPayload{
			WorkflowID: workflowID,
			Modules:    []ModuleID{"nonexistent.mod@1.0"},
			Payload:    map[string]any{"key": "value"},
		}
		_, err := svc.Set(context.Background(), payload)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "could not resolve provided module", "Error message should indicate module not found")
		assert.ErrorIs(t, err, ErrNotFound, "Error should wrap ErrNotFound")
	})

	t.Run("no suitable module for schema", func(t *testing.T) {
		// Create a module that does NOT accept schema.step1
		otherModule, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: "mod.other", Version: "1.0"}, "executor", []string{"schema.other@1.0"}, nil, nil)
		require.NoError(t, err)
		require.NoError(t, svc.PutModule(otherModule))

		payload := &SetPayload{
			WorkflowID: workflowID,
			Modules:    []ModuleID{ModuleID(otherModule.ID())},
			Payload:    map[string]any{"key": "value"},
		}
		_, err = svc.Set(context.Background(), payload)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no suitable module provided for schema", "Error message should indicate no suitable module")
		assert.ErrorIs(t, err, ErrInvalid, "Error should wrap ErrInvalid")
	})
}
