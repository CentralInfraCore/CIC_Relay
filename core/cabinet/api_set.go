package cabinet

import (
	"context"
	"fmt"
)

// SetResponse is the response to a Set call.
type SetResponse struct {
	Results  map[string]any
	Warnings []string
	Trace    *ProofTrace
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
