package iac

import (
	"context"
	"errors"
	"fmt"

	"centralrelay/core/cabinet"
	"centralrelay/core/nexus/types"
)

// IaCValidator resolves schemaRefs in a Graph against the Cabinet schema registry
// and performs structural validation on each resource.
type IaCValidator struct {
	cabinet cabinet.Cabinet
}

// NewIaCValidator creates a new IaCValidator backed by the given Cabinet.
func NewIaCValidator(c cabinet.Cabinet) *IaCValidator {
	return &IaCValidator{cabinet: c}
}

// Validate iterates over all services and their resources in the graph.
// It returns a joined error listing every schemaRef that could not be resolved.
func (v *IaCValidator) Validate(ctx context.Context, graph *types.Graph) error {
	var errs []error
	for svcName, svc := range graph.Services {
		for _, res := range svc.Spec.Resources {
			if err := v.resolveSchema(ctx, svcName, res); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// ValidateResource validates a single ResourceSpec against the Cabinet schema registry.
// Suitable for per-event validation in the Operator.
func (v *IaCValidator) ValidateResource(ctx context.Context, res types.ResourceSpec) error {
	return v.resolveSchema(ctx, "", res)
}

// resolveSchema looks up the schemaRef in the Cabinet and returns a descriptive error on failure.
func (v *IaCValidator) resolveSchema(_ context.Context, svcName string, res types.ResourceSpec) error {
	schemaID := res.SchemaRef.Name + "@" + res.SchemaRef.Version
	if _, err := v.cabinet.GetSchema(schemaID); err != nil {
		if svcName != "" {
			return fmt.Errorf("service/%s resource/%s: schemaRef not found: %s", svcName, res.Meta.Name, schemaID)
		}
		return fmt.Errorf("resource/%s: schemaRef not found: %s", res.Meta.Name, schemaID)
	}
	return nil
}
