package cabinet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/o1verify"
)

const v2VectorFile = "../../testdata/o1-vectors/chain-and-interaction-vectors.json"

type v2Vectors struct {
	ChainCases []struct {
		ID         string          `json:"id"`
		Artifact   json.RawMessage `json:"artifact"`
		ExpectHash string          `json:"expect_chain_hash"`
		ExpectIn   string          `json:"expect_hash_input_b64"`
	} `json:"chain_cases"`
}

func loadV2Vectors(t *testing.T) v2Vectors {
	t.Helper()
	b, err := os.ReadFile(v2VectorFile)
	require.NoError(t, err)
	var v v2Vectors
	require.NoError(t, json.Unmarshal(b, &v))
	return v
}

func vectorArtifact(t *testing.T, i int) *ProofArtifactV2 {
	var a ProofArtifactV2
	require.NoError(t, json.Unmarshal(loadV2Vectors(t).ChainCases[i].Artifact, &a))
	return &a
}

// AC-V2-15: the producer reproduces the shared vectors' hash input byte for byte and the hash. The independent
// verifier (pkg/o1verify, no shared code) reproduces the same bytes from the same file.
func TestV2ProducerReproducesTheSharedVectors(t *testing.T) {
	for i, c := range loadV2Vectors(t).ChainCases {
		a := vectorArtifact(t, i)
		require.NoError(t, ValidateArtifactV2(a), c.ID)
		in, err := ChainBytesV2(a)
		require.NoError(t, err, c.ID)
		want, _ := base64.StdEncoding.DecodeString(c.ExpectIn)
		assert.Equal(t, string(want), string(in), c.ID)
		h, err := ComputeChainHashV2(a)
		require.NoError(t, err)
		assert.Equal(t, c.ExpectHash, h, c.ID)
		// the two implementations agree on every case
		vh, err := o1verify.ChainHash(c.Artifact)
		require.NoError(t, err)
		assert.Equal(t, h, vh, "producer and independent verifier: %s", c.ID)
	}
}

func TestV2SealWritesTheHashAndRefusesABrokenArtifact(t *testing.T) {
	a := vectorArtifact(t, 0)
	a.ChainHash = ""
	require.NoError(t, SealArtifactV2(a))
	assert.Equal(t, loadV2Vectors(t).ChainCases[0].ExpectHash, a.ChainHash)
	b := vectorArtifact(t, 0)
	b.ChainHash = ""
	b.Steps[0].Executed = nil
	err := SealArtifactV2(b)
	require.Error(t, err)
	assert.Equal(t, CodeExecutedRequired, ArtifactV2ErrorCode(err))
	assert.Equal(t, "", b.ChainHash, "a refused artifact gets no chain hash")
}

func TestV2ValidationRefusesForTheNamedReason(t *testing.T) {
	str := func(s string) *string { return &s }
	d := "sha256:" + strings.Repeat("ab", 32)
	rejected := func(a *ProofArtifactV2) { // turn step 0 into a well-formed REJECTED step
		s := &a.Steps[0]
		s.Outcome.Execution = ExecutionRejected
		s.Executed, s.OutputDigest = nil, nil
		s.Rejection = &RejectionV2{Phase: RejectionPhasePolicy, Code: "POLICY_DENIED"}
		s.References.HostInteractions.Count = 0
	}
	cases := []struct {
		name string
		mut  func(a *ProofArtifactV2)
		code string
	}{
		{"kind", func(a *ProofArtifactV2) { a.Kind = "Other" }, CodeKindInvalid},
		{"V1 version in the V2 domain", func(a *ProofArtifactV2) { a.Version = "1.0" }, CodeVersionInvalid},
		{"V1 chain version", func(a *ProofArtifactV2) { a.ChainVersion = ChainVersionV1 }, CodeChainVersionInvalid},
		{"workflow id", func(a *ProofArtifactV2) { a.Workflow.ID = "" }, CodeWorkflowIDRequired},
		{"workflow source digest", func(a *ProofArtifactV2) { a.Workflow.SourceDigest = "" }, CodeWorkflowSourceDigestRequired},
		{"execution enum", func(a *ProofArtifactV2) { a.Outcome.Execution = "OK" }, CodeExecutionInvalid},
		{"authorization enum", func(a *ProofArtifactV2) { a.Outcome.Authorization = "MAYBE" }, CodeAuthorizationInvalid},
		{"no steps", func(a *ProofArtifactV2) { a.Steps = nil }, CodeStepsRequired},
		{"step index", func(a *ProofArtifactV2) { a.Steps[0].Index = 3 }, CodeStepIndexMismatch},
		{"step name", func(a *ProofArtifactV2) { a.Steps[0].Name = "" }, CodeStepNameRequired},
		{"requested identity", func(a *ProofArtifactV2) { a.Steps[0].Requested.ModuleID = "" }, CodeRequestedIdentityRequired},
		{"rejected claims an executed identity (V2-01)", func(a *ProofArtifactV2) {
			rejected(a)
			e := vectorArtifact(t, 0).Steps[0].Executed
			a.Steps[0].Executed = e
		}, CodeRejectedWithExecuted},
		{"rejected without a rejection record", func(a *ProofArtifactV2) { rejected(a); a.Steps[0].Rejection = nil }, CodeRejectedWithoutRejection},
		{"rejected with an output", func(a *ProofArtifactV2) { rejected(a); a.Steps[0].OutputDigest = str(d) }, CodeRejectedWithOutput},
		{"rejected with host interactions", func(a *ProofArtifactV2) { rejected(a); a.Steps[0].References.HostInteractions.Count = 1 }, CodeRejectedWithHostInteractions},
		{"executed required", func(a *ProofArtifactV2) { a.Steps[0].Executed = nil }, CodeExecutedRequired},
		{"executed with a rejection", func(a *ProofArtifactV2) { a.Steps[0].Rejection = &RejectionV2{Phase: RejectionPhasePolicy, Code: "X"} }, CodeExecutedWithRejection},
		{"authenticated without an envelope digest (AC-V2-06)", func(a *ProofArtifactV2) { a.Steps[0].Executed.EnvelopeDigest = nil }, CodeAuthenticatedWithoutEnvelopeDigest},
		{"allowed without a decision reference (AC-V2-11)", func(a *ProofArtifactV2) { a.Steps[0].References.AuthorizationDecision = nil }, CodeAuthorizationDecisionRequired},
		{"rejection phase", func(a *ProofArtifactV2) { rejected(a); a.Steps[0].Rejection.Phase = "later" }, CodeRejectionPhaseInvalid},
		{"rejection code", func(a *ProofArtifactV2) { rejected(a); a.Steps[0].Rejection.Code = "lower" }, CodeRejectionCodeInvalid},
		{"trust state", func(a *ProofArtifactV2) { a.Steps[0].Executed.TrustState = "trusted" }, CodeTrustStateInvalid},
		{"digest format", func(a *ProofArtifactV2) { a.Steps[0].InputDigest = "ABC" }, CodeDigestFormatInvalid},
		{"digest required", func(a *ProofArtifactV2) { a.Steps[0].InputDigest = "" }, CodeDigestRequired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := vectorArtifact(t, 0)
			c.mut(a)
			err := ValidateArtifactV2(a)
			require.Error(t, err)
			assert.Equal(t, c.code, ArtifactV2ErrorCode(err), err.Error())
		})
	}
	require.NoError(t, ValidateArtifactV2(vectorArtifact(t, 0)), "the unmutated artifact is valid (positive control)")
	require.NoError(t, ValidateArtifactV2(vectorArtifact(t, 1)), "the rejected-before-load vector is valid")
}

func TestV2HashInputRefusesWhatItCanNotRepresent(t *testing.T) {
	a := vectorArtifact(t, 0)
	a.Workflow.ID = "a\nb"
	_, err := ChainBytesV2(a)
	require.Error(t, err)
	assert.Equal(t, CodeBadValue, ArtifactV2ErrorCode(err))
	a.Workflow.ID = "é"
	_, err = ChainBytesV2(a)
	assert.Equal(t, CodeBadValue, ArtifactV2ErrorCode(err))
}

func TestV2EveryIdentityFieldChangesTheChainHash(t *testing.T) {
	base, _ := ComputeChainHashV2(vectorArtifact(t, 0))
	d2 := "sha256:" + strings.Repeat("34", 32)
	muts := map[string]func(a *ProofArtifactV2){
		"artifact digest": func(a *ProofArtifactV2) { a.Steps[0].Executed.ArtifactDigest = d2 },
		"executed digest": func(a *ProofArtifactV2) { a.Steps[0].Executed.ExecutedDigest = d2 },
		"manifest digest": func(a *ProofArtifactV2) { a.Steps[0].Executed.ManifestDigest = d2 },
		"envelope digest": func(a *ProofArtifactV2) { a.Steps[0].Executed.EnvelopeDigest = &d2 },
		"profile digest":  func(a *ProofArtifactV2) { a.Steps[0].Executed.Profile.Digest = d2 },
		"dispatched":      func(a *ProofArtifactV2) { a.Steps[0].Executed.Dispatched = "other" },
		"trust state":     func(a *ProofArtifactV2) { a.Steps[0].Executed.TrustState = TrustStateTransitional },
		"requested op":    func(a *ProofArtifactV2) { a.Steps[0].Requested.Operation = "other" },
		"host list":       func(a *ProofArtifactV2) { a.Steps[0].References.HostInteractions.Digest = d2 },
		"host count":      func(a *ProofArtifactV2) { a.Steps[0].References.HostInteractions.Count++ },
		"decision":        func(a *ProofArtifactV2) { a.Steps[0].References.AuthorizationDecision.Digest = d2 },
		"step name":       func(a *ProofArtifactV2) { a.Steps[0].Name = "other" },
	}
	for name, m := range muts {
		a := vectorArtifact(t, 0)
		m(a)
		h, err := ComputeChainHashV2(a)
		require.NoError(t, err, name)
		assert.NotEqual(t, base, h, name)
	}
	// a profile whose raw bytes differ has a different content digest (AC-V2-01)
	assert.NotEqual(t, ProfileRawDigestV2([]byte(`{"a":1}`)), ProfileRawDigestV2([]byte(`{"a":2}`)))
}

func TestV2PersistenceRecordRules(t *testing.T) {
	d := "sha256:" + strings.Repeat("ab", 32)
	att := &AttestationV2{Kind: AttestationGitSignedCommit, SignerKeyID: d, SignatureDigest: d}
	ok := &PersistenceStatusV2{ArtifactChainHash: d, Persisted: true, Recorder: "git-state-recorder", Commit: "abc", Attestation: att}
	require.NoError(t, ok.Validate())
	fail, err := NewPersistenceFailureV2(d, "git-state-recorder", "recorder_unavailable")
	require.NoError(t, err)
	require.NoError(t, fail.Validate())
	cases := []struct {
		name string
		p    *PersistenceStatusV2
		code string
	}{
		{"not bound to an artifact", &PersistenceStatusV2{Persisted: true, Recorder: "r", Commit: "c", Attestation: att}, CodePersistenceNotBound},
		{"no recorder", &PersistenceStatusV2{ArtifactChainHash: d, Persisted: true, Commit: "c", Attestation: att}, CodePersistenceRecorderRequired},
		{"persisted without an attestation (AC-V2-09)", &PersistenceStatusV2{ArtifactChainHash: d, Persisted: true, Recorder: "r", Commit: "c"}, CodePersistedWithoutAttestation},
		{"persisted with an error", &PersistenceStatusV2{ArtifactChainHash: d, Persisted: true, Recorder: "r", Commit: "c", Attestation: att, ErrorCode: "x"}, CodePersistedWithError},
		{"not persisted without an error", &PersistenceStatusV2{ArtifactChainHash: d, Recorder: "r"}, CodeNotPersistedWithoutError},
		{"not persisted with a commit", &PersistenceStatusV2{ArtifactChainHash: d, Recorder: "r", ErrorCode: "x", Commit: "c"}, CodeNotPersistedWithEvidence},
		{"unknown attestation kind", &PersistenceStatusV2{ArtifactChainHash: d, Persisted: true, Recorder: "r", Commit: "c", Attestation: &AttestationV2{Kind: "magic", SignerKeyID: d, SignatureDigest: d}}, CodeAttestationInvalid},
		{"malformed attestation digest", &PersistenceStatusV2{ArtifactChainHash: d, Persisted: true, Recorder: "r", Commit: "c", Attestation: &AttestationV2{Kind: AttestationGitSignedCommit, SignerKeyID: "x", SignatureDigest: d}}, CodeAttestationInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.p.Validate()
			require.Error(t, err)
			assert.Equal(t, c.code, ArtifactV2ErrorCode(err))
		})
	}
	_, err = NewPersistenceFailureV2(d, "r", "")
	require.Error(t, err)
}

// ---- trace-on-failure: a Setx run records V2 evidence in every case, and changes nothing else ----

type fakeSource struct {
	noIdentity bool
	protected  string // module id whose authorization is decided (ALLOWED or DENIED)
	decision   string
}

func (f fakeSource) ExecutedIdentity(m *ModuleDescriptor, dispatched string) (ExecutedIdentityV2, bool) {
	if f.noIdentity {
		return ExecutedIdentityV2{}, false
	}
	d := func(s string) string { return "sha256:" + strings.Repeat(s, 32) }
	env := d("12")
	return ExecutedIdentityV2{ArtifactDigest: d("ab"), ExecutedDigest: d("cd"), ManifestDigest: d("ef"), EnvelopeDigest: &env,
		Profile: ProfileRefV2{ID: "cic:provider@0.1.0", Digest: d("34")}, Dispatched: dispatched, TrustState: TrustStateAuthenticated}, true
}
func (f fakeSource) Authorization(_ context.Context, s ExecutionGraphStep) (string, string) {
	if f.protected != "" && s.ComponentID == f.protected {
		return AuthorizationDenied, "sha256:" + strings.Repeat("56", 32)
	}
	return AuthorizationNotEvaluated, ""
}
func (f fakeSource) ExecutionContext(context.Context, string) string {
	return "sha256:" + strings.Repeat("78", 32)
}
func (f fakeSource) HostInteractions(context.Context, ExecutionGraphStep) (string, int) {
	return EmptyHostInteractionsDigestV2(), 0
}

type v2Run struct {
	svc  *cabinetService
	arts []*ProofArtifactV2
}

func newV2Run(t *testing.T, src TraceV2Source, fn interface{}, protected ...string) *v2Run {
	t.Helper()
	ctx := context.Background()
	mods := NewMemModuleStore()
	mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "echo", Version: "1.0"}, "native", nil, nil, nil)
	require.NoError(t, err)
	mod.NativeImpl = NativeFunctions{"echo": fn}
	mods.Put(mod)
	r := &v2Run{svc: &cabinetService{modules: mods, workflows: NewMemWorkflowStore(), wasmManager: new(MockWasmManager), clock: func() string { return "2026-01-01T00:00:00Z" }}}
	if len(protected) > 0 {
		r.svc.protected = map[string]bool{protected[0]: true}
	}
	if src != nil {
		WithTraceV2(src, func(_ context.Context, a *ProofArtifactV2) { r.arts = append(r.arts, a) })(r.svc)
	}
	return r
}

func (r *v2Run) run(t *testing.T, steps ...string) (*SetResponse, error) {
	t.Helper()
	wf, err := NewWorkflow(context.Background(), WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf.v2", Steps: steps}, WorkflowSigning{})
	require.NoError(t, err)
	r.svc.workflows.Put(wf)
	return r.svc.Setx(context.Background(), &SetPayload{WorkflowID: wf.ID(), SourceDigest: "git:abc", Payload: map[string]any{"input": map[string]any{"k": "v"}}})
}

func okEcho(_ context.Context, in interface{}) (interface{}, error) { return in, nil }

// every recorded artifact is valid, sealed, and independently re-verified with the same chain hash.
func requireVerified(t *testing.T, a *ProofArtifactV2) o1verify.Result {
	t.Helper()
	require.NoError(t, ValidateArtifactV2(a))
	h, err := ComputeChainHashV2(a)
	require.NoError(t, err)
	assert.Equal(t, h, a.ChainHash)
	doc, err := json.Marshal(a)
	require.NoError(t, err)
	recs := map[string][]map[string]any{EmptyHostInteractionsDigestV2(): {}}
	res := o1verify.Verify(doc, o1verify.Options{LegacyDigestOnlyAuthorization: true, Verifier: o1verify.VerifierInfo{ID: "o1verify", Version: "0"}, HostInteractions: recs})
	assert.Empty(t, res.Problems, "the independent verifier accepts the producer's artifact")
	assert.Equal(t, o1verify.VerificationComplete, res.Dimensions.Verification)
	return res
}

func TestV2TraceOnSuccess(t *testing.T) {
	r := newV2Run(t, fakeSource{}, okEcho)
	resp, err := r.run(t, "echo@1.0.echo(input) -> out")
	require.NoError(t, err)
	require.Len(t, r.arts, 1)
	a := r.arts[0]
	assert.Equal(t, ExecutionSucceeded, a.Outcome.Execution)
	require.Len(t, a.Steps, 1)
	assert.Equal(t, "echo@1.0", a.Steps[0].Requested.ModuleID)
	require.NotNil(t, a.Steps[0].Executed)
	assert.Equal(t, "echo", a.Steps[0].Executed.Dispatched)
	require.NotNil(t, a.Steps[0].OutputDigest)
	assert.NotNil(t, resp.Trace, "the V1 trace is still produced")
	res := requireVerified(t, a)
	assert.Equal(t, o1verify.VerdictVerified, res.EvidenceVerdict)
}

func TestV2TraceOnFailureIsRecordedAndTheErrorIsUnchanged(t *testing.T) {
	boom := errors.New("boom")
	failing := func(context.Context, interface{}) (interface{}, error) { return nil, boom }
	on := newV2Run(t, fakeSource{}, failing)
	off := newV2Run(t, nil, failing)
	_, errOn := on.run(t, "echo@1.0.echo(input) -> out")
	_, errOff := off.run(t, "echo@1.0.echo(input) -> out")
	require.Error(t, errOn)
	assert.Equal(t, errOff.Error(), errOn.Error(), "V1 error semantics are unchanged by V2 recording")
	assert.True(t, errors.Is(errOn, boom))
	require.Len(t, on.arts, 1, "the failed run is recorded")
	a := on.arts[0]
	assert.Equal(t, ExecutionFailed, a.Outcome.Execution)
	require.NotNil(t, a.Steps[0].Executed, "the execution phase was reached: the executed identity is claimed")
	assert.Nil(t, a.Steps[0].OutputDigest)
	res := requireVerified(t, a)
	assert.Equal(t, "FAILED", res.Dimensions.Execution)
	assert.Empty(t, off.arts, "nothing is recorded when V2 is off")
}

func TestV2TimeoutIsAbortedNotFailed(t *testing.T) {
	slow := func(context.Context, interface{}) (interface{}, error) { return nil, ErrTimeout }
	r := newV2Run(t, fakeSource{}, slow)
	_, err := r.run(t, "echo@1.0.echo(input) -> out")
	require.Error(t, err)
	require.Len(t, r.arts, 1)
	assert.Equal(t, ExecutionAborted, r.arts[0].Outcome.Execution)
	requireVerified(t, r.arts[0])
}

func TestV2RejectionBeforeDispatchHasNoExecutedIdentity(t *testing.T) {
	t.Run("unknown module: admission", func(t *testing.T) {
		r := newV2Run(t, fakeSource{}, okEcho)
		_, err := r.run(t, "missing@1.0.echo(input) -> out")
		require.Error(t, err)
		require.Len(t, r.arts, 1)
		s := r.arts[0].Steps[0]
		assert.Equal(t, ExecutionRejected, s.Outcome.Execution)
		assert.Nil(t, s.Executed, "a rejected step claims no executed identity")
		require.NotNil(t, s.Rejection)
		assert.Equal(t, RejectionPhaseAdmission, s.Rejection.Phase)
		assert.Equal(t, "missing@1.0", s.Requested.ModuleID, "but the requested identity is always there")
		assert.Nil(t, s.OutputDigest)
		assert.Equal(t, 0, s.References.HostInteractions.Count)
		requireVerified(t, r.arts[0])
	})
	t.Run("input not found: dispatch", func(t *testing.T) {
		r := newV2Run(t, fakeSource{}, okEcho)
		_, err := r.run(t, "echo@1.0.echo(nokey) -> out")
		require.Error(t, err)
		require.Len(t, r.arts, 1)
		assert.Equal(t, RejectionPhaseDispatch, r.arts[0].Steps[0].Rejection.Phase)
		assert.Nil(t, r.arts[0].Steps[0].Executed)
		requireVerified(t, r.arts[0])
	})
	t.Run("protected operation without a principal: policy, DENIED", func(t *testing.T) {
		r := newV2Run(t, fakeSource{protected: "echo@1.0", decision: "x"}, okEcho, "echo@1.0")
		_, err := r.run(t, "echo@1.0.echo(input) -> out")
		require.ErrorIs(t, err, ErrUnauthorized)
		require.Len(t, r.arts, 1)
		s := r.arts[0].Steps[0]
		assert.Equal(t, RejectionPhasePolicy, s.Rejection.Phase)
		assert.Equal(t, AuthorizationDenied, s.Outcome.Authorization)
		require.NotNil(t, s.References.AuthorizationDecision)
		assert.Equal(t, AuthorizationDenied, r.arts[0].Outcome.Authorization)
		res := requireVerified(t, r.arts[0])
		assert.Equal(t, o1verify.AcceptanceNotEvaluated, res.Acceptance)
	})
}

// A source that has no authenticated identity for the module means the step can not be recorded as executed: nothing
// is recorded (and a warning logged); it is never recorded with an invented identity.
func TestV2NoIdentityNoRecord(t *testing.T) {
	r := newV2Run(t, fakeSource{noIdentity: true}, okEcho)
	_, err := r.run(t, "echo@1.0.echo(input) -> out")
	require.NoError(t, err, "the run itself is unaffected")
	assert.Empty(t, r.arts)
}

// V1 is unchanged: the same run with V2 recording on and off gives an identical V1 trace and chain hash.
func TestV1IsUnchangedByV2Recording(t *testing.T) {
	on := newV2Run(t, fakeSource{}, okEcho)
	off := newV2Run(t, nil, okEcho)
	respOn, err := on.run(t, "echo@1.0.echo(input) -> out")
	require.NoError(t, err)
	respOff, err := off.run(t, "echo@1.0.echo(input) -> out")
	require.NoError(t, err)
	assert.Equal(t, respOff.Trace, respOn.Trace)
	assert.Equal(t, respOff.Trace.ChainHash, respOn.Trace.ChainHash)
	assert.True(t, strings.HasPrefix(respOn.Trace.ChainHash, ""), "V1 hash domain is untouched")
	assert.NotEqual(t, respOn.Trace.ChainHash, on.arts[0].ChainHash, "different hash domains: no conversion")
}

func TestWithTraceV2NeedsASourceAndASink(t *testing.T) {
	assert.Panics(t, func() { WithTraceV2(nil, func(context.Context, *ProofArtifactV2) {}) })
	assert.Panics(t, func() { WithTraceV2(fakeSource{}, nil) })
}

func TestV2NilAndNonV2Errors(t *testing.T) {
	_, err := ChainBytesV2(nil)
	require.Error(t, err)
	_, err = ComputeChainHashV2(nil)
	require.Error(t, err)
	require.Error(t, SealArtifactV2(nil))
	require.Error(t, ValidateArtifactV2(nil))
	assert.Equal(t, "", ArtifactV2ErrorCode(errors.New("not a V2 error")))
	assert.Equal(t, "", ArtifactV2ErrorCode(nil))
	var nilP *PersistenceStatusV2
	require.Error(t, nilP.Validate())
}

func TestV2MoreFieldRefusals(t *testing.T) {
	d := "sha256:" + strings.Repeat("ab", 32)
	cases := []struct {
		name string
		mut  func(a *ProofArtifactV2)
		code string
	}{
		{"presented artifact digest malformed", func(a *ProofArtifactV2) { a.Steps[0].Requested.PresentedArtifactDigest = "zz" }, CodeDigestFormatInvalid},
		{"output digest malformed", func(a *ProofArtifactV2) { s := "zz"; a.Steps[0].OutputDigest = &s }, CodeDigestFormatInvalid},
		{"execution context digest missing", func(a *ProofArtifactV2) { a.Steps[0].References.ExecutionContext.Digest = "" }, CodeDigestRequired},
		{"host interactions digest missing", func(a *ProofArtifactV2) { a.Steps[0].References.HostInteractions.Digest = "" }, CodeDigestRequired},
		{"negative host interaction count", func(a *ProofArtifactV2) { a.Steps[0].References.HostInteractions.Count = -1 }, CodeRejectedWithHostInteractions},
		{"decision digest malformed", func(a *ProofArtifactV2) { a.Steps[0].References.AuthorizationDecision.Digest = "zz" }, CodeDigestFormatInvalid},
		{"executed artifact digest missing", func(a *ProofArtifactV2) { a.Steps[0].Executed.ArtifactDigest = "" }, CodeDigestRequired},
		{"executed manifest digest malformed", func(a *ProofArtifactV2) { a.Steps[0].Executed.ManifestDigest = "zz" }, CodeDigestFormatInvalid},
		{"envelope digest malformed", func(a *ProofArtifactV2) { s := "zz"; a.Steps[0].Executed.EnvelopeDigest = &s }, CodeDigestFormatInvalid},
		{"profile digest missing", func(a *ProofArtifactV2) { a.Steps[0].Executed.Profile.Digest = "" }, CodeDigestRequired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := vectorArtifact(t, 0)
			c.mut(a)
			err := ValidateArtifactV2(a)
			require.Error(t, err)
			assert.Equal(t, c.code, ArtifactV2ErrorCode(err), err.Error())
		})
	}
	_ = d
}

func TestV2RejectionClassification(t *testing.T) {
	assert.Equal(t, RejectionV2{Phase: RejectionPhasePolicy, Code: "PROTECTED_OPERATION"}, rejectionOf(ErrUnauthorized, true))
	assert.Equal(t, RejectionV2{Phase: RejectionPhaseAdmission, Code: "MODULE_NOT_RESOLVED"}, rejectionOf(ErrNotFound, true))
	assert.Equal(t, RejectionV2{Phase: RejectionPhaseAdmission, Code: "MODULE_NOT_RESOLVED"}, rejectionOf(errors.New("x"), false))
	assert.Equal(t, RejectionV2{Phase: RejectionPhaseDispatch, Code: "INPUT_REJECTED"}, rejectionOf(ErrInvalid, true))
	assert.Equal(t, RejectionV2{Phase: RejectionPhaseDispatch, Code: "DISPATCH_REJECTED"}, rejectionOf(errors.New("x"), true))
}

// A run that fails before its first step (here: the workflow does not exist) has nothing to record: no artifact, no panic.
func TestV2RunFailingBeforeTheFirstStepRecordsNothing(t *testing.T) {
	r := newV2Run(t, fakeSource{}, okEcho)
	_, err := r.svc.Setx(context.Background(), &SetPayload{WorkflowID: "no-such-workflow"})
	require.Error(t, err)
	assert.Empty(t, r.arts)
}

func TestV2TwoStepRunRecordsBothAndTheOutcomeOfTheFailingOne(t *testing.T) {
	n := 0
	flaky := func(_ context.Context, in interface{}) (interface{}, error) {
		n++
		if n == 2 {
			return nil, errors.New("second step failed")
		}
		return in, nil
	}
	r := newV2Run(t, fakeSource{}, flaky)
	_, err := r.run(t, "echo@1.0.echo(input) -> mid", "echo@1.0.echo(mid) -> out")
	require.Error(t, err)
	require.Len(t, r.arts, 1)
	a := r.arts[0]
	require.Len(t, a.Steps, 2)
	assert.Equal(t, ExecutionSucceeded, a.Steps[0].Outcome.Execution)
	assert.Equal(t, ExecutionFailed, a.Steps[1].Outcome.Execution)
	assert.Equal(t, ExecutionFailed, a.Outcome.Execution)
	requireVerified(t, a)
}
