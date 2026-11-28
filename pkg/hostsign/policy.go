package hostsign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Limits are the timeout and size caps of one call. The operator policy gives the upper bound;
// a profile can only narrow it; a guest request carries no limit at all (SS-05).
type Limits struct {
	TimeoutMS        int `json:"timeout_ms"`
	MaxRequestBytes  int `json:"max_request_bytes"`
	MaxResponseBytes int `json:"max_response_bytes"`
}

// ErrLimitExceedsOperatorCap is returned when a profile limit is wider than the operator cap.
var ErrLimitExceedsOperatorCap = errors.New("LIMIT_EXCEEDS_OPERATOR_CAP")

// effectiveLimits is min(operator, profile) per field; a zero profile field does not narrow.
// It fails if the profile limit is wider than the operator cap.
func effectiveLimits(operator, profile Limits) (Limits, error) {
	pick := func(op, pr int) (int, error) {
		if pr == 0 {
			return op, nil
		}
		if pr > op {
			return 0, ErrLimitExceedsOperatorCap
		}
		return pr, nil
	}
	var out Limits
	var err error
	if out.TimeoutMS, err = pick(operator.TimeoutMS, profile.TimeoutMS); err != nil {
		return Limits{}, err
	}
	if out.MaxRequestBytes, err = pick(operator.MaxRequestBytes, profile.MaxRequestBytes); err != nil {
		return Limits{}, err
	}
	if out.MaxResponseBytes, err = pick(operator.MaxResponseBytes, profile.MaxResponseBytes); err != nil {
		return Limits{}, err
	}
	return out, nil
}

// OperationPolicy is what the operator allows for one intent operation.
type OperationPolicy struct {
	Methods      []string `json:"methods"`
	PathTemplate string   `json:"path_template"` // contains exactly one {resource_id}
	QueryKeys    []string `json:"query_keys,omitempty"`
}

// Policy is the operator-owned deployment policy the host validates every request against.
// It is NOT guest-supplied and not part of the module artifact.
type Policy struct {
	Hosts                   []string                   `json:"hosts"`
	Operations              map[string]OperationPolicy `json:"operations"`
	Bindings                map[string]string          `json:"bindings"` // resource_id -> binding digest
	ResponseHeaderAllowlist []string                   `json:"response_header_allowlist"`
	// ObservationOnly makes the policy an observation profile: no method but GET/HEAD leaves the
	// host, whatever the operations list allows.
	ObservationOnly bool   `json:"observation_only"`
	Limits          Limits `json:"limits"`
}

var (
	// sha256Re is a package-level table or pattern of the cic-flow.send host capability.
	sha256Re = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// pathTrickRe is a package-level table or pattern of the cic-flow.send host capability.
	pathTrickRe = regexp.MustCompile(`(?i)(^|/)\.\.?(/|$)|//|%2[ef]|%5c|\\`)
	// validMethods is a package-level table or pattern of the cic-flow.send host capability.
	validMethods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
)

// resourcePlaceholder is a named constant of the cic-flow.send host capability.
const resourcePlaceholder = "{resource_id}"

// Validate rejects a policy that cannot be enforced unambiguously (fail-closed at configuration).
func (p Policy) Validate() error {
	if len(p.Hosts) == 0 {
		return errors.New("policy: no hosts")
	}
	for _, h := range p.Hosts {
		if h == "" || h != strings.ToLower(h) || strings.ContainsAny(h, "*/ ?#") {
			return fmt.Errorf("policy: host %q must be an exact lowercase host name", h)
		}
	}
	if len(p.Operations) == 0 {
		return errors.New("policy: no operations")
	}
	for name, op := range p.Operations {
		if name == "" || len(op.Methods) == 0 {
			return fmt.Errorf("policy: operation %q needs methods", name)
		}
		for _, m := range op.Methods {
			if !validMethods[m] {
				return fmt.Errorf("policy: operation %q: unknown method %q", name, m)
			}
		}
		if strings.Count(op.PathTemplate, resourcePlaceholder) != 1 || !strings.HasPrefix(op.PathTemplate, "/") ||
			strings.ContainsAny(op.PathTemplate, "?#") {
			return fmt.Errorf("policy: operation %q: path_template must start with / and hold exactly one %s", name, resourcePlaceholder)
		}
	}
	for id, d := range p.Bindings {
		if id == "" || pathTrickRe.MatchString(id) || strings.ContainsAny(id, "/?#% ") {
			return fmt.Errorf("policy: resource id %q is not path-safe", id)
		}
		if !sha256Re.MatchString(d) {
			return fmt.Errorf("policy: binding digest of %q is not sha256:<hex>", id)
		}
	}
	for _, h := range p.ResponseHeaderAllowlist {
		if h != strings.ToLower(h) {
			return fmt.Errorf("policy: response header %q must be lowercase", h)
		}
	}
	l := p.Limits
	if l.TimeoutMS <= 0 || l.MaxRequestBytes <= 0 || l.MaxResponseBytes <= 0 {
		return errors.New("policy: operator limits must be positive")
	}
	return nil
}

// Digest is the policy digest recorded in every host interaction: sha256 over a domain-separated
// canonical JSON form (encoding/json sorts map keys; list fields are sorted copies).
func (p Policy) Digest() string {
	c := p
	c.Hosts = sortedCopy(p.Hosts)
	c.ResponseHeaderAllowlist = sortedCopy(p.ResponseHeaderAllowlist)
	c.Operations = map[string]OperationPolicy{}
	for k, op := range p.Operations {
		op.Methods = sortedCopy(op.Methods)
		op.QueryKeys = sortedCopy(op.QueryKeys)
		c.Operations[k] = op
	}
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(append([]byte("CICHOSTPOLICYv1\n"), b...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// sortedCopy is a helper of the cic-flow.send host capability.
func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

// contains is a helper of the cic-flow.send host capability.
func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// TargetIntent is the intent part of a request as the target validation sees it.
type TargetIntent struct {
	Operation     string
	ResourceID    string
	BindingDigest string
}

// ValidateTarget is the normative host-side target validation (contract SS-03). It returns "" when
// the request is within the policy, else the POLICY_* code of the first failing step, in the
// fixed order: (1) operation, (2) host, (3) method, (4) resource, (5) binding digest, (6) path+query.
func (p Policy) ValidateTarget(in TargetIntent, method, host, path string, query map[string]string) string {
	op, ok := p.Operations[in.Operation]
	if !ok {
		return CodePolicyTarget
	}
	if !contains(p.Hosts, host) {
		return CodePolicyHost
	}
	if !contains(op.Methods, method) || (p.ObservationOnly && method != "GET" && method != "HEAD") {
		return CodePolicyMethod
	}
	want, ok := p.Bindings[in.ResourceID]
	if !ok {
		return CodePolicyTarget
	}
	if want != in.BindingDigest {
		return CodePolicyBinding
	}
	if pathTrickRe.MatchString(path) || path != strings.Replace(op.PathTemplate, resourcePlaceholder, in.ResourceID, 1) {
		return CodePolicyPath
	}
	for k := range query {
		if !contains(op.QueryKeys, k) {
			return CodePolicyPath
		}
	}
	return ""
}
