package cabinet

import (
	"context"
	"fmt"
)

// Cabinet defines the central API for schema/module/workflow management.
type Cabinet interface {
	PutSchema(s *Schema) error
	GetSchema(id string) (*Schema, error)

	PutModule(m *ModuleDescriptor) error
	GetModule(id string) (*ModuleDescriptor, error)

	PutWorkflow(w *Workflow) error
	GetWorkflow(id string) (*Workflow, error)

	// Set executes a workflow based on the payload.
	Set(ctx context.Context, in *SetPayload) (*SetResponse, error)
}

type cabinetService struct {
	schemas   SchemaStore
	modules   ModuleStore
	workflows WorkflowStore
}

func NewCabinetService(schemas SchemaStore, modules ModuleStore, workflows WorkflowStore) Cabinet {
	return &cabinetService{schemas: schemas, modules: modules, workflows: workflows}
}

// Set resolves the execution plan and (in the future) runs the workflow.
func (c *cabinetService) Set(ctx context.Context, in *SetPayload) (*SetResponse, error) {
	// 1. Resolve the workflow itself.
	workflow, err := c.GetWorkflow(in.WorkflowID)
	if err != nil {
		// This should have been caught by ValidateSet, but we check again for safety.
		return nil, fmt.Errorf("failed to resolve workflow %q: %w", in.WorkflowID, err)
	}

	// 2. Build the execution plan by resolving each step.
	var executionPlan []map[string]string

	for _, schemaID := range workflow.Steps {
		// 2a. Resolve the schema for the current step.
		schema, err := c.GetSchema(schemaID)
		if err != nil {
			return nil, fmt.Errorf("workflow %q failed: could not resolve step schema %q: %w", workflow.ID(), schemaID, err)
		}

		// 2b. Find a suitable module from the payload that accepts this schema.
		var foundModuleID ModuleID = ""
		for _, moduleID := range in.Modules {
			moduleDesc, err := c.GetModule(string(moduleID))
			if err != nil {
				// This should also be caught by ValidateSet.
				return nil, fmt.Errorf("workflow %q failed: could not resolve provided module %q: %w", workflow.ID(), moduleID, err)
			}

			for _, acceptedSchemaID := range moduleDesc.Accepts {
				if acceptedSchemaID == schema.ID() {
					foundModuleID = moduleID
					break
				}
			}
			if foundModuleID != "" {
				break
			}
		}

		if foundModuleID == "" {
			return nil, fmt.Errorf("%w: no suitable module provided for schema %q in workflow %q", ErrInvalid, schema.ID(), workflow.ID())
		}

		planStep := map[string]string{
			"step":   schemaID,
			"schema": schema.ID(),
			"module": string(foundModuleID),
		}
		executionPlan = append(executionPlan, planStep)
	}

	// TODO: Implement the actual execution of the plan (e.g., dispatching to WASM modules).
	// For now, return an empty Results map to conform to existing tests.
	// The executionPlan can be logged or used internally in the future.

	return &SetResponse{
		Results:  map[string]any{},
		Warnings: nil,
	}, nil
}

func (c *cabinetService) PutSchema(s *Schema) error {
	if err := s.Validate(); err != nil {
		return err
	}
	c.schemas.Put(s)
	return nil
}

func (c *cabinetService) GetSchema(id string) (*Schema, error) {
	if s, ok := c.schemas.Get(id); ok {
		return s, nil
	}
	return nil, ErrNotFound
}

func (c *cabinetService) PutModule(m *ModuleDescriptor) error {
	if err := m.Validate(); err != nil {
		return err
	}
	c.modules.Put(m)
	return nil
}

func (c *cabinetService) GetModule(id string) (*ModuleDescriptor, error) {
	if m, ok := c.modules.Get(id); ok {
		return m, nil
	}
	return nil, ErrNotFound
}

func (c *cabinetService) PutWorkflow(w *Workflow) error {
	if err := w.Validate(); err != nil {
		return err
	}
	c.workflows.Put(w)
	return nil
}

func (c *cabinetService) GetWorkflow(id string) (*Workflow, error) {
	if w, ok := c.workflows.Get(id); ok {
		return w, nil
	}
	return nil, ErrNotFound
}
