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
		res := o1verify.Verify(doc, o1verify.Options{LegacyDigestOnlyAuthorization: true, Verifier: o1verify.VerifierInfo{ID: "o1verify", Version: "0"},
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

}

func baseRecord() AuthorizationRecordV2 {
	return AuthorizationRecordV2{ModuleID: "m@1", EnvelopeDigest: "sha256:" + strings.Repeat("1", 64), ProfileID: "p", ProfileDigest: "sha256:" + strings.Repeat("2", 64),
		Operation: "observe", HostBoundary: "sign+send", Effect: EffectExternalRead, EffectResolved: "static", Principal: "service:svc/bearer",
		RealizationContextDigest: "sha256:" + strings.Repeat("3", 64), PolicyDigest: PolicyDigestOf([]string{"local"}), Decision: AuthorizationAllowed, Reason: "ok",
		WorkflowID: "wf@1", StepIndex: 0, InputDigest: "sha256:" + strings.Repeat("4", 64)}
}

// every field of the record is covered by its digest: changing any one changes it
func TestAuthorizationRecordDigestCoversEveryField(t *testing.T) {
	base := baseRecord()
	d := base.Digest()
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, d)
	assert.Equal(t, d, baseRecord().Digest())
	assert.True(t, strings.HasPrefix(base.Canonical(), "CICAUTHZv2\nmodule_id=m@1\n"))
	mutations := map[string]func(*AuthorizationRecordV2){
		"module_id": func(r *AuthorizationRecordV2) { r.ModuleID = "x" }, "envelope_digest": func(r *AuthorizationRecordV2) { r.EnvelopeDigest = "x" },
		"profile_id": func(r *AuthorizationRecordV2) { r.ProfileID = "x" }, "profile_digest": func(r *AuthorizationRecordV2) { r.ProfileDigest = "x" },
		"operation": func(r *AuthorizationRecordV2) { r.Operation = "x" }, "host_boundary": func(r *AuthorizationRecordV2) { r.HostBoundary = "x" },
		"effect": func(r *AuthorizationRecordV2) { r.Effect = "x" }, "effect_resolved": func(r *AuthorizationRecordV2) { r.EffectResolved = "x" },
		"principal": func(r *AuthorizationRecordV2) { r.Principal = "x" }, "realization": func(r *AuthorizationRecordV2) { r.RealizationContextDigest = "x" },
		"policy": func(r *AuthorizationRecordV2) { r.PolicyDigest = "x" }, "decision": func(r *AuthorizationRecordV2) { r.Decision = "x" },
		"reason": func(r *AuthorizationRecordV2) { r.Reason = "x" }, "workflow": func(r *AuthorizationRecordV2) { r.WorkflowID = "x" },
		"step_index": func(r *AuthorizationRecordV2) { r.StepIndex = 1 }, "input_digest": func(r *AuthorizationRecordV2) { r.InputDigest = "x" },
	}
	for name, m := range mutations {
		r := baseRecord()
		m(&r)
		assert.NotEqual(t, d, r.Digest(), "the digest covers %s", name)
	}
	// a line break in a value cannot forge the next field
	forged := baseRecord()
	forged.Reason = "ok\ndecision=denied"
	lines := 0
	for _, l := range strings.Split(forged.Canonical(), "\n") {
		if strings.HasPrefix(l, "decision=") {
			lines++
		}
	}
	assert.Equal(t, 1, lines, "a value with a line break does not create a second decision line")
}

func TestPolicyDigestAndPrincipalString(t *testing.T) {
	assert.Equal(t, PolicyDigestOf([]string{"b", "a"}), PolicyDigestOf([]string{"a", "b"}), "order-independent")
	assert.NotEqual(t, PolicyDigestOf([]string{"a"}), PolicyDigestOf([]string{"a", "b"}))
	assert.Equal(t, "service:svc/bearer", PrincipalString(Principal{ID: "svc", Method: "bearer", Type: PrincipalTypeService}, true))
	assert.Equal(t, "unavailable", PrincipalString(Principal{}, false))
}

// the cabinet produces the full, bound record for a decision, and hands it to an archiving source
type archivingSource struct {
	fakeSource
	got map[string]AuthorizationRecordV2
}

func (a *archivingSource) ArchiveDecision(_ context.Context, d string, r AuthorizationRecordV2) {
	if a.got == nil {
		a.got = map[string]AuthorizationRecordV2{}
	}
	a.got[d] = r
}

func TestTheGateProducesABoundRecordForEveryDecision(t *testing.T) {
	svc, host, b, _ := gateSvc(t, []string{EffectLocal, EffectExternalRead})
	WithRealizationContext(func() string { return "sha256:" + strings.Repeat("9", 64) })(svc)
	arch := &archivingSource{}
	arts := []*ProofArtifactV2{}
	WithTraceV2(arch, func(_ context.Context, a *ProofArtifactV2) { arts = append(arts, a) })(svc)
	host.On("Call", mock.Anything, "observe", mock.Anything, mock.Anything).Return(`{"data":"o"}`, "null").Twice()

	ctx := WithPrincipal(context.Background(), Principal{ID: "svc-1", Method: "bearer", Type: PrincipalTypeService})
	wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.bound", Steps: []string{gateModuleID + ".observe(input)"}}, WorkflowSigning{})
	require.NoError(t, err)
	svc.workflows.Put(wf)
	_, err = svc.Setx(ctx, &SetPayload{WorkflowID: wf.ID(), SourceDigest: "git:abc", Payload: map[string]any{"k": "v"}})
	require.NoError(t, err)

	require.Len(t, arts, 1)
	st := arts[0].Steps[0]
	rec, ok := arch.got[st.References.AuthorizationDecision.Digest]
	require.True(t, ok, "the record behind the digest was archived")
	assert.Equal(t, rec.Digest(), st.References.AuthorizationDecision.Digest)
	assert.Equal(t, "service:svc-1/bearer", rec.Principal)
	assert.Equal(t, b.EnvelopeDigest, rec.EnvelopeDigest)
	assert.Equal(t, st.Executed.EnvelopeDigest != nil, true)
	assert.Equal(t, b.ProfileDigest, rec.ProfileDigest)
	assert.Equal(t, "observe", rec.Operation)
	assert.Equal(t, EffectExternalRead, rec.Effect)
	assert.Equal(t, "sha256:"+strings.Repeat("9", 64), rec.RealizationContextDigest)
	assert.Equal(t, PolicyDigestOf([]string{EffectLocal, EffectExternalRead}), rec.PolicyDigest)
	assert.Equal(t, AuthorizationAllowed, rec.Decision)
	assert.Equal(t, wf.ID(), rec.WorkflowID)
	assert.Equal(t, 0, rec.StepIndex)
	assert.Equal(t, st.InputDigest, rec.InputDigest, "bound to the concrete input of this very execution")

	// no principal in the context: the record says so, it does not invent one
	_, err = svc.Setx(context.Background(), &SetPayload{WorkflowID: wf.ID(), SourceDigest: "git:abc", Payload: map[string]any{"k": "v"}})
	require.NoError(t, err)
	require.Len(t, arts, 2)
	second := arch.got[arts[1].Steps[0].References.AuthorizationDecision.Digest]
	assert.Equal(t, "unavailable", second.Principal)
	assert.NotEqual(t, rec.Digest(), second.Digest(), "another principal (here none) is another decision")
}

func TestProfileGateErrorRendersItsCode(t *testing.T) {
	e := &ProfileGateError{Phase: RejectionPhasePolicy, Code: CodeEffectNotAuthorized, Detail: "d", base: ErrInvalid}
	assert.Contains(t, e.Error(), CodeEffectNotAuthorized)
	assert.True(t, errors.Is(e, ErrInvalid))
}
