package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AC-03-5: the generic ABI-profile dispatch is off unless explicitly, and exactly, "true".
func TestGenericDispatchFromEnv(t *testing.T) {
	for _, c := range []struct {
		set  bool
		val  string
		want bool
		err  bool
	}{
		{false, "", false, false},
		{true, "", false, false},
		{true, "false", false, false},
		{true, "true", true, false},
		{true, " true ", true, false},
		{true, "TRUE", false, true},
		{true, "1", false, true},
		{true, "yes", false, true},
	} {
		if c.set {
			t.Setenv("CIC_ABI_GENERIC_DISPATCH", c.val)
		} else {
			t.Setenv("CIC_ABI_GENERIC_DISPATCH", "")
		}
		got, err := genericDispatchFromEnv()
		if c.err {
			require.Error(t, err, c.val)
			assert.False(t, got, c.val)
			continue
		}
		require.NoError(t, err, c.val)
		assert.Equal(t, c.want, got, c.val)
	}
}
