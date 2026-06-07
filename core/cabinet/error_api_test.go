package cabinet_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"centralrelay/core/cabinet"
)

func TestSentinelPredicates_API(t *testing.T) {
	t.Run("IsInvalid true for ErrInvalid and wrapped", func(t *testing.T) {
		if !cabinet.IsInvalid(cabinet.ErrInvalid) {
			t.Fatalf("IsInvalid(ErrInvalid) = false, want true")
		}
		if !cabinet.IsInvalid(fmt.Errorf("wrap: %w", cabinet.ErrInvalid)) {
			t.Fatalf("IsInvalid(wrapped ErrInvalid) = false, want true")
		}
	})

	t.Run("IsInvalid false for others", func(t *testing.T) {
		if cabinet.IsInvalid(cabinet.ErrConflict) ||
			cabinet.IsInvalid(cabinet.ErrNotFound) ||
			cabinet.IsInvalid(errors.New("x")) {
			t.Fatalf("IsInvalid returned true for non-ErrInvalid")
		}
	})

	t.Run("IsConflict", func(t *testing.T) {
		if !cabinet.IsConflict(fmt.Errorf("wrap: %w", cabinet.ErrConflict)) {
			t.Fatalf("IsConflict(wrapped ErrConflict) = false, want true")
		}
		if cabinet.IsConflict(cabinet.ErrInvalid) || cabinet.IsConflict(cabinet.ErrNotFound) {
			t.Fatalf("IsConflict returned true for other errors")
		}
	})

	t.Run("IsNotFound", func(t *testing.T) {
		if !cabinet.IsNotFound(fmt.Errorf("wrap: %w", cabinet.ErrNotFound)) {
			t.Fatalf("IsNotFound(wrapped ErrNotFound) = false, want true")
		}
		if cabinet.IsNotFound(cabinet.ErrInvalid) || cabinet.IsNotFound(cabinet.ErrConflict) {
			t.Fatalf("IsNotFound returned true for other errors")
		}
	})
}

func TestValidationError_Error_EmptyReturnsErrInvalid_API(t *testing.T) {
	var ve cabinet.ValidationError
	if got, want := ve.Error(), cabinet.ErrInvalid.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestValidationError_Error_WithCauses_JSONPrefixed_API(t *testing.T) {
	ve := cabinet.ValidationError{
		Cause: []cabinet.FieldError{
			{Field: "meta.name", Msg: "required"},
			{Field: "meta.version", Msg: "invalid semver"},
		},
	}
	got := ve.Error()

	b, err := json.Marshal(ve.Cause)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	want := fmt.Sprintf("%s: %s", cabinet.ErrInvalid.Error(), string(b))

	if got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestNewValidationError_API(t *testing.T) {
	fe1 := cabinet.FieldError{Field: "meta.name", Msg: "required"}
	fe2 := cabinet.FieldError{Field: "meta.version", Msg: "invalid semver"}

	ve := cabinet.NewValidationError(fe1, fe2)
	if ve == nil {
		t.Fatalf("NewValidationError returned nil")
	}
	if len(ve.Cause) != 2 {
		t.Fatalf("len(Cause) = %d, want 2", len(ve.Cause))
	}
	if ve.Cause[0] != fe1 || ve.Cause[1] != fe2 {
		t.Fatalf("Cause slice mismatch: %#v", ve.Cause)
	}
}

func TestValidationError_DoesNotMatchIsInvalid_API(t *testing.T) {
	ve := cabinet.NewValidationError(cabinet.FieldError{Field: "x", Msg: "y"})
	if cabinet.IsInvalid(ve) {
		t.Fatalf("IsInvalid(ValidationError) = true, want false (not wrapping ErrInvalid)")
	}
}

func TestValidationError_ImplementsError_API(t *testing.T) {
	ve := cabinet.NewValidationError()
	var _ error = ve // compile-time interface satisfaction
}
