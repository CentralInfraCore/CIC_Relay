package cabinet

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
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

	// Helper function to parse a step string. Defined locally within Setx.
	parseStep := func(step string) (componentID, functionName, dataKey string, err error) {
		// This is a placeholder implementation. A robust implementation would use regex.
		// Example format: "component.function(data)"
		before, after, found := strings.Cut(step, "(")
		if !found || !strings.HasSuffix(after, ")") {
			return "", "", "", fmt.Errorf("invalid step format: missing parentheses")
		}

		parts := strings.Split(before, ".")
		if len(parts) != 2 {
			return "", "", "", fmt.Errorf("invalid step format: expected component.function")
		}

		componentID = parts[0]
		functionName = parts[1]
		dataKey = strings.TrimSuffix(after, ")")

		return componentID, functionName, dataKey, nil
	}

	// --- Start of the new execution logic ---

	// TODO: Initialize executionContext

	for _, step := range workflow.Steps {
		componentID, functionName, dataKey, err := parseStep(step)
		if err != nil {
			return nil, fmt.Errorf("error in workflow %q, step %q: %w", workflow.ID(), step, err)
		}

		// For now, just log the parsed parts to see that it's working.
		logrus.WithFields(logrus.Fields{
			"workflow":   workflow.ID(),
			"step":       step,
			"component":  componentID,
			"function":   functionName,
			"data_key":   dataKey,
		}).Info("Parsing workflow step")

		// TODO: Implement the rest of the execution logic (dispatching, etc.)
	}

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
