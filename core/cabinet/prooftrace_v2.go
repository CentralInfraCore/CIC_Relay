package cabinet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ProofTrace V2 (`CICCHAINv2`) — the producer side of the O-1 wire contract.
//
// V1 (ProofTrace, ComputeChainHashV1, Checksum) is untouched and stays valid on
// its own terms: there is no conversion between the two, the hash domains are
// separate ("CICCHAINv1" / "CICCHAINv2"), and a V1 artifact cannot satisfy a
// V2-class claim (operation identity, module artifact, manifest, trust state,
// host interactions). See ComputeChainHashV1 for V1.
//
// What is in the hash domain and what is not:
//   - in:  kind, version, chain_version, workflow{id,source_digest}, steps[],
//     outcome{execution,authorization}
//   - out: the chain_hash field itself, and the whole persistence object
//     (PersistenceStatusV2) — an artifact cannot assert about itself that it was
//     durably recorded.
const (
	// ProofArtifactV2Kind is the only accepted `kind` of a V2 artifact.
	ProofArtifactV2Kind = "ProofArtifact"
	// ProofArtifactV2Version is the only accepted `version` of a V2 artifact.
	ProofArtifactV2Version = "2.0"
	// ChainVersionV2 is the V2 hash domain and the only accepted `chain_version`.
	ChainVersionV2 = "CICCHAINv2"
	// ChainVersionV1 is the V1 hash domain. It is rejected in a V2 artifact.
	ChainVersionV1 = "CICCHAINv1"
)

// Execution outcome values (`outcome.execution`): the first result dimension,
// asserted by the Relay.
const (
	// ExecutionSucceeded is a named value of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	ExecutionSucceeded = "SUCCEEDED"
	// ExecutionRejected is a named value of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	ExecutionRejected = "REJECTED"
	// ExecutionFailed is a named value of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	ExecutionFailed = "FAILED"
	// ExecutionAborted is a named value of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	ExecutionAborted = "ABORTED"
)

// Authorization outcome values (`outcome.authorization`): the second result
// dimension, asserted by the Relay.
const (
	// AuthorizationAllowed is a named value of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	AuthorizationAllowed = "ALLOWED"
	// AuthorizationDenied is a named value of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	AuthorizationDenied = "DENIED"
	// AuthorizationNotEvaluated is a named value of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	AuthorizationNotEvaluated = "NOT_EVALUATED"
)

// Rejection phases (`rejection.phase`): where the operation stopped.
const (
	// RejectionPhaseAdmission is a rejection phase of a REJECTED step (V2-01).
	RejectionPhaseAdmission = "admission"
	// RejectionPhaseDispatch is a rejection phase of a REJECTED step (V2-01).
	RejectionPhaseDispatch = "dispatch"
	// RejectionPhasePolicy is a rejection phase of a REJECTED step (V2-01).
	RejectionPhasePolicy = "policy"
)

// Trust states of an executed module artifact (`executed.trust_state`).
const (
	// TrustStateAuthenticated is a trust state of an executed module artifact (D-1).
	TrustStateAuthenticated = "authenticated"
	// TrustStateTransitional is a trust state of an executed module artifact (D-1).
	TrustStateTransitional = "transitional"
	// TrustStateDev is a trust state of an executed module artifact (D-1).
	TrustStateDev = "dev"
)

// Validation and canonicalisation error codes. A rejection is only evidence if
// it happens for the named reason, so every refusal below carries its code.
const (
	// CodeBadValue: a value in the hash input contains a line break or a
	// non-ASCII byte. The canonical form has no escaping, so such a value cannot
	// be represented and is refused instead of being mangled.
	CodeBadValue = "BAD_VALUE"
	// CodeKindInvalid: `kind` is not ProofArtifact.
	CodeKindInvalid = "V2_KIND_INVALID"
	// CodeVersionInvalid: `version` is not "2.0" (a V1 record in the V2 domain).
	CodeVersionInvalid = "V2_VERSION_INVALID"
	// CodeChainVersionInvalid: `chain_version` is not CICCHAINv2.
	CodeChainVersionInvalid = "V2_CHAIN_VERSION_INVALID"
	// CodeWorkflowIDRequired: `workflow.id` is empty.
	CodeWorkflowIDRequired = "V2_WORKFLOW_ID_REQUIRED"
	// CodeWorkflowSourceDigestRequired: `workflow.source_digest` is empty.
	CodeWorkflowSourceDigestRequired = "V2_WORKFLOW_SOURCE_DIGEST_REQUIRED"
	// CodeExecutionInvalid: an execution outcome outside the enum.
	CodeExecutionInvalid = "V2_EXECUTION_INVALID"
	// CodeAuthorizationInvalid: an authorization outcome outside the enum.
	CodeAuthorizationInvalid = "V2_AUTHORIZATION_INVALID"
	// CodeStepIndexMismatch: `step.index` is not the step's position.
	CodeStepIndexMismatch = "V2_STEP_INDEX_MISMATCH"
	// CodeStepNameRequired: `step.name` is empty.
	CodeStepNameRequired = "V2_STEP_NAME_REQUIRED"
	// CodeRequestedIdentityRequired: V2-01 — the requested identity is missing.
	CodeRequestedIdentityRequired = "V2_REQUESTED_IDENTITY_REQUIRED"
	// CodeRejectedWithExecuted: V2-01 — a REJECTED step claims an executed identity.
	CodeRejectedWithExecuted = "V2_REJECTED_WITH_EXECUTED"
	// CodeRejectedWithoutRejection: V2-01 — a REJECTED step has no rejection record.
	CodeRejectedWithoutRejection = "V2_REJECTED_WITHOUT_REJECTION"
	// CodeRejectedWithOutput: a REJECTED step claims an output digest.
	CodeRejectedWithOutput = "V2_REJECTED_WITH_OUTPUT"
	// CodeRejectedWithHostInteractions: a REJECTED step claims host interactions.
	CodeRejectedWithHostInteractions = "V2_REJECTED_WITH_HOST_INTERACTIONS"
	// CodeExecutedRequired: V2-01 — SUCCEEDED/FAILED/ABORTED without an executed identity.
	CodeExecutedRequired = "V2_EXECUTED_REQUIRED"
	// CodeExecutedWithRejection: a non-REJECTED step carries a rejection record.
	CodeExecutedWithRejection = "V2_EXECUTED_WITH_REJECTION"
	// CodeAuthenticatedWithoutEnvelopeDigest: AC-V2-06 — trust_state
	// "authenticated" without the authenticated envelope identity.
	CodeAuthenticatedWithoutEnvelopeDigest = "V2_AUTHENTICATED_WITHOUT_ENVELOPE_DIGEST"
	// CodeAuthorizationDecisionRequired: AC-V2-11 — an ALLOWED/DENIED step with no
	// reference to the decision record of the deciding mechanism.
	CodeAuthorizationDecisionRequired = "V2_AUTHORIZATION_DECISION_REFERENCE_REQUIRED"
	// CodeDigestRequired: a mandatory digest field is empty.
	CodeDigestRequired = "V2_DIGEST_REQUIRED"
	// CodeDigestFormatInvalid: a digest is not `sha256:` + 64 lowercase hex chars.
	CodeDigestFormatInvalid = "V2_DIGEST_FORMAT_INVALID"
	// CodeRejectionPhaseInvalid: `rejection.phase` outside the enum.
	CodeRejectionPhaseInvalid = "V2_REJECTION_PHASE_INVALID"
	// CodeRejectionCodeInvalid: `rejection.code` is not /^[A-Z_]+$/.
	CodeRejectionCodeInvalid = "V2_REJECTION_CODE_INVALID"
	// CodeTrustStateInvalid: `executed.trust_state` outside the enum.
	CodeTrustStateInvalid = "V2_TRUST_STATE_INVALID"
	// CodeStepsRequired: an artifact with no steps.
	CodeStepsRequired = "V2_STEPS_REQUIRED"
)

// ArtifactV2Error is a refusal with a machine-readable code: a negative test can
// assert the reason, not just the fact of the refusal.
type ArtifactV2Error struct {
	Code   string // one of the Code* constants
	Field  string // the field the refusal is about, in wire notation
	Detail string
}

// Error renders the code, the field and the detail.
func (e *ArtifactV2Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Field)
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Field, e.Detail)
}

// ArtifactV2ErrorCode returns the code of err if it is an ArtifactV2Error, and
// "" otherwise.
func ArtifactV2ErrorCode(err error) string {
	var e *ArtifactV2Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// WorkflowRefV2 anchors the artifact to the workflow and the source revision.
type WorkflowRefV2 struct {
	ID           string `json:"id"`
	SourceDigest string `json:"source_digest"`
}

// OutcomeV2 carries the two Relay-asserted result dimensions. The other two
// (evidence, verification) live outside the artifact, by design.
type OutcomeV2 struct {
	Execution     string `json:"execution"`
	Authorization string `json:"authorization"`
}

// RequestedIdentityV2 is the REQUESTED identity (V2-01): what the caller asked
// to be executed. It exists for every step, including one rejected before any
// load or dispatch.
type RequestedIdentityV2 struct {
	ModuleID  string `json:"module_id"`
	Operation string `json:"operation"`
	// PresentedArtifactDigest is the artifact digest the caller presented, if any.
	// Empty means absent; it enters the hash input as the `null` literal.
	PresentedArtifactDigest string `json:"presented_artifact_digest,omitempty"`
}

// ProfileRefV2 is the ABI profile that governed the step: the content digest
// (ABI-05: sha256 of the published raw bytes), not just the name.
type ProfileRefV2 struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// ExecutedIdentityV2 is the EXECUTED identity (V2-01): what actually ran. It is
// asserted only when the execution phase was reached, so a REJECTED step must
// not carry it — it cannot claim a non-existent execution identity.
type ExecutedIdentityV2 struct {
	// ArtifactDigest is the sha256 of the raw WASM bytes (D-10).
	ArtifactDigest string `json:"artifact_digest"`
	// ExecutedDigest is the sha256 of the actually instantiated (canonical) bytes.
	ExecutedDigest string `json:"executed_digest"`
	// ManifestDigest is the sha256 of the raw bytes of the full release manifest (ENV-01).
	ManifestDigest string `json:"manifest_digest"`
	// EnvelopeDigest is the authenticated envelope identity (ENV-03). nil is only
	// allowed in the transitional/dev trust states; with "authenticated" it is
	// mandatory (AC-V2-06).
	EnvelopeDigest *string      `json:"envelope_digest"`
	Profile        ProfileRefV2 `json:"profile"`
	// Dispatched is the operation string that was actually dispatched.
	Dispatched string `json:"dispatched"`
	TrustState string `json:"trust_state"`
}

// RejectionV2 records where and why the operation stopped. Mandatory on a
// REJECTED step and forbidden on any other (V2-01).
type RejectionV2 struct {
	Phase string `json:"phase"`
	Code  string `json:"code"`
}

// DigestRefV2 is a content-addressed reference to an evidence record that lives
// outside the artifact. A reference is complete evidence only if the content is
// reachable and checkable; otherwise the verifier reports UNRESOLVED_REFERENCE.
type DigestRefV2 struct {
	Digest string `json:"digest"`
}

// HostInteractionsRefV2 references the ordered host-interaction list: its digest
// and its length. The list also contains the denied and the failed calls.
type HostInteractionsRefV2 struct {
	Digest string `json:"digest"`
	Count  int    `json:"count"`
}

// StepReferencesV2 holds the step's references to records outside the hash
// domain. AuthorizationDecision is mandatory for an ALLOWED or DENIED step
// (AC-V2-11).
type StepReferencesV2 struct {
	ExecutionContext      DigestRefV2           `json:"execution_context"`
	AuthorizationDecision *DigestRefV2          `json:"authorization_decision,omitempty"`
	HostInteractions      HostInteractionsRefV2 `json:"host_interactions"`
}

// ProofStepV2 is one step of a V2 artifact.
type ProofStepV2 struct {
	Index       int                 `json:"index"`
	Name        string              `json:"name"` // the raw step text; enters the hash only as a sha256
	Requested   RequestedIdentityV2 `json:"requested"`
	Outcome     OutcomeV2           `json:"outcome"`
	Rejection   *RejectionV2        `json:"rejection,omitempty"`
	Executed    *ExecutedIdentityV2 `json:"executed,omitempty"`
	InputDigest string              `json:"input_digest"`
	// OutputDigest is nil (JSON null) for a rejected or failed step.
	OutputDigest *string          `json:"output_digest"`
	References   StepReferencesV2 `json:"references"`
}

// ProofArtifactV2 is the V2 proof artifact: the whole hash domain of
// CICCHAINv2. The persistence object is deliberately not a field here.
type ProofArtifactV2 struct {
	Kind         string        `json:"kind"`
	Version      string        `json:"version"`
	ChainVersion string        `json:"chain_version"`
	Workflow     WorkflowRefV2 `json:"workflow"`
	Steps        []ProofStepV2 `json:"steps"`
	Outcome      OutcomeV2     `json:"outcome"`
	ChainHash    string        `json:"chain_hash"`
}

// digestPattern is a package-level lookup table or pattern of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// rejectionCodePattern mirrors the wire schema: an upper-case, underscore code.
var rejectionCodePattern = regexp.MustCompile(`^[A-Z_]+$`)

// canonValue renders one value of the hash input. An empty string means the
// field is absent and enters as the `null` literal; a value with a line break or
// a non-ASCII byte is refused (CodeBadValue) rather than escaped, because the
// canonical form has no escaping.
func canonValue(field, v string) (string, error) {
	if v == "" {
		return "null", nil
	}
	if strings.ContainsAny(v, "\n\r") {
		return "", &ArtifactV2Error{Code: CodeBadValue, Field: field, Detail: "value contains a line break"}
	}
	for i := 0; i < len(v); i++ {
		if v[i] > 0x7f {
			return "", &ArtifactV2Error{Code: CodeBadValue, Field: field, Detail: "value is not ASCII"}
		}
	}
	return v, nil
}

// canonOptional renders a nullable string field.
func canonOptional(field string, v *string) (string, error) {
	if v == nil {
		return "null", nil
	}
	return canonValue(field, *v)
}

// ChainBytesV2 builds the normative CICCHAINv2 hash input (V2-03): fixed order,
// LF-terminated `key=value` lines, with no JSON re-serialisation. Free text (the
// step name) enters only as the sha256 of its UTF-8 bytes; an absent value is
// the `null` literal; absent groups are explicit (`rejection=absent`,
// `executed=absent`); the chain_hash field is not part of the input.
func ChainBytesV2(a *ProofArtifactV2) ([]byte, error) {
	if a == nil {
		return nil, &ArtifactV2Error{Code: CodeKindInvalid, Field: "artifact", Detail: "artifact is nil"}
	}
	lines := []string{ChainVersionV2}
	add := func(key, field, value string) error {
		v, err := canonValue(field, value)
		if err != nil {
			return err
		}
		lines = append(lines, key+"="+v)
		return nil
	}
	addOpt := func(key, field string, value *string) error {
		v, err := canonOptional(field, value)
		if err != nil {
			return err
		}
		lines = append(lines, key+"="+v)
		return nil
	}
	for _, f := range []struct{ key, field, value string }{
		{"version", "version", a.Version},
		{"workflow.id", "workflow.id", a.Workflow.ID},
		{"workflow.source_digest", "workflow.source_digest", a.Workflow.SourceDigest},
		{"outcome.execution", "outcome.execution", a.Outcome.Execution},
		{"outcome.authorization", "outcome.authorization", a.Outcome.Authorization},
	} {
		if err := add(f.key, f.field, f.value); err != nil {
			return nil, err
		}
	}
	lines = append(lines, "steps="+strconv.Itoa(len(a.Steps)))

	for si := range a.Steps {
		s := &a.Steps[si]
		p := "step." + strconv.Itoa(s.Index)
		nameSum := sha256.Sum256([]byte(s.Name))
		lines = append(lines, p+".name_sha256=sha256:"+hex.EncodeToString(nameSum[:]))
		lines = append(lines, p+".index="+strconv.Itoa(s.Index))

		var authzDecision *string
		if s.References.AuthorizationDecision != nil {
			d := s.References.AuthorizationDecision.Digest
			authzDecision = &d
		}
		for _, f := range []struct{ key, value string }{
			{".requested.module_id", s.Requested.ModuleID},
			{".requested.operation", s.Requested.Operation},
			{".requested.presented_artifact_digest", s.Requested.PresentedArtifactDigest},
			{".outcome.execution", s.Outcome.Execution},
			{".outcome.authorization", s.Outcome.Authorization},
			{".input_digest", s.InputDigest},
		} {
			if err := add(p+f.key, p+f.key, f.value); err != nil {
				return nil, err
			}
		}
		if err := addOpt(p+".output_digest", p+".output_digest", s.OutputDigest); err != nil {
			return nil, err
		}
		if err := add(p+".references.execution_context.digest", p+".references.execution_context.digest", s.References.ExecutionContext.Digest); err != nil {
			return nil, err
		}
		if err := addOpt(p+".references.authorization_decision.digest", p+".references.authorization_decision.digest", authzDecision); err != nil {
			return nil, err
		}
		if err := add(p+".references.host_interactions.digest", p+".references.host_interactions.digest", s.References.HostInteractions.Digest); err != nil {
			return nil, err
		}
		lines = append(lines, p+".references.host_interactions.count="+strconv.Itoa(s.References.HostInteractions.Count))

		if s.Rejection == nil {
			lines = append(lines, p+".rejection=absent")
		} else {
			lines = append(lines, p+".rejection=present")
			if err := add(p+".rejection.phase", p+".rejection.phase", s.Rejection.Phase); err != nil {
				return nil, err
			}
			if err := add(p+".rejection.code", p+".rejection.code", s.Rejection.Code); err != nil {
				return nil, err
			}
		}
		if s.Executed == nil {
			lines = append(lines, p+".executed=absent")
		} else {
			lines = append(lines, p+".executed=present")
			e := s.Executed
			for _, f := range []struct{ key, value string }{
				{".executed.artifact_digest", e.ArtifactDigest},
				{".executed.executed_digest", e.ExecutedDigest},
				{".executed.manifest_digest", e.ManifestDigest},
			} {
				if err := add(p+f.key, p+f.key, f.value); err != nil {
					return nil, err
				}
			}
			if err := addOpt(p+".executed.envelope_digest", p+".executed.envelope_digest", e.EnvelopeDigest); err != nil {
				return nil, err
			}
			for _, f := range []struct{ key, value string }{
				{".executed.profile.id", e.Profile.ID},
				{".executed.profile.digest", e.Profile.Digest},
				{".executed.dispatched", e.Dispatched},
				{".executed.trust_state", e.TrustState},
			} {
				if err := add(p+f.key, p+f.key, f.value); err != nil {
					return nil, err
				}
			}
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// ComputeChainHashV2 returns "sha256:" + hex(sha256(ChainBytesV2(a))). It is the
// V2 chain hash: a different hash domain from ComputeChainHashV1, over a
// different field set, with no conversion between the two.
func ComputeChainHashV2(a *ProofArtifactV2) (string, error) {
	b, err := ChainBytesV2(a)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// SealArtifactV2 validates the artifact and writes its computed chain hash. A
// refused artifact gets no chain hash: the Relay does not emit a V2 artifact
// that breaks the V2-01 identity rules.
func SealArtifactV2(a *ProofArtifactV2) error {
	if err := ValidateArtifactV2(a); err != nil {
		return err
	}
	h, err := ComputeChainHashV2(a)
	if err != nil {
		return err
	}
	a.ChainHash = h
	return nil
}

// ProfileRawDigestV2 returns the ABI profile content digest (ABI-05): the sha256 of
// the profile artifact's published raw bytes, with no re-serialisation and no
// canonicalisation. A modified profile therefore gives a different digest
// (AC-V2-01).
func ProfileRawDigestV2(rawProfileBytes []byte) string {
	sum := sha256.Sum256(rawProfileBytes)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// requireDigest checks a mandatory digest field.
func requireDigest(field, v string) error {
	if v == "" {
		return &ArtifactV2Error{Code: CodeDigestRequired, Field: field}
	}
	if !digestPattern.MatchString(v) {
		return &ArtifactV2Error{Code: CodeDigestFormatInvalid, Field: field, Detail: v}
	}
	return nil
}

// optionalDigest checks an optional digest field when it is present.
func optionalDigest(field, v string) error {
	if v == "" {
		return nil
	}
	if !digestPattern.MatchString(v) {
		return &ArtifactV2Error{Code: CodeDigestFormatInvalid, Field: field, Detail: v}
	}
	return nil
}

// ValidateArtifactV2 enforces the V2 invariants the producer must never break.
// It is the Relay-side counterpart of the wire schema, plus the normative
// V2-01 rules: the requested identity always exists; a REJECTED step has a
// rejection record and no executed identity, output or host interaction; a
// SUCCEEDED/FAILED/ABORTED step has an executed identity and no rejection;
// "authenticated" requires the envelope digest (AC-V2-06); an ALLOWED/DENIED
// step requires the authorization-decision reference (AC-V2-11); and a V1
// record is refused in the V2 domain (AC-V2-07).
func ValidateArtifactV2(a *ProofArtifactV2) error {
	if a == nil {
		return &ArtifactV2Error{Code: CodeKindInvalid, Field: "artifact", Detail: "artifact is nil"}
	}
	if a.Kind != ProofArtifactV2Kind {
		return &ArtifactV2Error{Code: CodeKindInvalid, Field: "kind", Detail: a.Kind}
	}
	if a.Version != ProofArtifactV2Version {
		return &ArtifactV2Error{Code: CodeVersionInvalid, Field: "version", Detail: a.Version}
	}
	if a.ChainVersion != ChainVersionV2 {
		return &ArtifactV2Error{Code: CodeChainVersionInvalid, Field: "chain_version", Detail: a.ChainVersion}
	}
	if a.Workflow.ID == "" {
		return &ArtifactV2Error{Code: CodeWorkflowIDRequired, Field: "workflow.id"}
	}
	if a.Workflow.SourceDigest == "" {
		return &ArtifactV2Error{Code: CodeWorkflowSourceDigestRequired, Field: "workflow.source_digest"}
	}
	if err := validExecution("outcome.execution", a.Outcome.Execution); err != nil {
		return err
	}
	if err := validAuthorization("outcome.authorization", a.Outcome.Authorization); err != nil {
		return err
	}
	if len(a.Steps) == 0 {
		return &ArtifactV2Error{Code: CodeStepsRequired, Field: "steps"}
	}
	for i := range a.Steps {
		if err := validateStepV2(i, &a.Steps[i]); err != nil {
			return err
		}
	}
	return nil
}

// validExecution checks the execution enum.
func validExecution(field, v string) error {
	switch v {
	case ExecutionSucceeded, ExecutionRejected, ExecutionFailed, ExecutionAborted:
		return nil
	}
	return &ArtifactV2Error{Code: CodeExecutionInvalid, Field: field, Detail: v}
}

// validAuthorization checks the authorization enum.
func validAuthorization(field, v string) error {
	switch v {
	case AuthorizationAllowed, AuthorizationDenied, AuthorizationNotEvaluated:
		return nil
	}
	return &ArtifactV2Error{Code: CodeAuthorizationInvalid, Field: field, Detail: v}
}

// validateStepV2 enforces the per-step V2 rules.
func validateStepV2(pos int, s *ProofStepV2) error {
	p := fmt.Sprintf("steps[%d]", pos)
	if s.Index != pos {
		return &ArtifactV2Error{Code: CodeStepIndexMismatch, Field: p + ".index", Detail: strconv.Itoa(s.Index)}
	}
	if s.Name == "" {
		return &ArtifactV2Error{Code: CodeStepNameRequired, Field: p + ".name"}
	}
	if s.Requested.ModuleID == "" || s.Requested.Operation == "" {
		return &ArtifactV2Error{Code: CodeRequestedIdentityRequired, Field: p + ".requested"}
	}
	if err := optionalDigest(p+".requested.presented_artifact_digest", s.Requested.PresentedArtifactDigest); err != nil {
		return err
	}
	if err := validExecution(p+".outcome.execution", s.Outcome.Execution); err != nil {
		return err
	}
	if err := validAuthorization(p+".outcome.authorization", s.Outcome.Authorization); err != nil {
		return err
	}
	if err := requireDigest(p+".input_digest", s.InputDigest); err != nil {
		return err
	}
	if s.OutputDigest != nil {
		if err := requireDigest(p+".output_digest", *s.OutputDigest); err != nil {
			return err
		}
	}
	if err := requireDigest(p+".references.execution_context.digest", s.References.ExecutionContext.Digest); err != nil {
		return err
	}
	if err := requireDigest(p+".references.host_interactions.digest", s.References.HostInteractions.Digest); err != nil {
		return err
	}
	if s.References.HostInteractions.Count < 0 {
		return &ArtifactV2Error{Code: CodeRejectedWithHostInteractions, Field: p + ".references.host_interactions.count"}
	}
	if s.References.AuthorizationDecision != nil {
		if err := requireDigest(p+".references.authorization_decision.digest", s.References.AuthorizationDecision.Digest); err != nil {
			return err
		}
	}

	// AC-V2-11: an ALLOWED or DENIED step must name the decision record.
	switch s.Outcome.Authorization {
	case AuthorizationAllowed, AuthorizationDenied:
		if s.References.AuthorizationDecision == nil {
			return &ArtifactV2Error{Code: CodeAuthorizationDecisionRequired, Field: p + ".references.authorization_decision", Detail: s.Outcome.Authorization}
		}
	}

	// V2-01: requested/executed identity and the rejection record.
	if s.Outcome.Execution == ExecutionRejected {
		if s.Executed != nil {
			return &ArtifactV2Error{Code: CodeRejectedWithExecuted, Field: p + ".executed"}
		}
		if s.Rejection == nil {
			return &ArtifactV2Error{Code: CodeRejectedWithoutRejection, Field: p + ".rejection"}
		}
		if s.OutputDigest != nil {
			return &ArtifactV2Error{Code: CodeRejectedWithOutput, Field: p + ".output_digest"}
		}
		if s.References.HostInteractions.Count != 0 {
			return &ArtifactV2Error{Code: CodeRejectedWithHostInteractions, Field: p + ".references.host_interactions.count", Detail: strconv.Itoa(s.References.HostInteractions.Count)}
		}
	} else {
		if s.Rejection != nil {
			return &ArtifactV2Error{Code: CodeExecutedWithRejection, Field: p + ".rejection"}
		}
		if s.Executed == nil {
			return &ArtifactV2Error{Code: CodeExecutedRequired, Field: p + ".executed", Detail: s.Outcome.Execution}
		}
	}
	if s.Rejection != nil {
		switch s.Rejection.Phase {
		case RejectionPhaseAdmission, RejectionPhaseDispatch, RejectionPhasePolicy:
		default:
			return &ArtifactV2Error{Code: CodeRejectionPhaseInvalid, Field: p + ".rejection.phase", Detail: s.Rejection.Phase}
		}
		if !rejectionCodePattern.MatchString(s.Rejection.Code) {
			return &ArtifactV2Error{Code: CodeRejectionCodeInvalid, Field: p + ".rejection.code", Detail: s.Rejection.Code}
		}
	}
	if s.Executed != nil {
		if err := validateExecutedV2(p+".executed", s.Executed); err != nil {
			return err
		}
	}
	return nil
}

// validateExecutedV2 enforces the executed-identity rules, including AC-V2-06.
func validateExecutedV2(p string, e *ExecutedIdentityV2) error {
	for _, f := range []struct{ field, value string }{
		{p + ".artifact_digest", e.ArtifactDigest},
		{p + ".executed_digest", e.ExecutedDigest},
		{p + ".manifest_digest", e.ManifestDigest},
		{p + ".profile.digest", e.Profile.Digest},
	} {
		if err := requireDigest(f.field, f.value); err != nil {
			return err
		}
	}
	if e.EnvelopeDigest != nil {
		if err := requireDigest(p+".envelope_digest", *e.EnvelopeDigest); err != nil {
			return err
		}
	}
	switch e.TrustState {
	case TrustStateAuthenticated:
		// AC-V2-06: the authenticated trust state is exactly the claim that the
		// envelope identity was verified, so it cannot be made without it.
		if e.EnvelopeDigest == nil {
			return &ArtifactV2Error{Code: CodeAuthenticatedWithoutEnvelopeDigest, Field: p + ".envelope_digest"}
		}
	case TrustStateTransitional, TrustStateDev:
	default:
		return &ArtifactV2Error{Code: CodeTrustStateInvalid, Field: p + ".trust_state", Detail: e.TrustState}
	}
	return nil
}
