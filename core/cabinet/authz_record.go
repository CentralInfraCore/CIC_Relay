package cabinet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// AuthorizationRecordV2 is the authorization decision of one step, with everything the decision is about (O-1 G-2). A digest
// of a record proves only that the bytes are those bytes; the verifier checks the record's CONTENT against the artifact it
// belongs to: the principal, the module envelope, the profile, the operation, the effect, the realization context, the
// policy, the decision and the concrete execution (workflow, step position, input digest) must be the ones of that step.
type AuthorizationRecordV2 struct {
	ModuleID                 string `json:"module_id"`
	EnvelopeDigest           string `json:"envelope_digest"`
	ProfileID                string `json:"profile_id"`
	ProfileDigest            string `json:"profile_digest"`
	Operation                string `json:"operation"`
	HostBoundary             string `json:"host_boundary"`
	Effect                   string `json:"effect"`
	EffectResolved           string `json:"effect_resolved"`
	Principal                string `json:"principal"`
	RealizationContextDigest string `json:"realization_context_digest"`
	PolicyDigest             string `json:"policy_digest"`
	Decision                 string `json:"decision"`
	Reason                   string `json:"reason"`
	WorkflowID               string `json:"workflow_id"`
	StepIndex                int    `json:"step_index"`
	InputDigest              string `json:"input_digest"`
}

// AuthzRecordHeader heads the canonical text of an authorization record.
const AuthzRecordHeader = "CICAUTHZv2"

// None is the value of a record field for which there is no value (an unadmitted module has no envelope, an unconfigured
// build has no realization context). It is explicit text, never an empty string.
const None = "none"

// Canonical is the byte-exact text the record's digest is taken over: the header, then the sixteen fields in this fixed
// order as key=value lines, LF-terminated. Values never contain a line break (see sanitize).
func (r AuthorizationRecordV2) Canonical() string {
	var b strings.Builder
	b.WriteString(AuthzRecordHeader + "\n")
	for _, kv := range [][2]string{
		{"module_id", r.ModuleID}, {"envelope_digest", r.EnvelopeDigest}, {"profile_id", r.ProfileID}, {"profile_digest", r.ProfileDigest},
		{"operation", r.Operation}, {"host_boundary", r.HostBoundary}, {"effect", r.Effect}, {"effect_resolved", r.EffectResolved},
		{"principal", r.Principal}, {"realization_context_digest", r.RealizationContextDigest}, {"policy_digest", r.PolicyDigest},
		{"decision", r.Decision}, {"reason", r.Reason}, {"workflow_id", r.WorkflowID}, {"step_index", strconv.Itoa(r.StepIndex)}, {"input_digest", r.InputDigest},
	} {
		b.WriteString(kv[0] + "=" + sanitize(kv[1]) + "\n")
	}
	return b.String()
}

// sanitize keeps a value on one line (a line break would let one field forge the next).
func sanitize(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

// Digest is the sha256:<hex> of the canonical text.
func (r AuthorizationRecordV2) Digest() string {
	sum := sha256.Sum256([]byte(r.Canonical()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PolicyDigestOf is the digest of the operator's effect policy: the sorted allowed effect classes.
func PolicyDigestOf(allowed []string) string {
	a := append([]string(nil), allowed...)
	sort.Strings(a)
	sum := sha256.Sum256([]byte("CICPOLICYv1\nallowed_effects=" + strings.Join(a, ",") + "\n"))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PrincipalString is how a principal is written into a record: type:id/method, or "unavailable".
func PrincipalString(p Principal, ok bool) string {
	if !ok {
		return "unavailable"
	}
	return p.Type + ":" + p.ID + "/" + p.Method
}

// DecisionArchiver is an optional extension of a TraceV2Source: it receives every authorization record the cabinet itself
// produced for a sealed artifact, so that the records can be kept and handed to the verifier with the artifact.
type DecisionArchiver interface {
	ArchiveDecision(ctx context.Context, digest string, record AuthorizationRecordV2)
}

// WithRealizationContext gives the cabinet the digest of the operator's realization context (the authenticated,
// content-addressed object the observation is realised in). It is a function because the context is loaded after the cabinet
// is built; None is used when it returns "".
func WithRealizationContext(digest func() string) CabinetOption {
	return func(s *cabinetService) { s.realizationDigest = digest }
}

// realization is the realization-context digest the cabinet binds decisions to, or None.
func (c *cabinetService) realization() string {
	if c.realizationDigest != nil {
		if d := c.realizationDigest(); d != "" {
			return d
		}
	}
	return None
}
