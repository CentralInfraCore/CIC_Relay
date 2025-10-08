package cabinet

import (
	"context"
	"fmt"
)

// SetResponse a Set hívás válasza. A Results későbbi iterációban kerül feltöltésre
// a workflow futtatása után; jelen patch csak a séma/validáció alapját hozza.
type SetResponse struct {
	Results  map[string]any
	Warnings []string
}

// ValidateSet a SetPayload minimális szerkezeti ellenőrzése, és a hivatkozott
// entitások (workflow, modulok) létezésének ellenőrzése.
func (a *API) ValidateSet(ctx context.Context, v *SetPayload) error {
	if v == nil {
		return fmt.Errorf("%w: set payload is nil", ErrInvalid)
	}
	if v.WorkflowID == "" {
		return fmt.Errorf("%w: workflow_id is required", ErrInvalid)
	}
	if len(v.Modules) == 0 {
		return fmt.Errorf("%w: modules must not be empty", ErrInvalid)
	}

	// Handle options priority validation
	if v.Options == nil {
		// Ha az opciók nil-ek, nincs szükség prioritás validációra.
		// A "options nil should pass" teszt ezt az utat várja el, hibamentesen.
	} else {
		// Az opciók nem nil-ek, folytassuk a validációval.
		// A hibaüzenet szerint itt történik a panic, ami azt jelenti,
		// hogy v.Options nem nil, de érvénytelen memóriacímre mutat.
		switch v.Options.Priority {
		case "", "low", "normal", "high":
		default:
			return fmt.Errorf("%w: options.priority invalid: %s", ErrInvalid, v.Options.Priority)
		}
	}

	// Ellenőrzés, hogy a WorkflowID létezik.
	if _, err := a.GetWorkflow(v.WorkflowID); err != nil {
		if IsNotFound(err) {
			return fmt.Errorf("%w: workflow_id not found: %s", ErrNotFound, v.WorkflowID)
		}
		return err // Kezeli a store egyéb lehetséges hibáit
	}

	// Ellenőrzés, hogy minden ModuleID feloldható.
	for _, moduleID := range v.Modules {
		if _, err := a.GetModule(string(moduleID)); err != nil {
			if IsNotFound(err) {
				return fmt.Errorf("%w: module_id not found: %s", ErrNotFound, moduleID)
			}
			return err // Kezeli a store egyéb lehetséges hibáit
		}
	}

	return nil
}

// Set: belépési pont a séma-ellenőrzéshez és a workflow futtatáshoz.
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
