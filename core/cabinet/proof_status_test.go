package cabinet

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRecorder is a plain ExecutionRecorder (no commit hash).
type fakeRecorder struct{ err error }

func (f *fakeRecorder) Record(context.Context, string, interface{}) error { return f.err }

// fakeCommitRecorder is an ExecutionRecorder that also names the persisted record.
type fakeCommitRecorder struct {
	commit string
	err    error
}

func (f *fakeCommitRecorder) Record(ctx context.Context, id string, tr interface{}) error {
	_, err := f.RecordCommit(ctx, id, tr)
	return err
}

func (f *fakeCommitRecorder) RecordCommit(context.Context, string, interface{}) (string, error) {
	return f.commit, f.err
}

// newProofTestService builds a service with one native "echo" workflow.
func newProofTestService(t *testing.T, rec ExecutionRecorder, required bool) (*cabinetService, *SetPayload) {
	t.Helper()
	ctx := context.Background()
	modStore := NewMemModuleStore()
	wfStore := NewMemWorkflowStore()

	native := make(NativeFunctions)
	native["echo"] = CoreEcho
	mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "echo", Version: "1.0"}, "native", nil, nil, nil)
	require.NoError(t, err)
	mod.NativeImpl = native
	modStore.Put(mod)

	wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.proof", Steps: []string{"echo@1.0.echo(input)"}}, WorkflowSigning{})
	require.NoError(t, err)
	wfStore.Put(wf)

	svc := &cabinetService{
		modules:          modStore,
		workflows:        wfStore,
		wasmManager:      new(MockWasmManager),
		clock:            func() string { return "2026-01-01T00:00:00Z" },
		recorder:         rec,
		evidenceRequired: required,
	}
	return svc, &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"msg": "hello"}}
}

func TestSetx_Proof_RecorderHealthy_PersistedWithCommit(t *testing.T) {
	svc, p := newProofTestService(t, &fakeCommitRecorder{commit: "abc123"}, false)
	resp, err := svc.Setx(context.Background(), p)
	require.NoError(t, err)
	require.NotNil(t, resp.Proof)
	assert.True(t, resp.Proof.Persisted)
	assert.Equal(t, "abc123", resp.Proof.Commit)
	assert.Empty(t, resp.Proof.ErrorCode)
	require.NotNil(t, resp.Trace)
}

func TestSetx_Proof_PlainRecorderHealthy_PersistedWithoutCommit(t *testing.T) {
	svc, p := newProofTestService(t, &fakeRecorder{}, false)
	resp, err := svc.Setx(context.Background(), p)
	require.NoError(t, err)
	require.NotNil(t, resp.Proof)
	assert.True(t, resp.Proof.Persisted)
	assert.Empty(t, resp.Proof.Commit, "a recorder that cannot name the record returns no commit")
}

// Recorder outage, evidence not required: the call succeeds, but the response says the
// proof was not persisted, with an error code, and keeps the trace (chain hash).
func TestSetx_Proof_RecorderOutage_NotRequired_ReportsNotPersisted(t *testing.T) {
	for name, rec := range map[string]ExecutionRecorder{
		"plain":  &fakeRecorder{err: errors.New("vault unavailable at secret/ci-token")},
		"commit": &fakeCommitRecorder{err: errors.New("vault unavailable at secret/ci-token")},
	} {
		t.Run(name, func(t *testing.T) {
			svc, p := newProofTestService(t, rec, false)
			resp, err := svc.Setx(context.Background(), p)
			require.NoError(t, err, "evidence is not required: the execution stays a success")
			require.NotNil(t, resp.Proof, "the outcome must always be reported")
			assert.False(t, resp.Proof.Persisted)
			assert.Equal(t, ProofErrRecordFailed, resp.Proof.ErrorCode)
			assert.Empty(t, resp.Proof.Commit)
			require.NotNil(t, resp.Trace, "the trace is kept so partial evidence is not lost")
			assert.NotEmpty(t, resp.Trace.ChainHash)

			raw, err := json.Marshal(resp)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "vault", "the recorder error text must not reach the response")
			assert.NotContains(t, string(raw), "secret/ci-token")
			assert.Contains(t, string(raw), `"proof":{"persisted":false,"error_code":"record_failed"}`)
		})
	}
}

// Recorder outage, evidence required: an explicit failure, never a plain success.
func TestSetx_Proof_RecorderOutage_Required_Fails(t *testing.T) {
	svc, p := newProofTestService(t, &fakeCommitRecorder{err: errors.New("vault unavailable")}, true)
	resp, err := svc.Setx(context.Background(), p)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.True(t, IsEvidenceRequired(err))
	assert.Contains(t, err.Error(), "chain_hash=", "the chain hash must survive in the error")
	assert.Contains(t, err.Error(), ProofErrRecordFailed)
	assert.NotContains(t, err.Error(), "vault unavailable", "the recorder error text must not reach the caller")
}

func TestSetx_Proof_NoRecorder(t *testing.T) {
	t.Run("not required reports not persisted", func(t *testing.T) {
		svc, p := newProofTestService(t, nil, false)
		resp, err := svc.Setx(context.Background(), p)
		require.NoError(t, err)
		require.NotNil(t, resp.Proof)
		assert.False(t, resp.Proof.Persisted)
		assert.Equal(t, ProofErrRecorderNotConfigured, resp.Proof.ErrorCode)
	})
	t.Run("required fails", func(t *testing.T) {
		svc, p := newProofTestService(t, nil, true)
		_, err := svc.Setx(context.Background(), p)
		require.Error(t, err)
		assert.True(t, IsEvidenceRequired(err))
		assert.Contains(t, err.Error(), ProofErrRecorderNotConfigured)
	})
}

func TestSetx_Proof_Required_HealthyRecorder_Succeeds(t *testing.T) {
	svc, p := newProofTestService(t, &fakeCommitRecorder{commit: "c0ffee"}, true)
	resp, err := svc.Setx(context.Background(), p)
	require.NoError(t, err)
	assert.True(t, resp.Proof.Persisted)
	assert.Equal(t, "c0ffee", resp.Proof.Commit)
}

func TestWithEvidenceRequired_SetsTheField(t *testing.T) {
	svc := &cabinetService{}
	WithEvidenceRequired(true)(svc)
	assert.True(t, svc.evidenceRequired)
	WithEvidenceRequired(false)(svc)
	assert.False(t, svc.evidenceRequired)
}
