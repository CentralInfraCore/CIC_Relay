package cabinet

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// cicWasmHost defines the interface for interacting with a live WASM instance.
// This allows for mocking in unit tests.
type cicWasmHost interface {
	Process(ctx context.Context, authContextJson, inputJson string) (string, string)
	Get(ctx context.Context, authContextJson, queryJson string) (string, string)
	Init(ctx context.Context, authContextJson, configJson string) (string, string)
	Notify(ctx context.Context, authContextJson, eventJson string) (string, string)
	Close(ctx context.Context) error
}

// wasmManager defines the interface for a WASM lifecycle manager.
// This allows for mocking in unit tests.
type wasmManager interface {
	NewHostInstance(ctx context.Context, moduleID string) (cicWasmHost, error) // Use the interface type
	Close(ctx context.Context) error
}

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
	wasmManager wasmManager
	clock       func() string    // injected; returns UTC timestamp for ProofTrace
	recorder    ExecutionRecorder // optional; nil = skip recording
}

// CabinetOption is a functional option for configuring a cabinetService.
type CabinetOption func(*cabinetService)

// WithRecorder sets an optional ExecutionRecorder on the service.
// If nil, ProofTrace recording is skipped silently.
func WithRecorder(r ExecutionRecorder) CabinetOption {
	return func(s *cabinetService) {
		s.recorder = r
	}
}

// NewCabinetService creates a new Cabinet service with the given stores and WasmManager configuration.
func NewCabinetService(ctx context.Context, schemas SchemaStore, modules ModuleStore, workflows WorkflowStore, wasmConfig WasmManagerConfig, opts ...CabinetOption) (Cabinet, error) {
	wasmManager, err := NewWasmManager(ctx, modules, wasmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create cabinet service: %w", err)
	}

	svc := &cabinetService{
		schemas:     schemas,
		modules:     modules,
		workflows:   workflows,
		wasmManager: wasmManager,
		clock:       func() string { return time.Now().UTC().Format(time.RFC3339) },
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, nil
}

// Setx executes a workflow based on the function-oriented model.
func (c *cabinetService) Setx(ctx context.Context, in *SetPayload) (*SetResponse, error) {
	// 1. Resolve the workflow itself.
	workflow, err := c.GetWorkflow(in.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workflow %q: %w", in.WorkflowID, err)
	}

	// 2. Parse workflow steps into an ExecutionGraph.
	graph, err := ParseExecutionGraph(workflow.Steps)
	if err != nil {
		return nil, fmt.Errorf("workflow %q: %w", workflow.ID(), err)
	}

	// 3. Initialize executionContext and run each step.
	execCtx := NewExecutionContext(in.Payload)
	traceSteps := make([]ProofTraceStep, 0, len(graph.Steps))

	for _, step := range graph.Steps {
		// 3a. Resolve the component.
		moduleDesc, err := c.GetModule(step.ComponentID)
		if err != nil {
			return nil, fmt.Errorf("workflow %q failed: could not resolve component %q: %w", workflow.ID(), step.ComponentID, err)
		}

		// 3b. Resolve input from the execution context.
		inputData, ok := execCtx.Get(step.InputKey)
		if !ok {
			return nil, fmt.Errorf("workflow %q failed: input key %q not found in context for step %q", workflow.ID(), step.InputKey, step.Raw)
		}

		// 3c. Validate inputData against the module's declared accepted schemas.
		if err := validateInputSchema(moduleDesc, inputData); err != nil {
			return nil, fmt.Errorf("schema validation failed at step %q: %w", step.Raw, err)
		}

		// 3d. Hash input for ProofTrace (before dispatch).
		inputHash, err := hashValue(inputData)
		if err != nil {
			return nil, fmt.Errorf("failed to hash input at step %q: %w", step.Raw, err)
		}

		// 3e. Dispatch the call based on the module type.
		var result interface{}
		var execErr error

		if moduleDesc.NativeImpl != nil {
			// --- NATIVE GO EXECUTION ---
			nativeFunc, ok := moduleDesc.NativeImpl[step.FunctionName]
			if !ok {
				return nil, fmt.Errorf("component %q does not have native function %q", step.ComponentID, step.FunctionName)
			}

			funcValue := reflect.ValueOf(nativeFunc)
			if funcValue.Kind() != reflect.Func || funcValue.Type().NumIn() != 2 ||
				funcValue.Type().In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() ||
				funcValue.Type().In(1) != reflect.TypeOf((*interface{})(nil)).Elem() ||
				funcValue.Type().NumOut() != 2 ||
				funcValue.Type().Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
				return nil, fmt.Errorf("native function %s.%s has unexpected signature", step.ComponentID, step.FunctionName)
			}

			args := []reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(inputData)}
			results := funcValue.Call(args)

			if len(results) > 0 {
				result = results[0].Interface()
			}
			if len(results) > 1 && !results[1].IsNil() {
				execErr = results[1].Interface().(error)
			}
		} else if moduleDesc.WasmCode != nil {
			// --- WASM EXECUTION ---
			host, err := c.wasmManager.NewHostInstance(ctx, step.ComponentID)
			if err != nil {
				return nil, fmt.Errorf("failed to create WASM host instance for module %q: %w", step.ComponentID, err)
			}
			defer host.Close(ctx)

			inputJson, err := json.Marshal(inputData)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal input data for WASM module %q: %w", step.ComponentID, err)
			}

			authContextJson := step.ComponentID
			normalizedFn := strings.ToLower(step.FunctionName)

			var dataResult, errorResult string
			switch normalizedFn {
			case "init":
				dataResult, errorResult = host.Init(ctx, authContextJson, string(inputJson))
			case "process":
				dataResult, errorResult = host.Process(ctx, authContextJson, string(inputJson))
			case "get":
				dataResult, errorResult = host.Get(ctx, authContextJson, string(inputJson))
			case "notify":
				dataResult, errorResult = host.Notify(ctx, authContextJson, string(inputJson))
			default:
				execErr = fmt.Errorf("WASM module %q does not support operation %q", step.ComponentID, step.FunctionName)
			}

			if execErr == nil {
				if errorResult != "null" && errorResult != "" {
					execErr = fmt.Errorf("WASM function %s.%s returned error: %s", step.ComponentID, step.FunctionName, errorResult)
				} else {
					var jsonResult interface{}
					if err := json.Unmarshal([]byte(dataResult), &jsonResult); err != nil {
						result = dataResult
					} else {
						result = jsonResult
					}
				}
			}
		} else {
			return nil, fmt.Errorf("component %s has no implementation (native or WASM)", step.ComponentID)
		}

		if execErr != nil {
			return nil, fmt.Errorf("error executing step %q in workflow %q: %w", step.Raw, workflow.ID(), execErr)
		}

		// 3f. Hash output and record ProofTrace step.
		outputHash, err := hashValue(result)
		if err != nil {
			return nil, fmt.Errorf("failed to hash output at step %q: %w", step.Raw, err)
		}
		traceSteps = append(traceSteps, ProofTraceStep{
			Name:       step.Raw,
			Module:     step.ComponentID,
			InputHash:  inputHash,
			OutputHash: outputHash,
		})

		// 3g. Store the result under the step's declared output key.
		execCtx.Set(step.OutputKey, result)
	}

	// 3. Build ProofTrace.
	clockFn := c.clock
	if clockFn == nil {
		clockFn = func() string { return time.Now().UTC().Format(time.RFC3339) }
	}
	trace := &ProofTrace{
		WorkflowID: workflow.ID(),
		Steps:      traceSteps,
		ChainHash:  computeChainHash(traceSteps),
		Timestamp:  clockFn(),
	}

	// 4. Record the execution (non-fatal: Vault may be unavailable).
	if c.recorder != nil {
		if err := c.recorder.Record(ctx, workflow.ID(), trace); err != nil {
			logrus.WithFields(logrus.Fields{
				"workflow_id": workflow.ID(),
				"error":       err,
			}).Warn("ProofTrace recording failed, continuing")
		}
	}

	return &SetResponse{
		Results:  execCtx.variables,
		Warnings: nil,
		Trace:    trace,
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
