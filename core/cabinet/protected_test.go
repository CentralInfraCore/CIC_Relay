package cabinet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// protectedFixture builds a service with a counting unprotected "echo" step and a counting
// protected "sign" step, and a workflow that runs echo first and sign second.
type protectedFixture struct {
	svc        *cabinetService
	echoRuns   int
	signRuns   int
	workflowID string
}

func newProtectedFixture(t *testing.T, protected ...string) *protectedFixture {
	t.Helper()
	ctx := context.Background()
	f := &protectedFixture{}
	modStore := NewMemModuleStore()
	wfStore := NewMemWorkflowStore()

	for _, m := range []struct {
		name, fn string
		count    *int
	}{{"echo", "echo", &f.echoRuns}, {"cic.artifact.sign", "sign", &f.signRuns}} {
		count := m.count
		mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: m.name, Version: "1.0"}, "native", nil, nil, nil)
		require.NoError(t, err)
		mod.NativeImpl = NativeFunctions{m.fn: func(_ context.Context, in interface{}) (interface{}, error) {
			*count++
			return in, nil
		}}
		modStore.Put(mod)
	}
	wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.protected",
		Steps: []string{"echo@1.0.echo(input) -> e", "cic.artifact.sign@1.0.sign(e) -> signed"}}, WorkflowSigning{})
	require.NoError(t, err)
	wfStore.Put(wf)
	f.workflowID = wf.ID()

	f.svc = &cabinetService{modules: modStore, workflows: wfStore, wasmManager: new(MockWasmManager),
		clock: func() string { return "2026-01-01T00:00:00Z" }}
	WithProtectedModules(protected...)(f.svc)
	return f
}

func (f *protectedFixture) run(ctx context.Context) (*SetResponse, error) {
	return f.svc.Setx(ctx, &SetPayload{WorkflowID: f.workflowID, Payload: map[string]any{"msg": "hi"}})
}

// The decision is made once, at the execution boundary, before any step runs: a refused
// call has no side effects, not even from the unprotected step in front of the protected one.
func TestSetx_ProtectedStep_RefusedWithoutPrincipal_NoStepRuns(t *testing.T) {
	f := newProtectedFixture(t, "cic.artifact.sign@1.0")
	resp, err := f.run(context.Background())
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.True(t, IsUnauthorized(err))
	assert.Equal(t, 0, f.signRuns, "the protected step must not run")
	assert.Equal(t, 0, f.echoRuns, "no earlier step may have run either: the refusal precedes all side effects")
}

func TestSetx_ProtectedStep_AllowedWithPrincipal(t *testing.T) {
	f := newProtectedFixture(t, "cic.artifact.sign@1.0")
	ctx := WithPrincipal(context.Background(), Principal{ID: "tester", Method: "test"})
	resp, err := f.run(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, f.signRuns)
	assert.Equal(t, 1, f.echoRuns)
}

func TestSetx_NothingProtected_BehavesAsBefore(t *testing.T) {
	f := newProtectedFixture(t) // no protected set
	_, err := f.run(context.Background())
	require.NoError(t, err, "without a protected set an anonymous call is not refused")
	assert.Equal(t, 1, f.signRuns)
}

// A workflow without a protected step is unaffected even when a protected set exists.
func TestSetx_UnprotectedWorkflow_RunsAnonymously(t *testing.T) {
	f := newProtectedFixture(t, "some.other.module@1.0")
	_, err := f.run(context.Background())
	require.NoError(t, err)
}

func TestPrincipal_ContextRoundTrip(t *testing.T) {
	_, ok := PrincipalFrom(context.Background())
	assert.False(t, ok, "a bare context carries no principal")
	p, ok := PrincipalFrom(WithPrincipal(context.Background(), Principal{ID: "x", Method: "m"}))
	require.True(t, ok)
	assert.Equal(t, Principal{ID: "x", Method: "m"}, p)
}
