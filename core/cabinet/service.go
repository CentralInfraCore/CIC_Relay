package cabinet

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
)

// executionContext holds the state for a single workflow execution.
// It contains the data variables and the materialized (live) modules.
type executionContext struct {
	variables         map[string]interface{}
	materializedModules map[string]*ModuleDescriptor
}

// NewExecutionContext creates a new execution context and initializes it with the starting payload.
func NewExecutionContext(initialPayload map[string]interface{}) *executionContext {
	ec := &executionContext{
		variables:         make(map[string]interface{}),
		materializedModules: make(map[string]*ModuleDescriptor),
	}
	// The initial payload is stored under the special key "input".
	ec.variables["input"] = initialPayload
	return ec
}

// Get retrieves a value from the context's variable store.
func (ec *executionContext) Get(key string) (interface{}, bool) {
	val, ok := ec.variables[key]
	return val, ok
}

// Set stores a value in the context's variable store.
func (ec *executionContext) Set(key string, value interface{}) {
	ec.variables[key] = value
}

// GetModule retrieves a materialized module from the context.
func (ec *executionContext) GetModule(id string) (*ModuleDescriptor, bool) {
	mod, ok := ec.materializedModules[id]
	return mod, ok
}

// StoreModule stores a newly materialized module in the context.
func (ec *executionContext) StoreModule(mod *ModuleDescriptor) {
	ec.materializedModules[mod.ID()] = mod
}

// Cabinet defines the central API for schema/module/workflow management.
type Cabinet interface {
	PutSchema(s *Schema) error
	GetSchema(id string) (*Schema, error)

	PutModule(m *ModuleDescriptor) error
	GetModule(id string) (*ModuleDescriptor, error)

	PutWorkflow(w *Workflow) error
	GetWorkflow(id string) (*Workflow, error)

	// Setx executes a workflow based on the payload.
	Setx(ctx context.Context, in *SetPayload) (*SetResponse, error)

	// Close closes the underlying resources, like the WASM runtime.
	Close(ctx context.Context) error
}

type cabinetService struct {
	schemas   SchemaStore
	modules   ModuleStore
	workflows WorkflowStore
	runtime   wazero.Runtime
}

func NewCabinetService(schemas SchemaStore, modules ModuleStore, workflows WorkflowStore) Cabinet {
	// Create a new WebAssembly Runtime.
	r := wazero.NewRuntime(context.Background())
	return &cabinetService{schemas: schemas, modules: modules, workflows: workflows, runtime: r}
}

// Setx executes a workflow based on the function-oriented model.
func (c *cabinetService) Setx(ctx context.Context, in *SetPayload) (*SetResponse, error) {
	// 1. Resolve the workflow itself.
	workflow, err := c.GetWorkflow(in.WorkflowID)
	if err != nil {
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

	return &SetResponse{
		Results:  map[string]any{},
		Warnings: nil,
	}, nil
}

func (c *cabinetService) Close(ctx context.Context) error {
	return c.runtime.Close(ctx)
}

func (c *cabinetService) PutSchema(s *Schema) error {
	// Validation is now handled by the constructor. The service trusts that a valid object is passed.
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
	// Validation is now handled by the constructor.
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
	// Validation is now handled by the constructor.
	c.workflows.Put(w)
	return nil
}

func (c *cabinetService) GetWorkflow(id string) (*Workflow, error) {
	if w, ok := c.workflows.Get(id); ok {
		return w, nil
	}
	return nil, ErrNotFound
}
