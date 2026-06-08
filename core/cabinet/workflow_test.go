package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWorkflow_Error(t *testing.T) {
	t.Run("validation fails for empty name", func(t *testing.T) {
		identity := WorkflowIdentity{Version: "1.0"}
		config := WorkflowConfig{Steps: []string{"step1"}} // Name is empty

		workflow, err := NewWorkflow(context.Background(), identity, config, WorkflowSigning{})

		assert.Error(t, err, "NewWorkflow should return an error for invalid metadata")
		assert.Nil(t, workflow, "Workflow should be nil on error")
		assert.ErrorIs(t, err, ErrInvalid, "Error should be of type ErrInvalid")
	})

	t.Run("empty steps should fail", func(t *testing.T) {
		_, err := NewWorkflow(context.Background(),
			WorkflowIdentity{Version: "1.0"},
			WorkflowConfig{Name: "test-wf"},
			WorkflowSigning{})
		require.Error(t, err)
		assert.EqualError(t, err, "workflow must have at least one step")
	})
}
