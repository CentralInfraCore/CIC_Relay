package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoreEcho_Success(t *testing.T) {
	ctx := context.Background()

	t.Run("echoes string input", func(t *testing.T) {
		input := "hello world"
		result, err := CoreEcho(ctx, input)
		require.NoError(t, err, "CoreEcho should not return an error for valid input")
		assert.Equal(t, input, result, "CoreEcho should return the same string input")
	})

	t.Run("echoes map input", func(t *testing.T) {
		input := map[string]interface{}{"key": "value", "number": 123}
		result, err := CoreEcho(ctx, input)
		require.NoError(t, err, "CoreEcho should not return an error for valid input")
		assert.Equal(t, input, result, "CoreEcho should return the same map input")
	})

	t.Run("echoes nil input", func(t *testing.T) {
		var input interface{}
		result, err := CoreEcho(ctx, input)
		require.NoError(t, err, "CoreEcho should not return an error for nil input")
		assert.Nil(t, result, "CoreEcho should return nil for nil input")
	})
}
