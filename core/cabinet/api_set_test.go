package cabinet

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCabinet is a mock implementation of the Cabinet interface for testing error propagation.
type mockCabinet struct {
	getWorkflowErr error
	getModuleErr   error
	getSchemaErr   error

	putWorkflowFunc func(wf *Workflow) error
	putModuleFunc   func(md *ModuleDescriptor) error
	putSchemaFunc   func(s *Schema) error

	setFunc   func(ctx context.Context, in *SetPayload) (*SetResponse, error)
	closeFunc func(ctx context.Context) error
}

func (m *mockCabinet) GetWorkflow(id string) (*Workflow, error) {
	if m.getWorkflowErr != nil {
		return nil, m.getWorkflowErr
	}
	// Return a dummy workflow to satisfy checks that don't care about the content
	return &Workflow{Config: WorkflowConfig{Name: "dummy-wf"}, Identity: WorkflowIdentity{Version: "1.0"}}, nil
}

func (m *mockCabinet) GetModule(id string) (*ModuleDescriptor, error) {
	if m.getModuleErr != nil {
		return nil, m.getModuleErr
	}
	return &ModuleDescriptor{Meta: BaseBlock{Name: "dummy-mod", Version: "1.0"}}, nil
}

func (m *mockCabinet) GetSchema(id string) (*Schema, error) {
	if m.getSchemaErr != nil {
		return nil, m.getSchemaErr
	}
	return &Schema{Config: SchemaConfig{Name: "dummy-schema"}, Identity: SchemaIdentity{Version: "1.0"}}, nil
}

func (m *mockCabinet) PutWorkflow(wf *Workflow) error {
	if m.putWorkflowFunc != nil {
		return m.putWorkflowFunc(wf)
	}
	return nil
}
func (m *mockCabinet) PutModule(md *ModuleDescriptor) error {
	if m.putModuleFunc != nil {
		return m.putModuleFunc(md)
	}
	return nil
}
func (m *mockCabinet) PutSchema(s *Schema) error {
	if m.putSchemaFunc != nil {
		return m.putSchemaFunc(s)
	}
	return nil
}

func (m *mockCabinet) Setx(ctx context.Context, in *SetPayload) (*SetResponse, error) {
	if m.setFunc != nil {
		return m.setFunc(ctx, in)
	}
	return nil, nil
}

func (m *mockCabinet) Close(ctx context.Context) error {
	if m.closeFunc != nil {
		return m.closeFunc(ctx)
	}
	return nil
}

func TestAPI_Set_NilPayload(t *testing.T) {
	api := &API{}
	_, err := api.Set(context.Background(), nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "nil request")
}

func TestAPI_Set_ValidationError(t *testing.T) {
	// Use a real service here, but provide a payload that will fail validation
	config := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := NewCabinetService(context.Background(), NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(), config)
	require.NoError(t, err)
	api := NewAPI(svc)

	payload := &SetPayload{WorkflowID: ""} // Invalid: empty workflow ID

	_, err = api.Set(context.Background(), payload)
	assert.Error(t, err, "Set should propagate validation errors")
	assert.ErrorIs(t, err, ErrInvalid, "Error should be a validation error")
}

func TestAPI_ValidateSet_NilPayload(t *testing.T) {
	api := &API{}
	err := api.ValidateSet(context.Background(), nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "set payload is nil")
}

func TestAPI_ValidateSet_StoreErrors(t *testing.T) {
	ctx := context.Background()
	genericErr := errors.New("a generic store error")

	t.Run("GetWorkflow returns generic error", func(t *testing.T) {
		mockSvc := &mockCabinet{getWorkflowErr: genericErr}
		api := NewAPI(mockSvc)
		payload := &SetPayload{WorkflowID: "wf-1", Modules: []ModuleID{"mod-1"}}

		err := api.ValidateSet(ctx, payload)

		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalid, "Should be the raw store error, not a validation error")
		assert.Equal(t, genericErr, err)
	})

	t.Run("GetModule returns generic error", func(t *testing.T) {
		mockSvc := &mockCabinet{
			getWorkflowErr: nil, // Ensure workflow lookup passes
			getModuleErr:   genericErr,
		}
		// Put a valid workflow so the first check passes
		// This is not needed anymore as mockCabinet handles it

		api := NewAPI(mockSvc)
		payload := &SetPayload{WorkflowID: "wf-1@1.0", Modules: []ModuleID{"mod-1"}}

		err := api.ValidateSet(ctx, payload)

		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalid)
		assert.Equal(t, genericErr, err)
	})
}

func TestAPI_ValidateSet_Options(t *testing.T) {
	api := NewAPI(&mockCabinet{}) // API needs a Cabinet to resolve workflows and modules

	t.Run("options nil should pass", func(t *testing.T) {
		payload := &SetPayload{
			WorkflowID: "wf-1@1.0",
			Modules:    []ModuleID{"mod-1"},
			Options:    nil, // Explicitly nil options
		}
		err := api.ValidateSet(context.Background(), payload)
		assert.NoError(t, err)
	})

	t.Run("options with valid priority should pass", func(t *testing.T) {
		payload := &SetPayload{
			WorkflowID: "wf-1@1.0",
			Modules:    []ModuleID{"mod-1"},
			Options:    &SetOptions{Priority: "low"},
		}
		err := api.ValidateSet(context.Background(), payload)
		assert.NoError(t, err)

		payload = &SetPayload{
			WorkflowID: "wf-1@1.0",
			Modules:    []ModuleID{"mod-1"},
			Options:    &SetOptions{Priority: "normal"},
		}
		err = api.ValidateSet(context.Background(), payload)
		assert.NoError(t, err)

		payload = &SetPayload{
			WorkflowID: "wf-1@1.0",
			Modules:    []ModuleID{"mod-1"},
			Options:    &SetOptions{Priority: "high"},
		}
		err = api.ValidateSet(context.Background(), payload)
		assert.NoError(t, err)
	})

	t.Run("options with invalid priority should fail", func(t *testing.T) {
		payload := &SetPayload{
			WorkflowID: "wf-1@1.0",
			Modules:    []ModuleID{"mod-1"},
			Options:    &SetOptions{Priority: "invalid"},
		}
		err := api.ValidateSet(context.Background(), payload)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "options.priority invalid")
	})
}
