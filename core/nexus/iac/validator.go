package iac

import (
	"context"
	"errors"
	"fmt"

	"centralrelay/core/cabinet"
	"centralrelay/core/nexus/types"
)

// IaCValidator validates an IaC graph against the Cabinet schema registry: it
// resolves each resource's schemaRef, structurally validates its (expanded) spec,
// and checks that every declared dependency resolves to a resource in the graph.
type IaCValidator struct {
	cabinet cabinet.Cabinet
}

// NewIaCValidator creates a new IaCValidator backed by the given Cabinet.
func NewIaCValidator(c cabinet.Cabinet) *IaCValidator {
	return &IaCValidator{cabinet: c}
}

// Validate iterates over all services and their resources in the graph and joins
// every error found: unresolved schemaRefs, spec-structure violations, and
// dependencies that point at a resource not present in the graph.
func (v *IaCValidator) Validate(_ context.Context, graph *types.Graph) error {
	index := resourceIndex(graph)
	var errs []error
	for svcName, svc := range graph.Services {
		for _, res := range svc.Spec.Resources {
			if err := v.resolveSchema(svcName, res); err != nil {
				errs = append(errs, err)
			}
			if err := validateSpecStructure(svcName, res); err != nil {
				errs = append(errs, err)
			}
			for _, dep := range res.Dependencies {
				if _, ok := index[dep]; !ok {
					errs = append(errs, fmt.Errorf("%s: dependency %q does not resolve to any resource in the graph", resourceRef(svcName, res), dep))
				}
			}
			if err := resolveFieldRefs(svcName, res, index); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// ValidateResource validates a single ResourceSpec (schemaRef resolution + spec
// structure). Suitable for per-event validation in the Operator; dependency
// integrity needs the whole graph and is only checked by [IaCValidator.Validate].
func (v *IaCValidator) ValidateResource(_ context.Context, res types.ResourceSpec) error {
	return errors.Join(v.resolveSchema("", res), validateSpecStructure("", res))
}

// resolveSchema looks up the schemaRef in the Cabinet and returns a descriptive
// error on failure.
func (v *IaCValidator) resolveSchema(svcName string, res types.ResourceSpec) error {
	schemaID := res.SchemaRef.Name + "@" + res.SchemaRef.Version
	if _, err := v.cabinet.GetSchema(schemaID); err != nil {
		return fmt.Errorf("%s: schemaRef not found: %s", resourceRef(svcName, res), schemaID)
	}
	return nil
}

// validateSpecStructure expands the resource spec into canonical fields (so a
// compact and a fully-written spec validate identically) and checks intent-side
// consistency: a field explicitly declared non-writable (mode.write=false) cannot
// appear in an intent spec — it is provider-computed / read-only and belongs to
// observed state, not the declaration.
func validateSpecStructure(svcName string, res types.ResourceSpec) error {
	var errs []error
	for name, field := range ExpandSpec(res.Spec) {
		if !field.Mode.Write {
			errs = append(errs, fmt.Errorf("%s: field %q is declared non-writable (mode.write=false) but appears in an intent spec", resourceRef(svcName, res), name))
		}
	}
	return errors.Join(errs...)
}

// resourceRef renders a stable "service/x resource/y" (or "resource/y") label.
func resourceRef(svcName string, res types.ResourceSpec) string {
	if svcName != "" {
		return fmt.Sprintf("service/%s resource/%s", svcName, res.Meta.Name)
	}
	return fmt.Sprintf("resource/%s", res.Meta.Name)
}

// resolveFieldRefs resolves every object-reference field in the resource's spec
// against the graph index: the referenced resource must exist, and — if the ref
// names a kind — the target's kind must match. Field-level references are the
// "hidden object behind a parameter" (reference.go).
func resolveFieldRefs(svcName string, res types.ResourceSpec, index map[string]string) error {
	var errs []error
	for name, field := range ExpandSpec(res.Spec) {
		ref, ok := field.AsRef()
		if !ok {
			continue
		}
		kind, exists := index[ref.Target]
		if !exists {
			errs = append(errs, fmt.Errorf("%s: field %q references resource %q which does not exist in the graph", resourceRef(svcName, res), name, ref.Target))
			continue
		}
		if ref.Kind != "" && kind != "" && kind != ref.Kind {
			errs = append(errs, fmt.Errorf("%s: field %q references %q as kind %q but it is %q", resourceRef(svcName, res), name, ref.Target, ref.Kind, kind))
		}
	}
	return errors.Join(errs...)
}

// resourceIndex maps every resource name across all services to its kind — the
// resolution target for dependency and field-reference integrity.
func resourceIndex(graph *types.Graph) map[string]string {
	index := map[string]string{}
	for _, svc := range graph.Services {
		for _, res := range svc.Spec.Resources {
			index[res.Meta.Name] = res.Meta.Kind
		}
	}
	return index
}
