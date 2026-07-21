package iac

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestField_AsRef(t *testing.T) {
	ref, ok := ExpandField(map[string]interface{}{"ref": "production-private", "kind": "RouteTable"}).AsRef()
	assert.True(t, ok)
	assert.Equal(t, "production-private", ref.Target)
	assert.Equal(t, "RouteTable", ref.Kind)

	// ref without kind
	ref, ok = ExpandField(map[string]interface{}{"ref": "x"}).AsRef()
	assert.True(t, ok)
	assert.Equal(t, "x", ref.Target)
	assert.Empty(t, ref.Kind)

	// scalar → not a ref
	_, ok = ExpandField("scalar").AsRef()
	assert.False(t, ok)

	// map without a ref key → not a ref
	_, ok = ExpandField(map[string]interface{}{"foo": "bar"}).AsRef()
	assert.False(t, ok)

	// empty / non-string ref → not a ref
	_, ok = ExpandField(map[string]interface{}{"ref": ""}).AsRef()
	assert.False(t, ok)
	_, ok = ExpandField(map[string]interface{}{"ref": 42}).AsRef()
	assert.False(t, ok)
}
