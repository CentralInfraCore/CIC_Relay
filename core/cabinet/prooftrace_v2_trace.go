package cabinet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// Trace-on-failure for ProofTrace V2 (O-1 WP-05, V2-01).
//
// The V1 path records a trace only when every step succeeded; a failed run returns an error and no evidence. The V2
// path records what happened in every case, with the REQUESTED identity of each step always present and the EXECUTED
// identity only when the execution phase was reached: a step rejected before any load or dispatch must not claim an
// identity it never had.
//
// It is OFF by default (WithTraceV2). When it is off, Setx behaves exactly as before and V1 is untouched; when it is on,
// the V1 response, the V1 trace and the V1 error semantics are unchanged too: V2 is an additional evidence stream
// delivered to the sink, never a change of what Setx returns.

// TraceV2Source supplies what the cabinet cannot know by itself: the authenticated identity of an executed module (the
// envelope digest of the admission, the ABI profile, the trust state), the authorization decision record, the context
// reference and the host-interaction list. Production wiring of these is integration work (the admission and the
// host-interaction record live in other work packages); the cabinet only asks.
type TraceV2Source interface {
	// ExecutedIdentity returns the executed identity of a module for one operation; ok=false means the module has none
	// (then the step cannot claim one and is not recorded as having executed).
	ExecutedIdentity(m *ModuleDescriptor, dispatched string) (ExecutedIdentityV2, bool)
	// Authorization returns the authorization outcome and the digest of the decision record for a step.
	Authorization(ctx context.Context, step ExecutionGraphStep) (outcome string, decisionDigest string)
	// ExecutionContext returns the digest of the execution context record (policy, realization context, principal).
	ExecutionContext(ctx context.Context, workflowID string) string
	// HostInteractions returns the digest and the count of the step's ordered host-interaction list (denied and failed
	// calls included); a step without host calls returns the empty-list digest and 0.
	HostInteractions(ctx context.Context, step ExecutionGraphStep) (digest string, count int)
}

// StepScoper is an optional extension of a TraceV2Source: it is asked, once per step and before the step dispatches, for the
// context the step runs under. A source uses it to keep the step's host interactions apart (O-1 INT-08): the scoped context
// reaches the host functions the step calls, and the same context is handed back to the source's HostInteractions,
// Authorization and ExecutionContext when the step is sealed.
type StepScoper interface {
	ScopeStep(ctx context.Context, step ExecutionGraphStep) context.Context
}

// TraceV2Sink receives the sealed V2 artifact of every run, successful or not.
type TraceV2Sink func(ctx context.Context, artifact *ProofArtifactV2)

// EmptyHostInteractionsDigestV2 is the CICHOSTINTv1 digest of an empty list.
func EmptyHostInteractionsDigestV2() string {
	sum := sha256.Sum256([]byte("CICHOSTINTv1\ncount=0\n"))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// traceV2Config is a type of the ProofTrace V2 trace recording.
type traceV2Config struct {
	source TraceV2Source
	sink   TraceV2Sink
}

// WithTraceV2 turns on V2 trace recording for every Setx run (off by default). It needs both a source of the
// identities and a sink; a missing one is a programming error and panics at construction time, not at run time.
func WithTraceV2(source TraceV2Source, sink TraceV2Sink) CabinetOption {
	if source == nil || sink == nil {
		panic("cabinet: WithTraceV2 needs a source and a sink")
	}
	return func(s *cabinetService) { s.traceV2 = &traceV2Config{source: source, sink: sink} }
}

// stepRecord is what Setx tells the recorder about one step as it goes.
type stepRecord struct {
	graph      ExecutionGraphStep
	module     *ModuleDescriptor
	inputHash  string
	dispatched bool
	outputHash string
	done       bool
	// decision is the authorization decision the cabinet itself took for the step (the profile gate), if any.
	decision *authzDecision
	// ctx is the context the step ran under when the source scoped it (StepScoper); nil otherwise.
	ctx context.Context
}

// traceV2Recorder collects the step records of one run.
type traceV2Recorder struct {
	graph []ExecutionGraphStep
	steps []*stepRecord
}

// begin is one of the recorder hooks that tell the V2 trace how far a Setx step got; nil-safe, so the V1 path pays nothing.
func (r *traceV2Recorder) begin(s ExecutionGraphStep) {
	r.steps = append(r.steps, &stepRecord{graph: s})
}

// cur is one of the recorder hooks that tell the V2 trace how far a Setx step got; nil-safe, so the V1 path pays nothing.
func (r *traceV2Recorder) cur() *stepRecord {
	if len(r.steps) == 0 {
		return nil
	}
	return r.steps[len(r.steps)-1]
}

// The four hooks below are no-ops on a nil recorder, so the V1 path pays nothing and changes nothing.
func (r *traceV2Recorder) setGraph(steps []ExecutionGraphStep) {
	if r != nil {
		r.graph = steps
	}
}

// stepBegin is one of the recorder hooks that tell the V2 trace how far a Setx step got; nil-safe, so the V1 path pays nothing.
func (r *traceV2Recorder) stepBegin(s ExecutionGraphStep) {
	if r != nil {
		r.begin(s)
	}
}

// scopeStep returns the context the current step runs under: the source's scoped context when it is a StepScoper (it is also
// kept for sealing the step), else base unchanged. Nil-safe: with V2 off the recorder is nil and nothing changes.
func (r *traceV2Recorder) scopeStep(base context.Context, cfg *traceV2Config, step ExecutionGraphStep) context.Context {
	if r == nil || cfg == nil || r.cur() == nil {
		return base
	}
	sc, ok := cfg.source.(StepScoper)
	if !ok {
		return base
	}
	ctx := sc.ScopeStep(base, step)
	r.cur().ctx = ctx
	return ctx
}

// stepDecision records the authorization decision of the profile gate for the current step; nil-safe.
func (r *traceV2Recorder) stepDecision(record AuthorizationRecordV2) {
	if r != nil && r.cur() != nil {
		r.cur().decision = &authzDecision{outcome: record.Decision, record: record}
	}
}

// stepModule is one of the recorder hooks that tell the V2 trace how far a Setx step got; nil-safe, so the V1 path pays nothing.
func (r *traceV2Recorder) stepModule(m *ModuleDescriptor) {
	if r != nil && r.cur() != nil {
		r.cur().module = m
	}
}

// stepInput is one of the recorder hooks that tell the V2 trace how far a Setx step got; nil-safe, so the V1 path pays nothing.
func (r *traceV2Recorder) stepInput(inputHash string) {
	if r != nil && r.cur() != nil {
		r.cur().inputHash = "sha256:" + inputHash
	}
}

// stepDispatched is one of the recorder hooks that tell the V2 trace how far a Setx step got; nil-safe, so the V1 path pays nothing.
func (r *traceV2Recorder) stepDispatched() {
	if r != nil && r.cur() != nil {
		r.cur().dispatched = true
	}
}

// stepDone is one of the recorder hooks that tell the V2 trace how far a Setx step got; nil-safe, so the V1 path pays nothing.
func (r *traceV2Recorder) stepDone(outputHash string) {
	if r != nil && r.cur() != nil {
		r.cur().outputHash, r.cur().done = "sha256:"+outputHash, true
	}
}

// rejectionOf classifies an error that stopped a step before its execution phase was reached.
func rejectionOf(err error, hadModule bool) RejectionV2 {
	var ge *ProfileGateError
	switch {
	case errors.As(err, &ge):
		return RejectionV2{Phase: ge.Phase, Code: ge.Code}
	case errors.Is(err, ErrUnauthorized):
		return RejectionV2{Phase: RejectionPhasePolicy, Code: "PROTECTED_OPERATION"}
	case errors.Is(err, ErrNotFound) || !hadModule:
		return RejectionV2{Phase: RejectionPhaseAdmission, Code: "MODULE_NOT_RESOLVED"}
	case errors.Is(err, ErrInvalid):
		return RejectionV2{Phase: RejectionPhaseDispatch, Code: "INPUT_REJECTED"}
	default:
		return RejectionV2{Phase: RejectionPhaseDispatch, Code: "DISPATCH_REJECTED"}
	}
}

// digestOfRaw is a small helper of the ProofTrace V2 implementation.
func digestOfRaw(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// build assembles the V2 artifact of one run from the recorded steps and the run's error (nil = success).
func (r *traceV2Recorder) build(ctx context.Context, cfg *traceV2Config, workflowID, sourceDigest string, runErr error) (*ProofArtifactV2, error) {
	src := cfg.source
	var steps []ProofStepV2
	if len(r.steps) == 0 && runErr != nil && errors.Is(runErr, ErrUnauthorized) && len(r.graph) > 0 {
		// refused at the authorization boundary before any step began: the first protected step is the rejected one
		r.begin(r.graph[0])
	}
	if len(r.steps) == 0 && runErr != nil {
		return nil, fmt.Errorf("nothing to record: the run failed before its first step (%v)", runErr)
	}
	execution, authorization := ExecutionSucceeded, AuthorizationNotEvaluated
	for pos, rec := range r.steps {
		last := pos == len(r.steps)-1
		failed := runErr != nil && last
		sctx := ctx // the step's own context when the source scoped it
		if rec.ctx != nil {
			sctx = rec.ctx
		}
		ctxDigest := src.ExecutionContext(sctx, workflowID)
		hiDigest, hiCount := src.HostInteractions(sctx, rec.graph)
		st := ProofStepV2{
			Index:       pos,
			Name:        rec.graph.Raw,
			Requested:   RequestedIdentityV2{ModuleID: rec.graph.ComponentID, Operation: rec.graph.FunctionName},
			Outcome:     OutcomeV2{Execution: ExecutionSucceeded, Authorization: AuthorizationNotEvaluated},
			InputDigest: rec.inputHash,
			References: StepReferencesV2{ExecutionContext: DigestRefV2{Digest: ctxDigest},
				HostInteractions: HostInteractionsRefV2{Digest: hiDigest, Count: hiCount}},
		}
		if st.InputDigest == "" {
			st.InputDigest = digestOfRaw(rec.graph.Raw) // the input was never resolved: a placeholder bound to the step text
		}
		if out, dec := src.Authorization(sctx, rec.graph); out == AuthorizationAllowed || out == AuthorizationDenied {
			st.Outcome.Authorization = out
			st.References.AuthorizationDecision = &DigestRefV2{Digest: dec}
		}
		if rec.decision != nil { // the cabinet's own decision (the profile gate) outranks the source's
			st.Outcome.Authorization = rec.decision.outcome
			dd := rec.decision.record.Digest()
			st.References.AuthorizationDecision = &DigestRefV2{Digest: dd}
			if arch, ok := src.(DecisionArchiver); ok {
				arch.ArchiveDecision(sctx, dd, rec.decision.record)
			}
		}
		var exe ExecutedIdentityV2
		hasExe := false
		if rec.module != nil && (rec.dispatched || !failed) {
			exe, hasExe = src.ExecutedIdentity(rec.module, rec.graph.FunctionName)
		}
		switch {
		case failed && !rec.dispatched:
			// stopped before the execution phase: REJECTED, no executed identity, no output, no host interaction
			st.Outcome.Execution = ExecutionRejected
			rj := rejectionOf(runErr, rec.module != nil)
			st.Rejection = &rj
			st.References.HostInteractions = HostInteractionsRefV2{Digest: EmptyHostInteractionsDigestV2(), Count: 0}
			if errors.Is(runErr, ErrUnauthorized) {
				st.Outcome.Authorization = AuthorizationDenied
				if st.References.AuthorizationDecision == nil {
					_, dec := src.Authorization(sctx, rec.graph)
					st.References.AuthorizationDecision = &DigestRefV2{Digest: dec}
				}
			}
		case failed:
			st.Outcome.Execution = ExecutionFailed
			if errors.Is(runErr, ErrTimeout) || ctx.Err() != nil {
				st.Outcome.Execution = ExecutionAborted
			}
		default:
			o := rec.outputHash
			st.OutputDigest = &o
		}
		if st.Outcome.Execution != ExecutionRejected {
			if !hasExe {
				return nil, fmt.Errorf("step %d (%s): the module has no authenticated executed identity; not recorded as executed", pos, rec.graph.Raw)
			}
			e := exe
			e.Dispatched = rec.graph.FunctionName
			st.Executed = &e
		}
		steps = append(steps, st)
		if st.Outcome.Execution != ExecutionSucceeded {
			execution = st.Outcome.Execution
		}
		if st.Outcome.Authorization == AuthorizationDenied || (st.Outcome.Authorization == AuthorizationAllowed && authorization != AuthorizationDenied) {
			authorization = st.Outcome.Authorization
		}
	}
	art := &ProofArtifactV2{
		Kind: ProofArtifactV2Kind, Version: ProofArtifactV2Version, ChainVersion: ChainVersionV2,
		Workflow: WorkflowRefV2{ID: workflowID, SourceDigest: sourceDigest},
		Steps:    steps, Outcome: OutcomeV2{Execution: execution, Authorization: authorization},
	}
	if err := SealArtifactV2(art); err != nil {
		return nil, err
	}
	return art, nil
}
