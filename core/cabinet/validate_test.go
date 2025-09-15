package cabinet

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/canonicaljson"
)

func TestValidateAndChecksum(t *testing.T) {
	// Restore original function after all tests in this file
	defer func() {
		canonicaljson.SetToJSONMock(nil)
	}()

	t.Run("missing name should fail", func(t *testing.T) {
		meta := &BaseBlock{Version: "1.0"}
		_, err := validateAndChecksum(meta, KindSpec, nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "name and version are required")
	})

	t.Run("missing version should fail", func(t *testing.T) {
		meta := &BaseBlock{Name: "test"}
		_, err := validateAndChecksum(meta, KindSpec, nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "name and version are required")
	})

	t.Run("checksum mismatch should fail and set valid=false", func(t *testing.T) {
		meta := &BaseBlock{Name: "test", Version: "1.0", Checksum: "bad-checksum"}
		_, err := validateAndChecksum(meta, KindSpec, "data")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "checksum mismatch")
		assert.False(t, meta.Valid, "meta.Valid should be false on checksum mismatch")
	})

	t.Run("correct existing checksum should pass and set valid=true", func(t *testing.T) {
		// Pre-calculated checksum for the given meta and data
		const correctChecksum = "45e1aee2cbd9775261601c55a041d9228f0cc6a764ffb7b9be218a719fec6276"
		meta := &BaseBlock{Name: "test", Version: "1.0", Checksum: correctChecksum}

		sum, err := validateAndChecksum(meta, KindSpec, "data")

		require.NoError(t, err)
		assert.Equal(t, correctChecksum, sum)
		assert.True(t, meta.Valid, "meta.Valid should be true on checksum match")
	})

	t.Run("no existing checksum should calculate and return a new one", func(t *testing.T) {
		meta := &BaseBlock{Name: "test", Version: "1.0"}
		const expectedChecksum = "45e1aee2cbd9775261601c55a041d9228f0cc6a764ffb7b9be218a719fec6276"

		sum, err := validateAndChecksum(meta, KindSpec, "data")

		require.NoError(t, err)
		assert.Equal(t, expectedChecksum, sum)
		assert.False(t, meta.Valid, "meta.Valid should not be set when checksum is new")
	})

	t.Run("error from computeChecksum should be propagated", func(t *testing.T) {
		// Inject an error into the canonicalizer for the metadata part
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			return nil, errors.New("canonicalizer failed")
		})

		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := validateAndChecksum(meta, KindSpec, "data")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "canonicalizer failed")
	})
}

func TestComputeChecksum_ErrorCases(t *testing.T) {
	// Restore original function after all tests in this file
	defer func() {
		canonicaljson.SetToJSONMock(nil)
	}()

	t.Run("error on meta canonicalization should fail", func(t *testing.T) {
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			// Fail only when trying to marshal the metaForChecksum struct
			if _, ok := v.(metaForChecksum); ok {
				return nil, errors.New("meta canonicalization failed")
			}
			return canonicaljson.ToJSONOriginal(v) // Call the original implementation
		})

		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := computeChecksum(meta, KindSpec, "data")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to canonicalize meta")
	})

	t.Run("error on data canonicalization should fail", func(t *testing.T) {
		canonicaljson.SetToJSONMock(func(v any) ([]byte, error) {
			// Fail only for the data part (e.g., a string in this test)
			if _, ok := v.(string); ok {
				return nil, errors.New("data canonicalization failed")
			}
			return canonicaljson.ToJSONOriginal(v)
		})

		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := computeChecksum(meta, KindSpec, "some-data")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to canonicalize data")
	})

	t.Run("unsupported type in data should fail", func(t *testing.T) {
		// No mock needed, let the original function fail on an unsupported type
		meta := &BaseBlock{Name: "test", Version: "1.0"}
		_, err := computeChecksum(meta, KindSpec, math.NaN())

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to canonicalize data")
	})
}
