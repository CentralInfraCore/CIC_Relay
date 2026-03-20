package cabinet

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestSentinelPredicates(t *testing.T) {
	t.Run("IsInvalid true for ErrInvalid", func(t *testing.T) {
		if !IsInvalid(ErrInvalid) {
			t.Fatalf("IsInvalid(ErrInvalid) = false, want true")
		}
	})
	t.Run("IsInvalid true for wrapped ErrInvalid", func(t *testing.T) {
		w := fmt.Errorf("wrap: %w", ErrInvalid)
		if !IsInvalid(w) {
			t.Fatalf("IsInvalid(wrapped ErrInvalid) = false, want true")
		}
	})
	t.Run("IsInvalid false for others", func(t *testing.T) {
		if IsInvalid(ErrConflict) || IsInvalid(ErrNotFound) || IsInvalid(errors.New("x")) {
			t.Fatalf("IsInvalid returned true for non-ErrInvalid")
		}
	})

	t.Run("IsConflict works", func(t *testing.T) {
		w := fmt.Errorf("wrap: %w", ErrConflict)
		if !IsConflict(w) {
			t.Fatalf("IsConflict(wrapped ErrConflict) = false, want true")
		}
		if IsConflict(ErrInvalid) || IsConflict(ErrNotFound) {
			t.Fatalf("IsConflict returned true for other errors")
		}
	})

	t.Run("IsNotFound works", func(t *testing.T) {
		w := fmt.Errorf("wrap: %w", ErrNotFound)
		if !IsNotFound(w) {
			t.Fatalf("IsNotFound(wrapped ErrNotFound) = false, want true")
		}
		if IsNotFound(ErrInvalid) || IsNotFound(ErrConflict) {
			t.Fatalf("IsNotFound returned true for other errors")
		}
	})

	t.Run("IsTimeout works", func(t *testing.T) {
		w := fmt.Errorf("wrap: %w", ErrTimeout)
		if !IsTimeout(w) {
			t.Fatalf("IsTimeout(wrapped ErrTimeout) = false, want true")
		}
		if IsTimeout(ErrInvalid) || IsTimeout(ErrNotFound) || IsTimeout(errors.New("x")) {
			t.Fatalf("IsTimeout returned true for non-ErrTimeout")
		}
	})
}

func TestValidationError_Error_EmptyReturnsErrInvalid(t *testing.T) {
	var ve ValidationError // zero value, empty Cause
	got := ve.Error()
	want := ErrInvalid.Error()
	if got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestValidationError_Error_WithCauses_JSONPrefixed(t *testing.T) {
	ve := ValidationError{
		Cause: []FieldError{
			{Field: "meta.name", Msg: "required"},
			{Field: "meta.version", Msg: "invalid semver"},
		},
	}

	got := ve.Error()

	b, err := json.Marshal(ve.Cause) // marshallal állítjuk elő az elvárt JSON-t
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	want := fmt.Sprintf("%s: %s", ErrInvalid.Error(), string(b))

	if got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestNewValidationError_ConstructsAsExpected(t *testing.T) {
	fe1 := FieldError{Field: "meta.name", Msg: "required"}
	fe2 := FieldError{Field: "meta.version", Msg: "invalid semver"}

	ve := NewValidationError(fe1, fe2)
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

func TestValidationError_IsInvalidPredicateDoesNotMatch(t *testing.T) {
	// ValidationError nem wrap-eli az ErrInvalid-et, csak a szövegben használja → IsInvalid false.
	ve := &ValidationError{Cause: []FieldError{{Field: "x", Msg: "y"}}}
	if IsInvalid(ve) {
		t.Fatalf("IsInvalid(ValidationError) = true, want false")
	}
}

func TestValidationError_Error_MarshalFails(t *testing.T) {
	originalJSONMarshal := jsonMarshal
	t.Cleanup(func() { jsonMarshal = originalJSONMarshal })

	// Mock jsonMarshal to return an error
	jsonMarshal = func(v any) ([]byte, error) {
		return nil, errors.New("mock marshal error")
	}

	ve := ValidationError{
		Cause: []FieldError{
			{Field: "field1", Msg: "message1"},
		},
	}

	got := ve.Error()
	want := fmt.Sprintf("%s: %v", ErrInvalid.Error(), ve.Cause)

	if got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestNewAPIError(t *testing.T) {
	t.Run("ValidationError", func(t *testing.T) {
		validationErr := NewValidationError(FieldError{Field: "test", Msg: "error"})
		apiErr := NewAPIError(validationErr)

		if apiErr.Code != ErrInvalid.Error() {
			t.Errorf("Code got %q, want %q", apiErr.Code, ErrInvalid.Error())
		}
		if apiErr.Message != "Validation failed" {
			t.Errorf("Message got %q, want %q", apiErr.Message, "Validation failed")
		}
		if len(apiErr.Details) != 1 || apiErr.Details[0].Field != "test" {
			t.Errorf("Details got %#v, want validation details", apiErr.Details)
		}
	})

	t.Run("ErrConflict", func(t *testing.T) {
		apiErr := NewAPIError(ErrConflict)
		if apiErr.Code != ErrConflict.Error() {
			t.Errorf("Code got %q, want %q", apiErr.Code, ErrConflict.Error())
		}
		if apiErr.Message != "Resource conflict" {
			t.Errorf("Message got %q, want %q", apiErr.Message, "Resource conflict")
		}
		if apiErr.Details != nil {
			t.Errorf("Details got %#v, want nil", apiErr.Details)
		}
	})

	t.Run("ErrNotFound", func(t *testing.T) {
		apiErr := NewAPIError(ErrNotFound)
		if apiErr.Code != ErrNotFound.Error() {
			t.Errorf("Code got %q, want %q", apiErr.Code, ErrNotFound.Error())
		}
		if apiErr.Message != "Resource not found" {
			t.Errorf("Message got %q, want %q", apiErr.Message, "Resource not found")
		}
		if apiErr.Details != nil {
			t.Errorf("Details got %#v, want nil", apiErr.Details)
		}
	})

	t.Run("OtherError", func(t *testing.T) {
		otherErr := errors.New("something unexpected happened")
		apiErr := NewAPIError(otherErr)
		if apiErr.Code != "internal_error" {
			t.Errorf("Code got %q, want %q", apiErr.Code, "internal_error")
		}
		if apiErr.Message != otherErr.Error() {
			t.Errorf("Message got %q, want %q", apiErr.Message, otherErr.Error())
		}
		if apiErr.Details != nil {
			t.Errorf("Details got %#v, want nil", apiErr.Details)
		}
	})
}
