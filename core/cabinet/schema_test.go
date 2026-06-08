package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSchema_Error(t *testing.T) {
	t.Run("validation fails for empty name", func(t *testing.T) {
		identity := SchemaIdentity{Version: "1.0"}
		config := SchemaConfig{} // Name is empty

		schema, err := NewSchema(context.Background(), identity, config, SchemaSigning{})

		assert.Error(t, err, "NewSchema should return an error for invalid metadata")
		assert.Nil(t, schema, "Schema should be nil on error")
		assert.ErrorIs(t, err, ErrInvalid, "Error should be of type ErrInvalid")
	})
}
