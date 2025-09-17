package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSchema_Error(t *testing.T) {
	t.Run("validation fails for empty name", func(t *testing.T) {
		// Create a BaseBlock with a missing Name to trigger the validation error
		meta := BaseBlock{Version: "1.0"}
		spec := SchemaDef{}

		schema, err := NewSchema(context.Background(), meta, spec)

		assert.Error(t, err, "NewSchema should return an error for invalid metadata")
		assert.Nil(t, schema, "Schema should be nil on error")
		assert.ErrorIs(t, err, ErrInvalid, "Error should be of type ErrInvalid")
	})
}
