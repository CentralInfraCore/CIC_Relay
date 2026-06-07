package cabinet

import "fmt"

// ValidateSchema checks whether data carries a "$schema" key matching one of accepts.
// If accepts is empty, all data passes. This is the exported counterpart of
// validateInputSchema — usable without a ModuleDescriptor (e.g. for self-validation).
func ValidateSchema(accepts []string, data interface{}) error {
	if len(accepts) == 0 {
		return nil
	}
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Errorf("%w: schema-typed input required but received %T", ErrInvalid, data)
	}
	raw, exists := dataMap["$schema"]
	if !exists {
		return fmt.Errorf("%w: input missing \"$schema\" key (accepts: %v)", ErrInvalid, accepts)
	}
	schemaID, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%w: \"$schema\" must be a string, got %T", ErrInvalid, raw)
	}
	for _, accepted := range accepts {
		if accepted == schemaID {
			return nil
		}
	}
	return fmt.Errorf("%w: schema %q not accepted (accepts: %v)", ErrInvalid, schemaID, accepts)
}

// validateInputSchema checks whether inputData declares a "$schema" key
// that matches one of the module's accepted schema IDs.
//
// If the module declares no accepted schemas (Accepts is empty), all inputs
// pass without inspection. This preserves backward-compatibility with native
// modules that do not restrict their input type.
//
// When Accepts is non-empty the input MUST be a map[string]interface{} carrying
// a "$schema" string value whose value appears in the Accepts list.
// Any deviation returns ErrInvalid.
func validateInputSchema(moduleDesc *ModuleDescriptor, inputData interface{}) error {
	if len(moduleDesc.Accepts) == 0 {
		return nil
	}

	dataMap, ok := inputData.(map[string]interface{})
	if !ok {
		return fmt.Errorf("%w: module %q requires schema-typed input but received %T", ErrInvalid, moduleDesc.ID(), inputData)
	}

	raw, exists := dataMap["$schema"]
	if !exists {
		return fmt.Errorf("%w: module %q requires input with \"$schema\" key (accepts: %v)", ErrInvalid, moduleDesc.ID(), moduleDesc.Accepts)
	}

	schemaID, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%w: \"$schema\" must be a string, got %T", ErrInvalid, raw)
	}

	for _, accepted := range moduleDesc.Accepts {
		if accepted == schemaID {
			return nil
		}
	}

	return fmt.Errorf("%w: module %q does not accept schema %q (accepts: %v)", ErrInvalid, moduleDesc.ID(), schemaID, moduleDesc.Accepts)
}
