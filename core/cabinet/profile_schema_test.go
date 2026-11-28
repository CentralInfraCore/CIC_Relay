package cabinet

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/schemalite"
)

// schemaBodies is a PayloadValidator over compiled schemalite bodies, keyed by exactly (id, digest).
type schemaBodies map[[2]string]*schemalite.Schema

func (b schemaBodies) ValidatePayload(id, digest string, payload []byte) error {
	s, ok := b[[2]string{id, digest}]
	if !ok {
		return fmt.Errorf("%w: %s %s", ErrSchemaUnavailable, id, digest)
	}
	return s.Validate(payload)
}

func compiled(t *testing.T, doc string) *schemalite.Schema {
	s, err := schemalite.Compile([]byte(doc))
	require.NoError(t, err)
	return s
}

// referenceBodies gives every schema of the reference profile a body: requests are objects that, for observe and invoke,
// require a field; responses require a "data" string.
func referenceBodies(t *testing.T) schemaBodies {
	obj := `{"type":"object"}`
	return schemaBodies{
		{"cic:provider:describe-request", tdDescribeReq}: compiled(t, obj),
		{"cic:provider:module-manifest", tdManifest}:     compiled(t, `{"type":"object","required":["data"],"properties":{"data":{"type":"string"}},"additionalProperties":false}`),
		{"cic:provider:observe-request", tdObserveReq}:   compiled(t, `{"type":"object","required":["k"],"properties":{"k":{"type":"string"}}}`),
		{"cic:provider:observation", tdObservation}:      compiled(t, `{"type":"object","required":["data"],"properties":{"data":{"type":"string"}}}`),
		{"cic:provider:operation-request", tdOpReq}:      compiled(t, obj),
		{"cic:provider:operation-result", tdOpResult}:    compiled(t, obj),
	}
}

func schemaSvc(t *testing.T, v PayloadValidator) (*cabinetService, *MockCicWasmHost, *[]*ProofArtifactV2) {
	t.Helper()
	svc, host, _, arts := gateSvc(t, []string{EffectLocal, EffectExternalRead})
	svc.payloadValidator = v
	return svc, host, arts
}

func TestSchemaValidationIsMandatoryOnTheProfilePath(t *testing.T) {
	svc, host, _ := schemaSvc(t, nil)
	_, err := runWith(t, svc, "describe", map[string]any{"k": "v"})
	requireGateError(t, err, RejectionPhasePolicy, CodeSchemaValidationUnconfigured)
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestARequestThatDoesNotConformIsRejectedBeforeExecution(t *testing.T) {
	svc, host, arts := schemaSvc(t, referenceBodies(t))
	_, err := runWith(t, svc, "observe", map[string]any{"k": 7}) // k must be a string
	requireGateError(t, err, RejectionPhaseDispatch, CodeRequestSchemaInvalid)
	_, err = runWith(t, svc, "observe", map[string]any{"other": "x"}) // k is required
	requireGateError(t, err, RejectionPhaseDispatch, CodeRequestSchemaInvalid)
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	require.Len(t, *arts, 2)
	st := (*arts)[0].Steps[0]
	assert.Equal(t, ExecutionRejected, st.Outcome.Execution, "a non-conforming request is never executed")
	assert.Nil(t, st.Executed)
	assert.Equal(t, RejectionV2{Phase: RejectionPhaseDispatch, Code: CodeRequestSchemaInvalid}, *st.Rejection)
	require.NoError(t, ValidateArtifactV2((*arts)[0]))
}

func TestAConformingRequestAndResponsePass(t *testing.T) {
	svc, host, _ := schemaSvc(t, referenceBodies(t))
	host.On("Call", mock.Anything, "observe", mock.Anything, mock.Anything).Return(`{"data":"ok"}`, "null").Once()
	_, err := runWith(t, svc, "observe", map[string]any{"k": "v"})
	require.NoError(t, err)
}

func TestAResponseThatDoesNotConformFailsTheStepAndIsNotReturned(t *testing.T) {
	svc, host, arts := schemaSvc(t, referenceBodies(t))
	host.On("Call", mock.Anything, "observe", mock.Anything, mock.Anything).Return(`{"data":42}`, "null").Once() // data must be a string
	resp, err := runWith(t, svc, "observe", map[string]any{"k": "v"})
	requireGateError(t, err, RejectionPhaseDispatch, CodeResponseSchemaInvalid)
	assert.Nil(t, resp, "the non-conforming response is not handed to the caller")

	require.Len(t, *arts, 1)
	st := (*arts)[0].Steps[0]
	assert.Equal(t, ExecutionFailed, st.Outcome.Execution, "the call happened: the step is executed and failed, not rejected")
	require.NotNil(t, st.Executed)
	assert.Nil(t, st.OutputDigest)
	assert.Equal(t, AuthorizationAllowed, st.Outcome.Authorization)
}

func TestAMissingOrMismatchedSchemaBodyFailsClosed(t *testing.T) {
	// no body at all for the request schema
	bodies := referenceBodies(t)
	delete(bodies, [2]string{"cic:provider:observe-request", tdObserveReq})
	svc, host, _ := schemaSvc(t, bodies)
	_, err := runWith(t, svc, "observe", map[string]any{"k": "v"})
	requireGateError(t, err, RejectionPhasePolicy, CodeSchemaUnavailable)

	// a body for the same id but another digest is not the schema the profile pinned
	bodies = referenceBodies(t)
	delete(bodies, [2]string{"cic:provider:observe-request", tdObserveReq})
	bodies[[2]string{"cic:provider:observe-request", "sha256:" + "00" + tdObserveReq[9:]}] = compiled(t, `{}`)
	svc, host2, _ := schemaSvc(t, bodies)
	_, err = runWith(t, svc, "observe", map[string]any{"k": "v"})
	requireGateError(t, err, RejectionPhasePolicy, CodeSchemaUnavailable)

	// the response body is missing: the call is made (the request was fine), then the response cannot be validated
	bodies = referenceBodies(t)
	delete(bodies, [2]string{"cic:provider:observation", tdObservation})
	svc, host3, _ := schemaSvc(t, bodies)
	host3.On("Call", mock.Anything, "observe", mock.Anything, mock.Anything).Return(`{"data":"ok"}`, "null").Once()
	_, err = runWith(t, svc, "observe", map[string]any{"k": "v"})
	requireGateError(t, err, RejectionPhasePolicy, CodeSchemaUnavailable)
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	host2.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAGuestErrorIsNotAResponseSchemaFailure(t *testing.T) {
	svc, host, _ := schemaSvc(t, referenceBodies(t))
	host.On("Call", mock.Anything, "observe", mock.Anything, mock.Anything).Return("", `{"code":"HOST_ERROR","message":"boom"}`).Once()
	_, err := runWith(t, svc, "observe", map[string]any{"k": "v"})
	require.Error(t, err)
	var ge *ProfileGateError
	assert.False(t, errors.As(err, &ge), "the guest's own error surfaces as itself, not as a schema failure: %v", err)
}

// INT-07: the profile timeout actually interrupts a guest that does not return. A guest that spins for ~2e9 iterations would
// run far past the timeout; the call must be cut short by the profile's 200 ms, not by the (much longer) default.
func TestTheProfileTimeoutInterruptsARunningGuest(t *testing.T) {
	ctx := context.Background()
	pr, digest, pin := admittedBase(t)
	longWasm := loadWasmFile(t, "long_running.wasm")
	mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "spin", Version: "1.0"}, "wasm", nil, nil, longWasm)
	require.NoError(t, err)
	b, err := NewModuleProfileBinding(mod, tdEnvelope, pr, digest, pin, []string{"describe"})
	require.NoError(t, err)
	b.Profile.Limits = &AbiLimits{MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, TimeoutMs: 200}

	store := NewMemModuleStore()
	store.Put(mod)
	svc, err := NewCabinetService(ctx, NewMemSchemaStore(), store, NewMemWorkflowStore(), WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 30},
		WithGenericDispatch(true), WithModuleProfileBindings(b), WithPayloadValidator(anyPayload{}))
	require.NoError(t, err)
	t.Cleanup(func() { svc.Close(ctx) })

	wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.spin", Steps: []string{mod.ID() + ".describe(input)"}}, WorkflowSigning{})
	require.NoError(t, err)
	require.NoError(t, svc.PutWorkflow(wf))

	start := time.Now()
	_, err = svc.Setx(ctx, &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"k": "v"}})
	elapsed := time.Since(start)
	require.Error(t, err, "a guest that never returns in time is an error")
	assert.Less(t, elapsed, 5*time.Second, "interrupted by the 200 ms profile timeout, not by the 30 s default (took %v)", elapsed)
	assert.True(t, errors.Is(err, ErrTimeout), "the error is the named timeout: %v", err)
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond, "the guest ran until the profile timeout, it did not fail for another reason at once")
}
