package cabinet

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"centralrelay/pkg/obs"
)

// cicWasmHost defines the interface for interacting with a live WASM instance.
// This allows for mocking in unit tests.
type cicWasmHost interface {
	Process(ctx context.Context, authContextJson, inputJson string) (string, string)
	Get(ctx context.Context, authContextJson, queryJson string) (string, string)
	Init(ctx context.Context, authContextJson, configJson string) (string, string)
	Notify(ctx context.Context, authContextJson, eventJson string) (string, string)
	// Call invokes an arbitrary named guest function; it is the generic-dispatch
	// (WP-03) counterpart of the four fixed-name methods above.
	Call(ctx context.Context, op, authContextJson, inputJson string) (string, string)
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

// cabinetService is the concrete implementation of the Cabinet interface, managing schemas, modules,
// workflows, and their execution via a WASM runtime with optional audit recording.
type cabinetService struct {
	schemas     SchemaStore
	modules     ModuleStore
	workflows   WorkflowStore
	wasmManager wasmManager
	clock       func() string     // injected; returns UTC timestamp for ProofTrace
	recorder    ExecutionRecorder // optional; nil = skip recording

	// evidenceRequired makes an execution whose proof was not persisted (recording
	// failed, or no recorder is configured) fail with ErrEvidenceRequired instead of
	// returning a plain success.
	evidenceRequired bool

	// protected is the set of component IDs (for example "cic.artifact.sign@1.0") that only an
	// authenticated principal may run. Setx is the one execution boundary every entry path
	// (HTTP routes, the activator, tests) goes through, so the decision is made here, once.
	protected map[string]bool

	// genericDispatch gates profile-driven dispatch (WP-03): off by default, so Setx always
	// uses the legacy built-in four-op dispatch (init/process/get/notify) below regardless of
	// whether a module carries an AbiProfile. See WithGenericDispatch.
	genericDispatch bool

	// profileBindings maps a module ID to its admitted ABI-profile binding (WP-03). Only
	// consulted when genericDispatch is on; a module without a binding, or whose current
	// checksum differs from the bound one, always uses the legacy dispatch.
	profileBindings map[string]*ModuleProfileBinding
	// profileMu guards profileBindings and profileRequired: SetModuleProfile writes them at module load, execution reads them.
	profileMu sync.RWMutex
	// effectPolicy is the operator's allowed effect classes for profile-bound operations (WithEffectPolicy); nil = not configured.
	effectPolicy map[string]bool
	// realizationDigest yields the digest of the operator's realization context (WithRealizationContext).
	realizationDigest func() string
	// payloadValidator validates the request and response of profile-bound operations against the schema bodies (WithPayloadValidator).
	payloadValidator PayloadValidator

	// profileRequired lists the modules whose manifest declares an ABI profile (abi.profile): with generic
	// dispatch on they run only through an admitted binding, never through the legacy dispatch.
	profileRequired map[string]bool

	// traceV2, when set, records a ProofTrace V2 artifact for every run (WithTraceV2); nil = off, V1 only.
	traceV2 *traceV2Config
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

// WithProtectedModules marks components that only an authenticated principal (WithPrincipal) may
// run. Setx checks every step of a workflow against this set before it executes anything, so a
// workflow that contains a protected step is refused with ErrUnauthorized no matter which entry
// path started it, and no earlier step has run.
func WithProtectedModules(componentIDs ...string) CabinetOption {
	return func(s *cabinetService) {
		s.protected = make(map[string]bool, len(componentIDs))
		for _, id := range componentIDs {
			s.protected[id] = true
		}
	}
}

// WithEvidenceRequired makes persisted evidence mandatory: when the ProofTrace of an
// execution is not persisted, Setx returns ErrEvidenceRequired (the message carries
// the chain hash) instead of a success. Off by default: a recording outage is then
// reported in SetResponse.Proof only.
func WithEvidenceRequired(required bool) CabinetOption {
	return func(s *cabinetService) {
		s.evidenceRequired = required
	}
}

// WithGenericDispatch turns on profile-driven dispatch (WP-03) for modules that
// have an admitted ABI-profile binding (see WithModuleProfileBindings). Off by
// default: Setx then always uses the legacy built-in four-op dispatch below,
// exactly as before this option existed, including for a module that has a
// binding. Even when enabled, a module without a binding is unaffected: it still
// goes through the legacy dispatch.
//
// Turning this on in production is a separate decision, gated on the #162
// acceptance (WP-02; see jobs/o1-wp03-abi-profile-dispatch/ref/02-abi-profile-wire.md
// §7); this job only provides the switch. See cmd/relay/main.go's
// genericDispatchFromEnv for the production wiring (default off).
func WithGenericDispatch(enabled bool) CabinetOption {
	return func(s *cabinetService) {
		s.genericDispatch = enabled
	}
}

// WithProfileRequiredModules declares which modules need an admitted profile binding (their manifest names a
// profile). With generic dispatch on, such a module without a valid binding is rejected, not run legacy.
func WithProfileRequiredModules(ids ...string) CabinetOption {
	return func(s *cabinetService) {
		if s.profileRequired == nil {
			s.profileRequired = make(map[string]bool, len(ids))
		}
		for _, id := range ids {
			s.profileRequired[id] = true
		}
	}
}

// SetModuleProfile records the outcome of a module's ABI-profile admission at load time (INT-05). A non-nil binding
// registers it and marks the module as needing a profile; nil clears both (a reload of the module without a profile must
// not keep the earlier binding). It is the runtime counterpart of WithProfileRequiredModules and WithModuleProfileBindings.
func (c *cabinetService) SetModuleProfile(moduleID string, b *ModuleProfileBinding) {
	c.profileMu.Lock()
	defer c.profileMu.Unlock()
	if b == nil {
		delete(c.profileBindings, moduleID)
		delete(c.profileRequired, moduleID)
		return
	}
	if c.profileBindings == nil {
		c.profileBindings = map[string]*ModuleProfileBinding{}
	}
	if c.profileRequired == nil {
		c.profileRequired = map[string]bool{}
	}
	c.profileBindings[moduleID] = b
	c.profileRequired[moduleID] = true
}

// ModuleProfile returns the admitted profile binding of a module, if any (diagnostics and acceptance tests).
func (c *cabinetService) ModuleProfile(moduleID string) (*ModuleProfileBinding, bool) {
	c.profileMu.RLock()
	defer c.profileMu.RUnlock()
	b, ok := c.profileBindings[moduleID]
	return b, ok
}

// WithModuleProfileBindings registers admitted ABI-profile bindings (one per module).
// A binding is inert unless WithGenericDispatch(true) is also given.
func WithModuleProfileBindings(bindings ...*ModuleProfileBinding) CabinetOption {
	return func(s *cabinetService) {
		if s.profileBindings == nil {
			s.profileBindings = make(map[string]*ModuleProfileBinding, len(bindings))
		}
		for _, b := range bindings {
			s.profileBindings[b.ModuleID] = b
		}
	}
}

// WithObsSinks wires observability sinks into the underlying WasmManager.
// Both arguments are nil-safe: passing nil disables the corresponding emissions.
func WithObsSinks(logSink obs.LogSink, metricSink obs.MetricSink) CabinetOption {
	return func(s *cabinetService) {
		if wm, ok := s.wasmManager.(*WasmManager); ok {
			wm.SetObsSinks(logSink, metricSink)
		}
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

// Setx executes a workflow based on the function-oriented model, resolving components, validating input schemas,
// dispatching to native or WASM implementations, and recording ProofTrace steps with input/output hashing.
func (c *cabinetService) Setx(ctx context.Context, in *SetPayload) (*SetResponse, error) {
	if c.traceV2 == nil {
		return c.setxRun(ctx, in, nil)
	}
	// V2 trace recording is on: the run's result and error are returned exactly as without it; the V2 artifact is an
	// additional evidence stream for the sink, for a failed run too (trace-on-failure).
	rec := &traceV2Recorder{}
	resp, err := c.setxRun(ctx, in, rec)
	if art, aerr := rec.build(ctx, c.traceV2, in.WorkflowID, in.SourceDigest, err); aerr != nil {
		logrus.WithFields(logrus.Fields{"workflow_id": in.WorkflowID, "error": aerr}).Warn("ProofTrace V2 not recorded")
	} else {
		c.traceV2.sink(ctx, art)
	}
	return resp, err
}

// setxRun is the execution itself; rec (nil when V2 recording is off) is told how far each step got.
func (c *cabinetService) setxRun(ctx context.Context, in *SetPayload, rec *traceV2Recorder) (*SetResponse, error) {
	// 1. Resolve the workflow itself.
	workflow, err := c.GetWorkflow(in.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workflow %q: %w", in.WorkflowID, err)
	}

	// 2. Parse workflow steps into an ExecutionGraph.
	graph, err := ParseExecutionGraph(workflow.Config.Steps)
	if err != nil {
		return nil, fmt.Errorf("workflow %q: %w", workflow.ID(), err)
	}
	rec.setGraph(graph.Steps)

	// 2b. The authorization decision, at the execution boundary: before any step runs, so a
	// refused call has no side effects. The set of protected components is the operator's.
	if len(c.protected) > 0 {
		if _, ok := PrincipalFrom(ctx); !ok {
			for _, st := range graph.Steps {
				if c.protected[string(st.ComponentID)] {
					return nil, fmt.Errorf("%w: workflow %q contains the protected operation %q", ErrUnauthorized, workflow.ID(), st.ComponentID)
				}
			}
		}
	}

	// 3. Initialize executionContext and run each step.
	execCtx := NewExecutionContext(in.Payload)
	traceSteps := make([]ProofTraceStep, 0, len(graph.Steps))

	baseCtx := ctx
	for stepIndex, step := range graph.Steps {
		rec.stepBegin(step)
		ctx = rec.scopeStep(baseCtx, c.traceV2, step) // the step's own context when V2 recording scopes it (INT-08)
		// 3a. Resolve the component.
		moduleDesc, err := c.GetModule(step.ComponentID)
		if err != nil {
			return nil, fmt.Errorf("workflow %q failed: could not resolve component %q: %w", workflow.ID(), step.ComponentID, err)
		}

		rec.stepModule(moduleDesc)

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

		rec.stepInput(inputHash)

		// 3d'. The ABI-profile gate (INT-06/07): effect authorization and the request ceiling, BEFORE the execution phase.
		var gate *profileGate
		if moduleDesc.NativeImpl == nil && moduleDesc.WasmCode != nil {
			g, gerr := c.profileGateFor(ctx, moduleDesc, step, inputData, rec, gateEnvelope{workflowID: in.WorkflowID, stepIndex: stepIndex, inputDigest: "sha256:" + inputHash})
			if gerr != nil {
				return nil, gerr
			}
			gate = g
		}

		// 3e. Dispatch the call based on the module type.
		rec.stepDispatched()
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
			if gate != nil {
				// --- PROFILE-DRIVEN GENERIC DISPATCH (WP-03, off unless WithGenericDispatch(true)) ---
				// Exact operation identity (D-7); the gate already checked the manifest op-set, the effect and the request
				// ceiling. The response ceiling and the timeout are enforced around the call.
				runCtx := ctx
				if gate.timeout > 0 {
					var cancel context.CancelFunc
					runCtx, cancel = context.WithTimeout(ctx, gate.timeout)
					defer cancel()
				}
				dataResult, errorResult = host.Call(runCtx, gate.op.Name, authContextJson, string(inputJson))
				if gate.maxResponse > 0 && len(dataResult) > gate.maxResponse {
					execErr = &ProfileGateError{Phase: RejectionPhaseDispatch, Code: CodeResponseTooLarge, base: ErrInvalid,
						Detail: fmt.Sprintf("the response is %d bytes, the profile allows %d", len(dataResult), gate.maxResponse)}
					dataResult, errorResult = "", ""
				} else if verr := gate.validateResponse(dataResult, errorResult); verr != nil {
					execErr = verr
					dataResult, errorResult = "", ""
				}
			} else {
				// --- LEGACY FOUR-OP DISPATCH (the Relay's built-in core lifecycle profile) ---
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
			}

			if execErr == nil {
				if errorResult != "null" && errorResult != "" {
					if isWasmTimeout(errorResult) {
						execErr = fmt.Errorf("%w: WASM step %q exceeded time budget", ErrTimeout, step.Raw)
					} else {
						execErr = fmt.Errorf("WASM function %s.%s returned error: %s", step.ComponentID, step.FunctionName, errorResult)
					}
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
		rec.stepDone(outputHash)
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
		WorkflowID:   workflow.ID(),
		SourceDigest: in.SourceDigest,
		Steps:        traceSteps,
		ChainHash:    ComputeChainHashV1(workflow.ID(), in.SourceDigest, traceSteps),
		Timestamp:    clockFn(),
	}

	// 4. Record the execution. A failure is non-fatal by default (Vault may be
	// unavailable) but is always reported in the response; with evidenceRequired it
	// fails the call. The workflow has already run at this point, so the error
	// carries the chain hash: the partial evidence is not lost.
	proof := c.recordProof(ctx, workflow.ID(), trace)
	if c.evidenceRequired && !proof.Persisted {
		return nil, &EvidenceRequiredError{WorkflowID: workflow.ID(), ChainHash: trace.ChainHash, ErrorCode: proof.ErrorCode}
	}

	return &SetResponse{
		Results:  execCtx.variables,
		Warnings: nil,
		Trace:    trace,
		Proof:    proof,
	}, nil
}

// recordProof persists the trace through the configured recorder and reports the outcome.
func (c *cabinetService) recordProof(ctx context.Context, workflowID string, trace *ProofTrace) *ProofStatus {
	if c.recorder == nil {
		logrus.WithField("workflow_id", workflowID).Warn("ProofTrace not persisted: no recorder configured")
		return &ProofStatus{ErrorCode: ProofErrRecorderNotConfigured}
	}
	var (
		commit string
		err    error
	)
	if cr, ok := c.recorder.(CommitRecorder); ok {
		commit, err = cr.RecordCommit(ctx, workflowID, trace)
	} else {
		err = c.recorder.Record(ctx, workflowID, trace)
	}
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"workflow_id": workflowID,
			"error":       err,
		}).Warn("ProofTrace recording failed")
		return &ProofStatus{ErrorCode: ProofErrRecordFailed}
	}
	return &ProofStatus{Persisted: true, Commit: commit}
}

// Close closes the underlying WASM runtime and frees all associated resources.
func (c *cabinetService) Close(ctx context.Context) error {
	return c.wasmManager.Close(ctx)
}

// isWasmTimeout checks whether a WASM errorResult string signals a timeout.
// The cicwasm layer encodes timeouts as {"code":"TIMEOUT",...}.
func isWasmTimeout(errorResult string) bool {
	var v struct {
		Code string `json:"code"`
	}
	return json.Unmarshal([]byte(errorResult), &v) == nil && v.Code == "TIMEOUT"
}

// PutSchema stores a Schema in the underlying SchemaStore.
func (c *cabinetService) PutSchema(s *Schema) error {
	// Validation is now handled by the constructor.
	c.schemas.Put(s)
	return nil
}

// GetSchema retrieves a Schema by ID, returning ErrNotFound if not present.
func (c *cabinetService) GetSchema(id string) (*Schema, error) {
	if s, ok := c.schemas.Get(id); ok {
		return s, nil
	}
	return nil, ErrNotFound
}

// PutModule stores a ModuleDescriptor in the underlying ModuleStore.
func (c *cabinetService) PutModule(m *ModuleDescriptor) error {
	// Validation is now handled by the constructor.
	c.modules.Put(m)
	return nil
}

// GetModule retrieves a ModuleDescriptor by ID, returning ErrNotFound if not present.
func (c *cabinetService) GetModule(id string) (*ModuleDescriptor, error) {
	if m, ok := c.modules.Get(id); ok {
		return m, nil
	}
	return nil, ErrNotFound
}

// PutWorkflow stores a Workflow in the underlying WorkflowStore.
func (c *cabinetService) PutWorkflow(w *Workflow) error {
	// Validation is now handled by the constructor.
	c.workflows.Put(w)
	return nil
}

// GetWorkflow retrieves a Workflow by ID, returning ErrNotFound if not present.
func (c *cabinetService) GetWorkflow(id string) (*Workflow, error) {
	if w, ok := c.workflows.Get(id); ok {
		return w, nil
	}
	return nil, ErrNotFound
}

// profileBindingFor decides which dispatch a module uses (review F2). Four distinct states:
//   - generic dispatch off, or an explicit legacy module (no binding, no profile required): (nil, nil), legacy;
//   - a valid binding for exactly this module content: (binding, nil), profile-driven;
//   - a binding that belongs to different content of the same module ID (stale), or a module that is declared
//     to need a profile but has no admitted binding: (nil, error), rejected, never a legacy fallback.
func (c *cabinetService) profileBindingFor(m *ModuleDescriptor) (*ModuleProfileBinding, error) {
	if !c.genericDispatch {
		return nil, nil
	}
	c.profileMu.RLock()
	b, ok := c.profileBindings[m.ID()]
	required := c.profileRequired[m.ID()]
	c.profileMu.RUnlock()
	if ok {
		if b.ModuleChecksum != m.Checksum() {
			return nil, fmt.Errorf("%w: module %q has an ABI-profile binding for different content (stale binding); refusing to run it", ErrInvalid, m.ID())
		}
		return b, nil
	}
	if required {
		return nil, fmt.Errorf("%w: module %q declares an ABI profile but none is admitted for it; refusing to run it", ErrInvalid, m.ID())
	}
	return nil, nil
}
