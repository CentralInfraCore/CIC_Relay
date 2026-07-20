package iac

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var allTrue = FieldMode{Read: true, Write: true, Implemented: true, Visible: true}

// The three forms of a parameter are equivalent — the determinism contract:
// a compact scalar and a fully-written descriptor expand to the same Field.
func TestExpandField_ThreeFormsEquivalent(t *testing.T) {
	want := Field{Value: "prod", Default: false, Mode: allTrue}

	assert.Equal(t, want, ExpandField("prod"))
	assert.Equal(t, want, ExpandField(map[string]interface{}{
		"value": "prod", "default": false,
	}))
	assert.Equal(t, want, ExpandField(map[string]interface{}{
		"value":   "prod",
		"default": false,
		"mode":    map[string]interface{}{"read": true, "write": true, "implemented": true, "visible": true},
	}))
}

func TestExpandField_ScalarKinds(t *testing.T) {
	assert.Equal(t, Field{Value: 42, Default: false, Mode: allTrue}, ExpandField(42))
	assert.Equal(t, Field{Value: true, Default: false, Mode: allTrue}, ExpandField(true))
	list := []interface{}{"a", "b"}
	assert.Equal(t, Field{Value: list, Default: false, Mode: allTrue}, ExpandField(list))
}

func TestExpandField_Default(t *testing.T) {
	f := ExpandField(map[string]interface{}{"value": "x", "default": true})
	assert.True(t, f.Default)
	assert.Equal(t, allTrue, f.Mode)

	// a non-bool default coerces to the fallback (false)
	f = ExpandField(map[string]interface{}{"value": "x", "default": "yes"})
	assert.False(t, f.Default)
}

func TestExpandField_PartialMode(t *testing.T) {
	// a scaffold field: implemented explicitly false, the rest default true
	f := ExpandField(map[string]interface{}{
		"value": "x",
		"mode":  map[string]interface{}{"implemented": false},
	})
	assert.Equal(t, FieldMode{Read: true, Write: true, Implemented: false, Visible: true}, f.Mode)

	// a read-only (provider-computed) field
	f = ExpandField(map[string]interface{}{
		"value": "ocid1...",
		"mode":  map[string]interface{}{"write": false},
	})
	assert.False(t, f.Mode.Write)
	assert.True(t, f.Mode.Read)

	// mode that isn't a map falls back to all-true
	f = ExpandField(map[string]interface{}{"value": "x", "mode": "bogus"})
	assert.Equal(t, allTrue, f.Mode)
}

func TestExpandField_MapWithoutValueIsOpaque(t *testing.T) {
	// A map without a "value" key is the value itself (deeper descent is a later
	// round), not a descriptor.
	nested := map[string]interface{}{"ocpus": 4, "memory": 64}
	f := ExpandField(nested)
	assert.Equal(t, Field{Value: nested, Default: false, Mode: allTrue}, f)
}

func TestExpandSpec(t *testing.T) {
	spec := map[string]interface{}{
		"display_name": "production-vcn", // scalar sugar
		"cidr": map[string]interface{}{ // explicit descriptor
			"value": "10.10.0.0/16",
			"mode":  map[string]interface{}{"write": false},
		},
	}
	got := ExpandSpec(spec)

	assert.Equal(t, Field{Value: "production-vcn", Default: false, Mode: allTrue}, got["display_name"])
	assert.Equal(t, "10.10.0.0/16", got["cidr"].Value)
	assert.False(t, got["cidr"].Mode.Write)
	assert.True(t, got["cidr"].Mode.Read)
	assert.Len(t, got, 2)
}
