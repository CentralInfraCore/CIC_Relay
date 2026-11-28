package o1verify

import (
	"encoding/json"
	"strings"
)

// This file checks the CONTENT of an authorization decision (O-1 G-2), not only its digest. A digest proves the bytes are
// those bytes; it does not prove the decision is about this execution. For every step that references a decision the
// verifier requires the record, recomputes its digest from its own copy of the canonical text, and compares what the record
// says against the artifact: the module, the operation, the envelope, the profile, the concrete execution (workflow, step
// position, input digest), the decision against the step's outcome, and, through the execution-context record, the principal
// and the realization context. A reference whose record is absent is unresolved, never passed.
//
// The canonical texts are re-derived here from the contract (04-prooftrace-v2-wire.md); this package shares no code with the
// producer.

const (
	// none is the explicit value of a field for which there is no value.
	none = "none"
	// authzHeader heads the canonical text of an authorization record.
	authzHeader = "CICAUTHZv2"
	// ctxHeader heads the canonical text of an execution-context record.
	ctxHeader = "CICEXECCTXv2"
)

// authzFields is the fixed order of an authorization record's fields.
var authzFields = []string{"module_id", "envelope_digest", "profile_id", "profile_digest", "operation", "host_boundary", "effect", "effect_resolved",
	"principal", "realization_context_digest", "policy_digest", "decision", "reason", "workflow_id", "step_index", "input_digest"}

// ctxFields is the fixed order of an execution-context record's fields.
var ctxFields = []string{"workflow_id", "trust_mode", "principal", "realization_context_digest"}

// oneLine keeps a value on a single line, as the producer's canonical text does.
func oneLine(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

// fieldText renders a record value the way the canonical text does (numbers as their literal).
func fieldText(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	}
	return "", false
}

// recordDigest recomputes the digest of a record from its fields in the given order; ok=false if a field is missing or has an
// unusable type.
func recordDigest(header string, order []string, rec map[string]any) (string, map[string]string, bool) {
	vals := map[string]string{}
	var b strings.Builder
	b.WriteString(header + "\n")
	for _, k := range order {
		s, ok := fieldText(rec[k])
		if !ok {
			return "", nil, false
		}
		vals[k] = s
		b.WriteString(k + "=" + oneLine(s) + "\n")
	}
	return sha([]byte(b.String())), vals, true
}

// checkDecisions applies the content rules to every step that references an authorization decision.
func checkDecisions(art map[string]any, steps []any, opt Options, res *Result, fail func(string, ...any)) {
	if opt.LegacyDigestOnlyAuthorization {
		return
	}
	wfID, _ := walk(art, "workflow.id")
	workflowID, _ := wfID.(string)
	for pos, raw := range steps {
		st, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		dRef, has := walk(st, "references.authorization_decision.digest")
		ref, _ := dRef.(string)
		if !has || ref == "" {
			continue
		}
		rec, have := opt.AuthorizationDecisions[ref]
		if !have {
			res.UnresolvedReferences = append(res.UnresolvedReferences, "authorization_decision:"+ref)
			continue
		}
		got, v, ok := recordDigest(authzHeader, authzFields, rec)
		if !ok {
			fail("AUTHORIZATION_RECORD_INCOMPLETE: step %d: the record lacks a field or has one of the wrong type", pos)
			continue
		}
		if got != ref {
			fail("AUTHORIZATION_DECISION_DIGEST_MISMATCH: step %d: recomputed %s, referenced %s", pos, got, ref)
			continue
		}
		checkDecisionContent(pos, st, v, workflowID, opt, res, fail)
	}
}

// str reads a string at a dotted path of a step.
func str(st map[string]any, p string) string {
	v, _ := walk(st, p)
	s, _ := v.(string)
	return s
}

// checkDecisionContent compares one decision record with the step it belongs to.
func checkDecisionContent(pos int, st map[string]any, v map[string]string, workflowID string, opt Options, res *Result, fail func(string, ...any)) {
	authz, exec := str(st, "outcome.authorization"), str(st, "outcome.execution")
	allowed := v["decision"] == "ALLOWED"
	// the record's decision must be one of the two and must be the step's authorization outcome
	agrees := (v["decision"] == "ALLOWED" || v["decision"] == "DENIED") && v["decision"] == authz
	if !agrees {
		fail("AUTHORIZATION_DECISION_OUTCOME_MISMATCH: step %d: the record says %q, the step's authorization is %s", pos, v["decision"], authz)
	}
	if allowed && exec == "REJECTED" {
		fail("AUTHORIZATION_ALLOWED_BUT_REJECTED: step %d: an allowed decision for a step that was never executed", pos)
	}
	if v["decision"] == "DENIED" && exec != "REJECTED" {
		fail("AUTHORIZATION_DENIED_BUT_EXECUTED: step %d: a denied decision for a step that ran", pos)
	}
	bind := func(field, want, what string) {
		if v[field] == none && !allowed {
			return // a refusal before admission cannot know it
		}
		if v[field] != want {
			fail("AUTHORIZATION_BINDING_MISMATCH: step %d: the decision's %s is %q, the step's is %q", pos, what, v[field], want)
		}
	}
	bind("module_id", str(st, "requested.module_id"), "module")
	bind("operation", str(st, "requested.operation"), "operation")
	bind("input_digest", str(st, "input_digest"), "input digest")
	bind("workflow_id", workflowID, "workflow")
	if idx, ok := walk(st, "index"); ok {
		if n, isNum := idx.(json.Number); isNum {
			bind("step_index", n.String(), "step position")
		}
	}
	if exec != "REJECTED" {
		bind("envelope_digest", str(st, "executed.envelope_digest"), "module envelope")
		bind("profile_digest", str(st, "executed.profile.digest"), "profile digest")
		bind("profile_id", str(st, "executed.profile.id"), "profile id")
	}
	if allowed {
		for _, f := range []string{"effect", "policy_digest", "realization_context_digest"} {
			if v[f] == none || v[f] == "" {
				fail("AUTHORIZATION_UNBOUND_FIELD: step %d: an allowed decision must carry %s", pos, f)
			}
		}
		if v["principal"] == "unavailable" {
			fail("AUTHORIZATION_WITHOUT_PRINCIPAL: step %d: an allowed decision needs an authenticated principal", pos)
		}
	}
	checkContext(pos, st, v, workflowID, opt, res, fail)
}

// checkContext cross-checks the decision against the execution-context record the step references.
func checkContext(pos int, st map[string]any, v map[string]string, workflowID string, opt Options, res *Result, fail func(string, ...any)) {
	ref := str(st, "references.execution_context.digest")
	rec, have := opt.ExecutionContexts[ref]
	if !have {
		res.UnresolvedReferences = append(res.UnresolvedReferences, "execution_context:"+ref)
		return
	}
	got, c, ok := recordDigest(ctxHeader, ctxFields, rec)
	if !ok {
		fail("EXECUTION_CONTEXT_RECORD_INCOMPLETE: step %d", pos)
		return
	}
	if got != ref {
		fail("EXECUTION_CONTEXT_DIGEST_MISMATCH: step %d: recomputed %s, referenced %s", pos, got, ref)
		return
	}
	if c["workflow_id"] != workflowID {
		fail("EXECUTION_CONTEXT_MISMATCH: step %d: the context is for workflow %q, the artifact's is %q", pos, c["workflow_id"], workflowID)
	}
	if c["principal"] != v["principal"] {
		fail("AUTHORIZATION_PRINCIPAL_MISMATCH: step %d: the decision's principal is %q, the execution context's is %q", pos, v["principal"], c["principal"])
	}
	if c["realization_context_digest"] != v["realization_context_digest"] {
		fail("AUTHORIZATION_REALIZATION_MISMATCH: step %d: the decision's realization context is %q, the execution context's is %q", pos, v["realization_context_digest"], c["realization_context_digest"])
	}
}
