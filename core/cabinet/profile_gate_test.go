package cabinet

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/o1verify"
)

// gateModuleID is the id of the module gateSvc built.
var gateModuleID string

// gateSvc is a dispatch service with generic dispatch on, the reference profile bound, the given effect policy, and V2
// recording with the reference source.
func gateSvc(t *testing.T, policy []string) (*cabinetService, *MockCicWasmHost, *ModuleProfileBinding, *[]*ProofArtifactV2) {
	t.Helper()
	mod, b := profiledModule(t, []string{"describe", "observe", "invoke"})
	gateModuleID = mod.ID()
	host := new(MockCicWasmHost)
	svc := dispatchSvc(true, mod, host, b)
	if policy != nil {
		WithEffectPolicy(policy...)(svc)
	}
	arts := &[]*ProofArtifactV2{}
	WithTraceV2(fakeSource{}, func(_ context.Context, a *ProofArtifactV2) { *arts = append(*arts, a) })(svc)
	return svc, host, b, arts
}

func runWith(t *testing.T, svc *cabinetService, op string, input map[string]any) (*SetResponse, error) {
	t.Helper()
	ctx := context.Background()
	wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.gate." + op, Steps: []string{gateModuleID + "." + op + "(input)"}}, WorkflowSigning{})
	require.NoError(t, err)
	svc.workflows.Put(wf)
	return svc.Setx(ctx, &SetPayload{WorkflowID: wf.ID(), SourceDigest: "git:abc", Payload: input}) // the payload IS the step input ("input" key of the execution context)
}

func requireGateError(t *testing.T, err error, phase, code string) {
	t.Helper()
	require.Error(t, err)
	var ge *ProfileGateError
	require.True(t, errors.As(err, &ge), "got %T: %v", err, err)
	assert.Equal(t, code, ge.Code)
	assert.Equal(t, phase, ge.Phase)
	assert.True(t, errors.Is(err, ErrInvalid))
}

func TestGateEffectPolicyAllowsAndDeniesByTheResolvedEffect(t *testing.T) {
	svc, host, _, _ := gateSvc(t, []string{EffectLocal, EffectExternalRead})
	host.On("Call", mock.Anything, "describe", mock.Anything, mock.Anything).Return(`{"data":"d"}`, "null").Once()
	host.On("Call", mock.Anything, "observe", mock.Anything, mock.Anything).Return(`{"data":"o"}`, "null").Once()
	host.On("Call", mock.Anything, "invoke", mock.Anything, mock.Anything).Return(`{"data":"i"}`, "null").Once()

	_, err := runWith(t, svc, "describe", map[string]any{"k": "v"})
	require.NoError(t, err, "local is allowed")
	_, err = runWith(t, svc, "observe", map[string]any{"k": "v"})
	require.NoError(t, err, "a sign+send external-read operation dispatches once an effect policy is configured")
	_, err = runWith(t, svc, "invoke", map[string]any{"operation_role": "read"})
	require.NoError(t, err, "contract-dependent, resolved to external-read from the request field")

	for name, in := range map[string]map[string]any{
		"resolved to external-write": {"operation_role": "action"},
		"unknown value: the default": {"operation_role": "weird"},
		"field absent: the default":  {"k": "v"},
		"field of the wrong type":    {"operation_role": 7},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runWith(t, svc, "invoke", in)
			requireGateError(t, err, RejectionPhasePolicy, CodeEffectNotAuthorized)
		})
	}
	host.AssertNumberOfCalls(t, "Call", 3)
}

func TestGateAnEmptyPolicyAllowsNothing(t *testing.T) {
	svc, host, _, _ := gateSvc(t, []string{})
	_, err := runWith(t, svc, "describe", map[string]any{"k": "v"})
	requireGateError(t, err, RejectionPhasePolicy, CodeEffectNotAuthorized)
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestGateWriteIsAllowedOnlyWhenTheOperatorSaysSo(t *testing.T) {
	svc, host, _, _ := gateSvc(t, []string{EffectLocal, EffectExternalRead, EffectExternalWrite})
	host.On("Call", mock.Anything, "invoke", mock.Anything, mock.Anything).Return(`{"data":"i"}`, "null").Once()
	_, err := runWith(t, svc, "invoke", map[string]any{"operation_role": "action"})
	require.NoError(t, err)
}

// the decision is evidence, allowed and denied alike, and a denial is a REJECTED step (never executed)
func TestGateDecisionsReachTheV2Artifact(t *testing.T) {
	svc, host, _, arts := gateSvc(t, []string{EffectLocal, EffectExternalRead})
	host.On("Call", mock.Anything, "observe", mock.Anything, mock.Anything).Return(`{"data":"o"}`, "null").Once()

	_, err := runWith(t, svc, "observe", map[string]any{"k": "v"})
	require.NoError(t, err)
	_, err = runWith(t, svc, "invoke", map[string]any{"operation_role": "action"})
	require.Error(t, err)
	require.Len(t, *arts, 2)

	ok, denied := (*arts)[0].Steps[0], (*arts)[1].Steps[0]
	assert.Equal(t, AuthorizationAllowed, ok.Outcome.Authorization)
	require.NotNil(t, ok.References.AuthorizationDecision)
	assert.Equal(t, ExecutionSucceeded, ok.Outcome.Execution)
	require.NotNil(t, ok.Executed)

	assert.Equal(t, AuthorizationDenied, denied.Outcome.Authorization)
	require.NotNil(t, denied.References.AuthorizationDecision)
	assert.Equal(t, ExecutionRejected, denied.Outcome.Execution)
	assert.Nil(t, denied.Executed, "a refused step claims no executed identity")
	require.NotNil(t, denied.Rejection)
	assert.Equal(t, RejectionV2{Phase: RejectionPhasePolicy, Code: CodeEffectNotAuthorized}, *denied.Rejection)
	assert.NotEqual(t, ok.References.AuthorizationDecision.Digest, denied.References.AuthorizationDecision.Digest)

	for _, a := range *arts {
		require.NoError(t, ValidateArtifactV2(a))
		doc, err := json.Marshal(a)
		require.NoError(t, err)
		res := o1verify.Verify(doc, o1verify.Options{Verifier: o1verify.VerifierInfo{ID: "o1verify", Version: "0"},
			HostInteractions: map[string][]map[string]any{EmptyHostInteractionsDigestV2(): {}}})
		assert.Empty(t, res.Problems)
	}
	assert.Equal(t, AuthorizationDenied, (*arts)[1].Outcome.Authorization)
	assert.Equal(t, AuthorizationAllowed, (*arts)[0].Outcome.Authorization)
}

func TestGateRequestAndResponseCeilingsAndTheTimeout(t *testing.T) {
	svc, host, b, _ := gateSvc(t, []string{EffectLocal, EffectExternalRead})
	b.Profile.Limits = &AbiLimits{MaxRequestBytes: 20, MaxResponseBytes: 10, TimeoutMs: 1500}

	_, err := runWith(t, svc, "describe", map[string]any{"k": strings.Repeat("x", 100)})
	requireGateError(t, err, RejectionPhaseDispatch, CodeRequestTooLarge)
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	withDeadline := mock.MatchedBy(func(ctx context.Context) bool {
		d, ok := ctx.Deadline()
		return ok && time.Until(d) > 0 && time.Until(d) <= 1500*time.Millisecond
	})
	host.On("Call", withDeadline, "describe", mock.Anything, mock.Anything).Return(strings.Repeat("y", 11), "null").Once()
	_, err = runWith(t, svc, "describe", map[string]any{"k": "v"})
	requireGateError(t, err, RejectionPhaseDispatch, CodeResponseTooLarge)

	host.On("Call", withDeadline, "describe", mock.Anything, mock.Anything).Return(strings.Repeat("y", 10), "null").Once()
	_, err = runWith(t, svc, "describe", map[string]any{"k": "v"})
	assert.NoError(t, err, "a response exactly at the ceiling passes")
	host.AssertExpectations(t)
}

func TestGateWithoutLimitsInTheProfileEnforcesNone(t *testing.T) {
	svc, host, b, _ := gateSvc(t, []string{EffectLocal})
	b.Profile.Limits = nil
	host.On("Call", mock.Anything, "describe", mock.Anything, mock.Anything).Return(strings.Repeat("z", 5000), "null").Once()
	_, err := runWith(t, svc, "describe", map[string]any{"k": strings.Repeat("x", 5000)})
	assert.NoError(t, err)
}

func TestResolveEffectAndTheDecisionDigest(t *testing.T) {
	op := AbiOperation{Name: "invoke", HostBoundary: "sign+send", Effect: "contract-dependent",
		EffectResolution: &AbiEffectResolution{Source: "request_field", Field: "role", Map: map[string]string{"r": EffectExternalRead}, Default: EffectExternalWrite}}
	e, via := resolveEffect(op, map[string]interface{}{"role": "r"})
	assert.Equal(t, EffectExternalRead, e)
	assert.Equal(t, "request_field:role=r", via)
	e, via = resolveEffect(op, "not a map")
	assert.Equal(t, EffectExternalWrite, e)
	assert.Equal(t, "default", via)
	e, via = resolveEffect(AbiOperation{Effect: EffectLocal}, nil)
	assert.Equal(t, EffectLocal, e)
	assert.Equal(t, "static", via)
	// a contract-dependent operation without a resolution rule keeps its (non-concrete) class: it is then not allowed by any policy
	e, _ = resolveEffect(AbiOperation{Effect: "contract-dependent"}, nil)
	assert.Equal(t, "contract-dependent", e)

	mod, b := profiledModule(t, []string{"describe"})
	d1 := decisionDigest(mod, b, op, EffectExternalRead, "static", []string{"local"}, AuthorizationAllowed, "ok")
	assert.Equal(t, d1, decisionDigest(mod, b, op, EffectExternalRead, "static", []string{"local"}, AuthorizationAllowed, "ok"))
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, d1)
	for name, other := range map[string]string{
		"effect":   decisionDigest(mod, b, op, EffectLocal, "static", []string{"local"}, AuthorizationAllowed, "ok"),
		"via":      decisionDigest(mod, b, op, EffectExternalRead, "default", []string{"local"}, AuthorizationAllowed, "ok"),
		"policy":   decisionDigest(mod, b, op, EffectExternalRead, "static", []string{"local", "external-read"}, AuthorizationAllowed, "ok"),
		"decision": decisionDigest(mod, b, op, EffectExternalRead, "static", []string{"local"}, AuthorizationDenied, "ok"),
		"reason":   decisionDigest(mod, b, op, EffectExternalRead, "static", []string{"local"}, AuthorizationAllowed, "other"),
	} {
		assert.NotEqual(t, d1, other, "the decision record covers its %s", name)
	}
}

func TestProfileGateErrorRendersItsCode(t *testing.T) {
	e := &ProfileGateError{Phase: RejectionPhasePolicy, Code: CodeEffectNotAuthorized, Detail: "d", base: ErrInvalid}
	assert.Contains(t, e.Error(), CodeEffectNotAuthorized)
	assert.True(t, errors.Is(e, ErrInvalid))
}
