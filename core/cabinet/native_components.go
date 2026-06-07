package cabinet

import (
	"context"
)

// CoreEcho is a simple native function for testing.
// It takes any data as input and returns it unmodified, acting as an echo.
func CoreEcho(ctx context.Context, data interface{}) (interface{}, error) {
	return data, nil
}
