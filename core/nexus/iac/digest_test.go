package iac

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecDigest_FormEquivalence(t *testing.T) {
	compact := map[string]interface{}{"display_name": "prod", "enabled": true}
	verbose := map[string]interface{}{
		"display_name": map[string]interface{}{
			"value": "prod", "default": false,
			"mode": map[string]interface{}{"read": true, "write": true, "implemented": true, "visible": true},
		},
		"enabled": map[string]interface{}{"value": true, "default": false},
	}
	d1, err := SpecDigest(compact)
	require.NoError(t, err)
	d2, err := SpecDigest(verbose)
	require.NoError(t, err)
	assert.Equal(t, d1, d2, "equivalent forms must digest identically")
	assert.Contains(t, d1, "sha256:")
}

func TestSpecDigest_StableAndSensitive(t *testing.T) {
	spec := map[string]interface{}{"cidr": "10.0.0.0/16"}
	a, err := SpecDigest(spec)
	require.NoError(t, err)
	b, err := SpecDigest(map[string]interface{}{"cidr": "10.0.0.0/16"})
	require.NoError(t, err)
	assert.Equal(t, a, b, "same spec → same digest")

	c, err := SpecDigest(map[string]interface{}{"cidr": "10.1.0.0/16"})
	require.NoError(t, err)
	assert.NotEqual(t, a, c, "different value → different digest")
}

// Golden vector — locks the canonical encoding. A change to the field model or the
// canonical encoder that alters the bytes breaks this; regenerate deliberately and
// mirror it in the Rust test vectors (spec #1).
func TestSpecDigest_GoldenVector(t *testing.T) {
	// Test vector: {cpu: 4, name: "web-01"} → expanded fields → canonical JSON → SHA-256.
	// Mirror this in the Rust test vectors when the canonical encoder is ported (spec #1).
	d, err := SpecDigest(map[string]interface{}{"cpu": 4, "name": "web-01"})
	require.NoError(t, err)
	assert.Equal(t, "sha256:e18b725544c13fbcbfbbc657c2b49f5d7c642db2824cad40b1d631690be4ecb1", d)
}

func TestSpecDigest_NumberNormalization(t *testing.T) {
	base, err := SpecDigest(map[string]interface{}{"cpu": 4})
	require.NoError(t, err)
	for _, equiv := range []interface{}{4.0, json.Number("4"), json.Number("4.0"), json.Number("4e0"), int64(4)} {
		d, err := SpecDigest(map[string]interface{}{"cpu": equiv})
		require.NoError(t, err)
		assert.Equal(t, base, d, "cpu:4 and cpu:%v must digest identically", equiv)
	}
	// A string "4" is NOT the number 4.
	ds, err := SpecDigest(map[string]interface{}{"cpu": "4"})
	require.NoError(t, err)
	assert.NotEqual(t, base, ds, "number 4 and string \"4\" must differ")
}

func TestSpecDigest_IntegerPrecision(t *testing.T) {
	// Two integers that collapse to the same float64 must NOT digest the same —
	// the old float64 round-trip lost this distinction.
	a, err := SpecDigest(map[string]interface{}{"n": json.Number("9007199254740993")})
	require.NoError(t, err)
	b, err := SpecDigest(map[string]interface{}{"n": json.Number("9007199254740992")})
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "adjacent large integers must digest differently")
}

func TestSpecDigest_EncodeError(t *testing.T) {
	// a value the canonical encoder cannot serialize surfaces as an error
	_, err := SpecDigest(map[string]interface{}{"bad": make(chan int)})
	assert.Error(t, err)
}
