package cabinet

import (
	"context"
	"fmt"
)

// Proof error codes reported in ProofStatus.ErrorCode. They carry no error text:
// the cause (which may name Vault paths or tokens) stays in the relay log.
const (
	ProofErrRecorderNotConfigured = "recorder_not_configured"
	ProofErrRecordFailed          = "record_failed"
)

// ProofStatus tells the caller whether the ProofTrace of an execution was persisted.
// Execution succeeded, the chain hash was computed (SetResponse.Trace) and the record
// was persisted are three separate facts; this is the third.
type ProofStatus struct {
	Persisted bool   `json:"persisted"`
	Commit    string `json:"commit,omitempty"`     // set when the recorder names the persisted record
	ErrorCode string `json:"error_code,omitempty"` // set when Persisted is false
}

// SetResponse is the response to a Set call.
type SetResponse struct {
	Results  map[string]any
	Warnings []string
	Trace    *ProofTrace
	Proof    *ProofStatus `json:"proof"`
}

// ValidateSet performs minimal structural validation of SetPayload and checks if referenced
// entities (workflow, modules) exist.
func (a *API) ValidateSet(ctx context.Context, v *SetPayload) error {
	if v == nil {
		return fmt.Errorf("%w: set payload is nil", ErrInvalid)
	}
	if v.WorkflowID == "" {
		return fmt.Errorf("%w: workflow_id is required", ErrInvalid)
	}
	// Modules is optional: the execution engine resolves modules from workflow step
	// ComponentIDs, not from this field. When provided, each listed module is validated
	// to exist — but an empty list is accepted.

	// Handle options priority validation
	if v.Options == nil {
		// If options are nil, no priority validation is needed.
		// The "options nil should pass" test expects this path to succeed without errors.
	} else {
		// Options are not nil, continue with validation.
		// According to the error message, the panic happens here, which means
		// that v.Options is not nil but points to an invalid memory address.
		switch v.Options.Priority {
		case "", "low", "normal", "high":
		default:
			return fmt.Errorf("%w: options.priority invalid: %s", ErrInvalid, v.Options.Priority)
		}
	}

	// Check that WorkflowID exists.
	if _, err := a.GetWorkflow(v.WorkflowID); err != nil {
		if IsNotFound(err) {
			return fmt.Errorf("%w: workflow_id not found: %s", ErrNotFound, v.WorkflowID)
		}
		return err // Handle other possible store errors
	}

	// Check that all ModuleIDs can be resolved.
	for _, moduleID := range v.Modules {
		if _, err := a.GetModule(string(moduleID)); err != nil {
			if IsNotFound(err) {
				return fmt.Errorf("%w: module_id not found: %s", ErrNotFound, moduleID)
			}
			return err // Handle other possible store errors
		}
	}

	return nil
}

// Set is the entry point for schema validation and workflow execution.
func (a *API) Set(ctx context.Context, in *SetPayload) (*SetResponse, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: nil request", ErrInvalid)
	}
	CanonicalizeSetPayload(in)
	if err := a.ValidateSet(ctx, in); err != nil {
		return nil, err
	}

	// Delegate the execution to the underlying cabinet service.
	return a.Cabinet.Setx(ctx, in)
}
