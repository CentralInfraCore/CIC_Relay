package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPI_Close_NilCabinet(t *testing.T) {
	api := NewAPI(nil)

	// Calling Close on a nil Cabinet should panic
	assert.Panics(t, func() {
		api.Close(context.Background())
	}, "Calling Close on a nil Cabinet should panic")
}
