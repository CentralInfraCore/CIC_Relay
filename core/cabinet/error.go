package cabinet

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Common error values for Cabinet operations.
var (
	ErrInvalid  = errors.New("invalid")   // validation or integrity failure
	ErrConflict = errors.New("conflict")  // conflicting resource state
	ErrNotFound = errors.New("not_found") // resource not found
)

// Helper predicates for common error matching.
func IsInvalid(err error) bool  { return errors.Is(err, ErrInvalid) }
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// FieldError describes a validation error for a specific field.
type FieldError struct {
	Field string `json:"field"`
	Msg   string `json:"msg"`
}

// ValidationError aggregates multiple field-level validation errors.
type ValidationError struct {
	Cause []FieldError `json:"cause"`
}

// Error returns a JSON-encoded list of causes, or a generic message if empty.
func (e *ValidationError) Error() string {
	if len(e.Cause) == 0 {
		return ErrInvalid.Error()
	}
	b, err := json.Marshal(e.Cause)
	if err != nil {
		return fmt.Sprintf("%s: %v", ErrInvalid.Error(), e.Cause)
	}
	return fmt.Sprintf("%s: %s", ErrInvalid.Error(), string(b))
}

// NewValidationError creates a ValidationError from given field errors.
func NewValidationError(fields ...FieldError) *ValidationError {
	return &ValidationError{Cause: fields}
}
