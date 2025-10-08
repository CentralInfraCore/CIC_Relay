package cabinet

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// executionContext holds the state for a single workflow execution.
// It contains the data variables and the materialized (live) modules.
type executionContext struct {
	variables           map[string]interface{}
	materializedModules map[string]*ModuleDescriptor
}

// NewExecutionContext creates a new execution context and initializes it with the starting payload.
func NewExecutionContext(initialPayload map[string]interface{}) *executionContext {
	ec := &executionContext{
		variables:           make(map[string]interface{}),
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
	schemas     SchemaStore
	modules     ModuleStore
	workflows   WorkflowStore
	wasmManager *WasmManager
}

// NewCabinetService creates a new Cabinet service with the given stores and WasmManager configuration.
func NewCabinetService(ctx context.Context, schemas SchemaStore, modules ModuleStore, workflows WorkflowStore, wasmConfig WasmManagerConfig) (Cabinet, error) {
	wasmManager, err := NewWasmManager(ctx, modules, wasmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create WASM manager: %w", err)
	}

	return &cabinetService{
		schemas:     schemas,
		modules:     modules,
		workflows:   workflows,
		wasmManager: wasmManager,
	}, nil
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

		// Find the last dot to separate componentID and functionName
		lastDotIndex := strings.LastIndex(before, ".")
		if lastDotIndex == -1 || lastDotIndex == 0 || lastDotIndex == len(before)-1 {
			return "", "", "", fmt.Errorf("invalid step format: expected component.function")
		}

		componentID = before[:lastDotIndex]
		functionName = before[lastDotIndex+1:]
		dataKey = strings.TrimSuffix(after, ")")

		return componentID, functionName, dataKey, nil
	}

	// --- Start of the new execution logic ---

	// 2. Initialize executionContext
	execCtx := NewExecutionContext(in.Payload)

	for _, step := range workflow.Steps {
		componentID, functionName, dataKey, err := parseStep(step)
		if err != nil {
			return nil, fmt.Errorf("error in workflow %q, step %q: %w", workflow.ID(), step, err)
		}

		// 2b. Resolve the component (ModuleDescriptor) from the module store.
		moduleDesc, err := c.GetModule(componentID)
		if err != nil {
			return nil, fmt.Errorf("workflow %q failed: could not resolve component %q: %w", workflow.ID(), componentID, err)
		}

		// 2c. Resolve the input data from the execution context.
		inputData, ok := execCtx.Get(dataKey)
		if !ok {
			return nil, fmt.Errorf("workflow %q failed: input data key %q not found in context for step %q", workflow.ID(), dataKey, step)
		}

		// 2d. (Optional) Validate inputData against the function's input schema.
		// TODO: Implement schema validation here using moduleDesc.Accepts and inputData's schema ID.

		// 2e. Dispatch the call based on the module type.
		var result interface{}
		var execErr error

		if moduleDesc.NativeImpl != nil {
			// --- NATIVE GO EXECUTION ---
			nativeFunc, ok := moduleDesc.NativeImpl[functionName]
			if !ok {
				return nil, fmt.Errorf("component %q does not have native function %q", componentID, functionName)
			}

			// Use reflection to call the native Go function.
			funcValue := reflect.ValueOf(nativeFunc)
			// Ensure the function has the expected signature: func(ctx context.Context, data interface{}) (interface{}, error)
			// This check is crucial for type safety when using reflection.
			if funcValue.Kind() != reflect.Func || funcValue.Type().NumIn() != 2 ||
				funcValue.Type().In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() ||
				funcValue.Type().In(1) != reflect.TypeOf((*interface{})(nil)).Elem() ||
				funcValue.Type().NumOut() != 2 ||
				funcValue.Type().Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
				return nil, fmt.Errorf("native function %s.%s has unexpected signature", componentID, functionName)
			}

			args := []reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(inputData)}
			results := funcValue.Call(args)

			// Handle return values
			if len(results) > 0 {
				result = results[0].Interface()
			}
			if len(results) > 1 && !results[1].IsNil() {
				execErr = results[1].Interface().(error)
			}
		} else if moduleDesc.WasmCode != nil {
			// --- WASM EXECUTION ---
			host, err := c.wasmManager.NewHostInstance(ctx, componentID)
			if err != nil {
				return nil, fmt.Errorf("failed to create WASM host instance for module %q: %w", componentID, err)
			}
			defer host.Close(ctx)

			inputJson, err := json.Marshal(inputData)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal input data for WASM module %q: %w", componentID, err)
			}

			authContextJson := "{}"

			normalizedFunctionName := strings.ToLower(functionName)

			var dataResult, errorResult string
			if normalizedFunctionName == "init" {
				dataResult, errorResult = host.Init(ctx, authContextJson, string(inputJson))
			} else if normalizedFunctionName == "process" {
				dataResult, errorResult = host.Process(ctx, authContextJson, string(inputJson))
			} else if normalizedFunctionName == "get" {
				dataResult, errorResult = host.Get(ctx, authContextJson, string(inputJson))
			} else if normalizedFunctionName == "notify" {
				dataResult, errorResult = host.Notify(ctx, authContextJson, string(inputJson))
			} else {
				execErr = fmt.Errorf("WASM module %q does not support operation %q", componentID, functionName)
			}

			if errorResult != "null" && errorResult != "" {
				execErr = fmt.Errorf("WASM function %s.%s returned error: %s", componentID, functionName, errorResult)
			} else {
				var jsonResult interface{}
				if err := json.Unmarshal([]byte(dataResult), &jsonResult); err != nil {
					result = dataResult
				} else {
					result = jsonResult
				}
			}
		} else {
			return nil, fmt.Errorf("component %s has no implementation (native or WASM)", componentID)
		}

		if execErr != nil {
			return nil, fmt.Errorf("error executing step %q in workflow %q: %w", step, workflow.ID(), execErr)
		}

		// 2g. Store the result back into the execution context for subsequent steps.
		// For now, we store the result under a generic "result" key. This should be made configurable.
		execCtx.Set("result", result)
	}

	// 3. Return the final results from the execution context.
	return &SetResponse{
		Results:  execCtx.variables,
		Warnings: nil,
	}, nil
}

func (c *cabinetService) Close(ctx context.Context) error {
	return c.wasmManager.Close(ctx)
}

func (c *cabinetService) PutSchema(s *Schema) error {
	// Validation is now handled by the constructor.
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
