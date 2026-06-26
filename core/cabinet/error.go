package cabinet

import (
	"encoding/json"
	"errors"
	"fmt"
)

// jsonMarshal is a variable that holds the json.Marshal function.
// It can be replaced in tests to simulate marshaling errors.
var jsonMarshal = json.Marshal

// Common error values for Cabinet operations.
var (
	ErrInvalid  = errors.New("invalid")   // validation or integrity failure
	ErrConflict = errors.New("conflict")  // conflicting resource state
	ErrNotFound = errors.New("not_found") // resource not found
	ErrTimeout  = errors.New("timeout")   // execution exceeded time budget
)

// IsInvalid checks if an error is an ErrInvalid validation or integrity failure.
func IsInvalid(err error) bool  { return errors.Is(err, ErrInvalid) }

// IsConflict checks if an error is an ErrConflict resource state conflict.
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }

// IsNotFound checks if an error is an ErrNotFound resource not found.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsTimeout checks if an error is an ErrTimeout execution exceeded time budget.
func IsTimeout(err error) bool  { return errors.Is(err, ErrTimeout) }

// FieldError describes a validation error for a specific field.
type FieldError struct {
	Field string `json:"field"`
	Msg   string `json:"msg"`
}

// ValidationError aggregates multiple field-level validation errors.
type ValidationError struct {
	Cause []FieldError `json:"cause"`
}

// Error implements the error interface, returning a JSON-encoded list of field-level validation errors or a generic message if empty.
func (e *ValidationError) Error() string {
	if len(e.Cause) == 0 {
		return ErrInvalid.Error()
	}
	b, err := jsonMarshal(e.Cause) // Use the variable here
	if err != nil {
		return fmt.Sprintf("%s: %v", ErrInvalid.Error(), e.Cause)
	}
	return fmt.Sprintf("%s: %s", ErrInvalid.Error(), string(b))
}

// NewValidationError constructs a ValidationError from the provided field errors.
func NewValidationError(fields ...FieldError) *ValidationError {
	return &ValidationError{Cause: fields}
}

// APIError represents a standardized error response for API consumers.
type APIError struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
}

// NewAPIError converts a Go error into a standardized APIError response for API consumers.
func NewAPIError(err error) *APIError {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return &APIError{
			Code:    ErrInvalid.Error(),
			Message: "Validation failed",
			Details: validationErr.Cause,
		}
	}

	if errors.Is(err, ErrConflict) {
		return &APIError{
			Code:    ErrConflict.Error(),
			Message: "Resource conflict",
		}
	}

	if errors.Is(err, ErrNotFound) {
		return &APIError{
			Code:    ErrNotFound.Error(),
			Message: "Resource not found",
		}
	}

	// Default case for unhandled errors
	return &APIError{
		Code:    "internal_error",
		Message: err.Error(),
	}
}
