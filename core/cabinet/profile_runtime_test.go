package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SetModuleProfile is the runtime registration of an admitted binding (INT-05): set marks the module as profile-bound
// and required, nil clears both.
func TestSetModuleProfileSetsAndClearsTheBinding(t *testing.T) {
	svc, err := NewCabinetService(context.Background(), NewMemSchemaStore(), NewMemModuleStore(), NewMemWorkflowStore(), WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1})
	require.NoError(t, err)
	c := svc.(*cabinetService)

	_, ok := c.ModuleProfile("m@1.0.0")
	assert.False(t, ok)

	b := &ModuleProfileBinding{ModuleID: "m@1.0.0", ProfileID: "p"}
	c.SetModuleProfile("m@1.0.0", b)
	got, ok := c.ModuleProfile("m@1.0.0")
	require.True(t, ok)
	assert.Same(t, b, got)
	assert.True(t, c.profileRequired["m@1.0.0"], "a bound module needs its profile")

	c.SetModuleProfile("m@1.0.0", nil)
	_, ok = c.ModuleProfile("m@1.0.0")
	assert.False(t, ok)
	assert.False(t, c.profileRequired["m@1.0.0"], "cleared together with the binding")

	c.SetModuleProfile("never-set", nil) // clearing an absent module is a no-op, not a panic
}
