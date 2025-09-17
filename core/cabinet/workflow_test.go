package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewWorkflow_Error(t *testing.T) {
	t.Run("validation fails for empty name", func(t *testing.T) {
		// Create a BaseBlock with a missing Name to trigger the validation error
		meta := BaseBlock{Version: "1.0"}
		steps := []string{"step1"}

		workflow, err := NewWorkflow(context.Background(), meta, steps)

		assert.Error(t, err, "NewWorkflow should return an error for invalid metadata")
		assert.Nil(t, workflow, "Workflow should be nil on error")
		assert.ErrorIs(t, err, ErrInvalid, "Error should be of type ErrInvalid")
	})
}
