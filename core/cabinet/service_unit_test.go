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
		workflow, err := NewWorkflow(context.Background(), BaseBlock{Name: "wf1", Version: "v1"}, []string{"step1"})
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

func TestCabinetService_Close(t *testing.T) {
	svc := NewCabinetService(NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore())

	// Call Close and assert no error is returned.
	// The actual implementation of Close for MemStores is a no-op, so it should always succeed.
	err := svc.Close(context.Background())
	assert.NoError(t, err, "Close should not return an error")
}
