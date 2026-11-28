package o1verify

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures are a real artifact, its authorization record and its execution-context record, produced by the production
// source through the real cabinet (cmd/relay TestDumpO1Fixtures). Each case here changes one thing in a self-consistent way:
// the record is re-digested, the artifact's reference and its chain hash are redone, so the only thing wrong is the
// CONTENT the rule under test looks at.

type authzFixture struct {
	art       map[string]any
	decisions map[string]map[string]any
	contexts  map[string]map[string]any
}

func decodeFile(t *testing.T, name string, into any) {
	t.Helper()
	b, err := os.ReadFile("testdata/authz/" + name)
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	require.NoError(t, dec.Decode(into))
}

func loadAuthz(t *testing.T) *authzFixture {
	f := &authzFixture{}
	decodeFile(t, "artifact.json", &f.art)
	decodeFile(t, "decisions.json", &f.decisions)
	decodeFile(t, "contexts.json", &f.contexts)
	return f
}

func (f *authzFixture) step() map[string]any { return f.art["steps"].([]any)[0].(map[string]any) }

func (f *authzFixture) refs() map[string]any { return f.step()["references"].(map[string]any) }

func (f *authzFixture) decisionRef() string {
	return f.refs()["authorization_decision"].(map[string]any)["digest"].(string)
}

func (f *authzFixture) contextRef() string {
	return f.refs()["execution_context"].(map[string]any)["digest"].(string)
}

type variant struct {
	record   func(map[string]any) // changes the decision record, then re-digests it
	context  func(map[string]any) // changes the context record, then re-digests it
	artifact func(map[string]any) // changes the artifact (before the chain hash is redone)
	rawRec   func(map[string]any) // changes the record WITHOUT re-digesting it
	rawCtx   func(map[string]any) // changes the context record WITHOUT re-digesting it
	drop     string               // "decision" or "context": the record is not supplied
}

func clone(m map[string]any) map[string]any {
	c := map[string]any{}
	for k, v := range m {
		c[k] = v
	}
	return c
}

// build applies a variant and returns the artifact bytes and the options' maps.
func build(t *testing.T, v variant) ([]byte, map[string]map[string]any, map[string]map[string]any) {
	f := loadAuthz(t)
	dRef, cRef := f.decisionRef(), f.contextRef()
	rec, ctx := clone(f.decisions[dRef]), clone(f.contexts[cRef])
	if v.record != nil {
		v.record(rec)
	}
	if v.context != nil {
		v.context(ctx)
	}
	newD, newC := dRef, cRef
	if v.record != nil {
		d, _, ok := recordDigest(authzHeader, authzFields, rec)
		require.True(t, ok)
		newD = d
	}
	if v.context != nil {
		d, _, ok := recordDigest(ctxHeader, ctxFields, ctx)
		require.True(t, ok)
		newC = d
	}
	if v.rawRec != nil {
		v.rawRec(rec)
	}
	if v.rawCtx != nil {
		v.rawCtx(ctx)
	}
	f.refs()["authorization_decision"].(map[string]any)["digest"] = newD
	f.refs()["execution_context"].(map[string]any)["digest"] = newC
	if v.artifact != nil {
		v.artifact(f.art)
	}
	doc, err := json.Marshal(f.art)
	require.NoError(t, err)
	h, err := ChainHash(doc)
	require.NoError(t, err)
	f.art["chain_hash"] = h
	doc, err = json.Marshal(f.art)
	require.NoError(t, err)
	decisions, contexts := map[string]map[string]any{newD: rec}, map[string]map[string]any{newC: ctx}
	if v.drop == "decision" {
		decisions = nil
	}
	if v.drop == "context" {
		contexts = nil
	}
	return doc, decisions, contexts
}

func run(t *testing.T, v variant, legacy bool) Result {
	doc, decisions, contexts := build(t, v)
	empty, err := InteractionsDigest(nil)
	require.NoError(t, err)
	return Verify(doc, Options{Verifier: VerifierInfo{ID: "t", Version: "1", ImplementationDigest: "sha256:" + strings.Repeat("a", 64)},
		HostInteractions: map[string][]map[string]any{empty: {}}, AuthorizationDecisions: decisions, ExecutionContexts: contexts, LegacyDigestOnlyAuthorization: legacy})
}

func joined(r Result) string { return strings.Join(r.Problems, " | ") }

func TestAuthorizationContentPositive(t *testing.T) {
	res := run(t, variant{}, false)
	assert.Empty(t, res.Problems)
	assert.Empty(t, res.UnresolvedReferences)
	assert.Equal(t, VerdictVerified, res.EvidenceVerdict)
	// the same artifact with only the digests re-derived is still fine: the rules are about content, not about being the original
	res = run(t, variant{record: func(r map[string]any) { r["reason"] = "effect_allowed_again" }}, false)
	assert.Empty(t, res.Problems, "a reason is documentation, not a binding")
}

func TestARecordThatIsMissingOrBrokenIsNotPassed(t *testing.T) {
	res := run(t, variant{drop: "decision"}, false)
	assert.Contains(t, strings.Join(res.UnresolvedReferences, ","), "authorization_decision:")
	assert.Equal(t, VerificationIncomplete, res.Dimensions.Verification)
	assert.Equal(t, VerdictNotVerified, res.EvidenceVerdict)

	res = run(t, variant{drop: "context"}, false)
	assert.Contains(t, strings.Join(res.UnresolvedReferences, ","), "execution_context:")
	assert.Equal(t, VerdictNotVerified, res.EvidenceVerdict)

	assert.Contains(t, joined(run(t, variant{rawRec: func(r map[string]any) { r["effect"] = "external-write" }}, false)), "AUTHORIZATION_DECISION_DIGEST_MISMATCH")
	assert.Contains(t, joined(run(t, variant{rawCtx: func(r map[string]any) { r["principal"] = "x" }}, false)), "EXECUTION_CONTEXT_DIGEST_MISMATCH")
	assert.Contains(t, joined(run(t, variant{rawRec: func(r map[string]any) { delete(r, "policy_digest") }}, false)), "AUTHORIZATION_RECORD_INCOMPLETE")
	assert.Contains(t, joined(run(t, variant{rawRec: func(r map[string]any) { r["effect"] = true }}, false)), "AUTHORIZATION_RECORD_INCOMPLETE", "a field of the wrong type")
	assert.Contains(t, joined(run(t, variant{rawCtx: func(r map[string]any) { delete(r, "trust_mode") }}, false)), "EXECUTION_CONTEXT_RECORD_INCOMPLETE")
}

func TestEveryBindingOfTheDecisionIsChecked(t *testing.T) {
	for field, val := range map[string]string{
		"module_id": "other.mod@1.0", "operation": "observe", "input_digest": "sha256:" + strings.Repeat("0", 64), "workflow_id": "other@1.0",
		"envelope_digest": "sha256:" + strings.Repeat("0", 64), "profile_digest": "sha256:" + strings.Repeat("0", 64), "profile_id": "other:profile@1",
	} {
		field, val := field, val
		t.Run(field, func(t *testing.T) {
			res := run(t, variant{record: func(r map[string]any) { r[field] = val }}, false)
			assert.Contains(t, joined(res), "AUTHORIZATION_BINDING_MISMATCH", field)
			assert.Equal(t, VerdictNotVerified, res.EvidenceVerdict)
		})
	}
	res := run(t, variant{record: func(r map[string]any) { r["step_index"] = json.Number("3") }}, false)
	assert.Contains(t, joined(res), "AUTHORIZATION_BINDING_MISMATCH", "the step position")
}

func TestTheDecisionMustAgreeWithTheStepsOutcome(t *testing.T) {
	assert.Contains(t, joined(run(t, variant{record: func(r map[string]any) { r["decision"] = "DENIED" }}, false)), "AUTHORIZATION_DECISION_OUTCOME_MISMATCH")
	assert.Contains(t, joined(run(t, variant{record: func(r map[string]any) { r["decision"] = "maybe" }}, false)), "AUTHORIZATION_DECISION_OUTCOME_MISMATCH")
	assert.Contains(t, joined(run(t, variant{record: func(r map[string]any) { r["decision"] = "allowed" }}, false)), "AUTHORIZATION_DECISION_OUTCOME_MISMATCH", "the vocabulary is exact")

	// a denied decision, a DENIED step, yet the step ran
	denied := variant{record: func(r map[string]any) { r["decision"] = "DENIED" }, artifact: func(a map[string]any) {
		a["steps"].([]any)[0].(map[string]any)["outcome"].(map[string]any)["authorization"] = "DENIED"
	}}
	assert.Contains(t, joined(run(t, denied, false)), "AUTHORIZATION_DENIED_BUT_EXECUTED")

	// an allowed decision for a step that was rejected
	rejected := variant{artifact: func(a map[string]any) {
		st := a["steps"].([]any)[0].(map[string]any)
		st["outcome"].(map[string]any)["execution"] = "REJECTED"
	}}
	assert.Contains(t, joined(run(t, rejected, false)), "AUTHORIZATION_ALLOWED_BUT_REJECTED")
}

func TestAnAllowedDecisionMustBeFullyBound(t *testing.T) {
	for _, f := range []string{"effect", "policy_digest", "realization_context_digest"} {
		f := f
		res := run(t, variant{record: func(r map[string]any) { r[f] = "none" }, context: func(c map[string]any) { c["realization_context_digest"] = "none" }}, false)
		assert.Contains(t, joined(res), "AUTHORIZATION_UNBOUND_FIELD", f)
	}
	res := run(t, variant{record: func(r map[string]any) { r["effect"] = "" }}, false)
	assert.Contains(t, joined(res), "AUTHORIZATION_UNBOUND_FIELD", "an empty value is as unbound as none")

	res = run(t, variant{record: func(r map[string]any) { r["principal"] = "unavailable" }, context: func(c map[string]any) { c["principal"] = "unavailable" }}, false)
	assert.Contains(t, joined(res), "AUTHORIZATION_WITHOUT_PRINCIPAL")
	assert.NotContains(t, joined(res), "AUTHORIZATION_PRINCIPAL_MISMATCH", "the two agree; what is wrong is that there is no principal")
}

func TestTheDecisionIsCrossCheckedAgainstTheExecutionContext(t *testing.T) {
	assert.Contains(t, joined(run(t, variant{context: func(c map[string]any) { c["principal"] = "service:impostor/bearer" }}, false)), "AUTHORIZATION_PRINCIPAL_MISMATCH")
	assert.Contains(t, joined(run(t, variant{context: func(c map[string]any) { c["realization_context_digest"] = "sha256:" + strings.Repeat("5", 64) }}, false)), "AUTHORIZATION_REALIZATION_MISMATCH")
	assert.Contains(t, joined(run(t, variant{context: func(c map[string]any) { c["workflow_id"] = "other@1.0" }}, false)), "EXECUTION_CONTEXT_MISMATCH")
	assert.Empty(t, run(t, variant{context: func(c map[string]any) { c["trust_mode"] = "dev" }}, false).Problems, "the trust mode is context, not a binding of the decision")
}

// a refusal before admission cannot know the envelope, the profile or the input: those fields may say none on a DENIED step,
// and only there
func TestADeniedStepMayLackWhatAnUnadmittedModuleCannotKnow(t *testing.T) {
	refused := variant{
		record: func(r map[string]any) {
			r["decision"] = "DENIED"
			for _, f := range []string{"envelope_digest", "profile_id", "profile_digest", "input_digest", "workflow_id", "effect", "policy_digest"} {
				r[f] = "none"
			}
		},
		artifact: func(a map[string]any) {
			st := a["steps"].([]any)[0].(map[string]any)
			o := st["outcome"].(map[string]any)
			o["execution"], o["authorization"] = "REJECTED", "DENIED"
			delete(st, "executed")
			delete(st, "output_digest")
			st["rejection"] = map[string]any{"phase": "policy", "code": "PROTECTED_OPERATION"}
		},
	}
	res := run(t, refused, false)
	assert.NotContains(t, joined(res), "AUTHORIZATION_BINDING_MISMATCH", "none on a denied, rejected step is not a mismatch")
	assert.NotContains(t, joined(res), "AUTHORIZATION_UNBOUND_FIELD")
	assert.NotContains(t, joined(res), "AUTHORIZATION_DECISION_OUTCOME_MISMATCH", "a DENIED record for a DENIED step agrees")
	assert.NotContains(t, joined(res), "AUTHORIZATION_DENIED_BUT_EXECUTED", "a DENIED step that was rejected did not run")
}

func TestTheLegacyOptionSkipsTheContentChecksAndNothingElse(t *testing.T) {
	bad := variant{record: func(r map[string]any) { r["operation"] = "observe" }}
	assert.Contains(t, joined(run(t, bad, false)), "AUTHORIZATION_BINDING_MISMATCH")
	assert.NotContains(t, joined(run(t, bad, true)), "AUTHORIZATION_BINDING_MISMATCH")
	res := run(t, variant{drop: "decision"}, true)
	assert.Empty(t, res.UnresolvedReferences, "legacy mode believes the digest")
}

func TestFieldTextAndRecordDigestHandleOddInput(t *testing.T) {
	s, ok := fieldText(json.Number("7"))
	assert.True(t, ok)
	assert.Equal(t, "7", s)
	_, ok = fieldText(nil)
	assert.False(t, ok)
	_, ok = fieldText([]any{})
	assert.False(t, ok)
	_, _, ok = recordDigest(authzHeader, authzFields, map[string]any{})
	assert.False(t, ok)
	assert.Equal(t, "a b", oneLine("a\nb"))
	assert.Equal(t, "a b", oneLine("a\rb"))
}
