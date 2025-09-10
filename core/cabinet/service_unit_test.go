package cabinet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCabinetService_Get_NotFound(t *testing.T) {
	svc := NewCabinetService(NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore())

	_, err := svc.GetSchema("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound, "GetSchema should return ErrNotFound for nonexistent ID")

	_, err = svc.GetModule("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound, "GetModule should return ErrNotFound for nonexistent ID")

	_, err = svc.GetWorkflow("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound, "GetWorkflow should return ErrNotFound for nonexistent ID")
}

func TestCabinetService_Put_ValidateErrors(t *testing.T) {
	svc := NewCabinetService(NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore())

	// Create components with missing required fields (Name/Version)
	invalidSchema := &Schema{}
	invalidModule := &ModuleDescriptor{}
	invalidWorkflow := &Workflow{}

	t.Run("PutSchema validation fail", func(t *testing.T) {
		err := svc.PutSchema(invalidSchema)
		assert.Error(t, err, "PutSchema should fail for invalid schema")
		assert.ErrorIs(t, err, ErrInvalid, "Error should wrap ErrInvalid")
	})

	t.Run("PutModule validation fail", func(t *testing.T) {
		err := svc.PutModule(invalidModule)
		assert.Error(t, err, "PutModule should fail for invalid module")
		assert.ErrorIs(t, err, ErrInvalid, "Error should wrap ErrInvalid")
	})

	t.Run("PutWorkflow validation fail", func(t *testing.T) {
		err := svc.PutWorkflow(invalidWorkflow)
		assert.Error(t, err, "PutWorkflow should fail for invalid workflow")
		assert.ErrorIs(t, err, ErrInvalid, "Error should wrap ErrInvalid")
	})
}

func TestCabinetService_Get_FoundWhenPreStored(t *testing.T) {
	svc := NewCabinetService(NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore())

	schema := &Schema{Meta: BaseBlock{Name: "s1", Version: "v1"}}
	require.NoError(t, svc.PutSchema(schema))

	retrieved, err := svc.GetSchema(schema.ID())
	require.NoError(t, err)
	assert.Equal(t, schema.ID(), retrieved.ID())
}

func TestCabinetService_Put_ValidReturnsNil(t *testing.T) {
	svc := NewCabinetService(NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore())

	schema := &Schema{Meta: BaseBlock{Name: "s1", Version: "v1"}}
	err := svc.PutSchema(schema)
	assert.NoError(t, err, "PutSchema should succeed for a valid schema")
}
