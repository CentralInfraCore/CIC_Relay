package cabinet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
)

// This file implements the ABI-profile artifact (D-2/D-3) as specified by
// jobs/o1-wp03-abi-profile-dispatch/ref/02-abi-profile-wire.md §2-3 and its §7
// review corrections (ABI-01..05, SS-05). It is the wire-level type and the raw
// parse; admission (pinning, signature, semantics) is abi_profile_registry.go
// and abi_profile_semantics.go.

// SignSendHostCapability is the one host capability ABI-04 ties to the
// "sign+send" host boundary (ref 02 §7 ABI-04): a profile with any
// "sign+send" operation must declare it, and a profile with no such operation
// must not.
const SignSendHostCapability = "provider-http@1"

// AbiSchemaRef is a (id, digest) reference to an operator-registered request or
// response schema. A reference without a digest is invalid at the wire level
// (ref 02 §2): there is no "trust the id alone" mode.
type AbiSchemaRef struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// AbiEffectResolution is the "contract-dependent" effect resolver (ref 02 §2, D-3):
// the request field whose value selects the effect, the map from field value to
// effect, and the fail-closed default for values the map does not list.
type AbiEffectResolution struct {
	Source  string            `json:"source"`
	Field   string            `json:"field"`
	Map     map[string]string `json:"map"`
	Default string            `json:"default"`
}

// AbiOperation is one entry of a profile's operations[] (ref 02 §2).
type AbiOperation struct {
	Name             string               `json:"name"`
	HostBoundary     string               `json:"host_boundary"`
	Effect           string               `json:"effect"`
	EffectResolution *AbiEffectResolution `json:"effect_resolution,omitempty"`
	RequestSchema    AbiSchemaRef         `json:"request_schema"`
	ResponseSchema   AbiSchemaRef         `json:"response_schema"`
}

// AbiLimits is a profile's optional resource ceilings (ref 02 §2); SS-05 requires
// every field to stay at or below the operator cap (LIMIT_EXCEEDS_OPERATOR_CAP).
type AbiLimits struct {
	MaxRequestBytes  int `json:"max_request_bytes,omitempty"`
	MaxResponseBytes int `json:"max_response_bytes,omitempty"`
	TimeoutMs        int `json:"timeout_ms,omitempty"`
}

// AbiProfile is the parsed ABI-profile artifact (ref 02 §2; schema:
// jobs/o1-wp03-abi-profile-dispatch/ref/schemas/abi-profile.schema.json).
type AbiProfile struct {
	Kind                     string         `json:"kind"`
	ProfileVersion           int            `json:"profile_version"`
	ProfileID                string         `json:"profile_id"`
	Operations               []AbiOperation `json:"operations"`
	RequiredHostCapabilities []string       `json:"required_host_capabilities"`
	Limits                   *AbiLimits     `json:"limits,omitempty"`
}

// FindOperation returns the named operation, if the profile declares it.
func (p *AbiProfile) FindOperation(name string) (AbiOperation, bool) {
	for _, op := range p.Operations {
		if op.Name == name {
			return op, true
		}
	}
	return AbiOperation{}, false
}

// OperationNames returns the profile's declared operation names, in profile order.
func (p *AbiProfile) OperationNames() []string {
	names := make([]string, len(p.Operations))
	for i, op := range p.Operations {
		names[i] = op.Name
	}
	return names
}

var (
	// profileIDPattern is the profile_id wire pattern <namespace>:<name>@<semver>.
	profileIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*:[a-z][a-z0-9-]*@[0-9]+\.[0-9]+\.[0-9]+$`)
	// opNamePattern is the operation name wire pattern.
	opNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// hostCapPattern is the required_host_capabilities entry pattern <name>@<n>.
	hostCapPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*@[0-9]+$`)
	// schemaDigestFormat is the only accepted digest form: sha256: plus 64 lowercase hex.
	schemaDigestFormat = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ParseProfile parses and structurally validates a raw ABI-profile artifact. This
// mirrors the structural ("the JSON Schema enforces this") half of the wire
// contract (ref 02 §2, schemas/abi-profile.schema.json); the runtime-only
// semantic rules (ABI-01..05, SS-05) that the schema cannot express are
// CheckProfileSemantics's job, not this function's. ParseProfile deliberately
// does not accept partial/best-effort profiles: any structural violation is
// ErrInvalid, matching "ismeretlen/nem befogadott profil: elutasítás, nincs
// fallback" (ref 02 §1).
func ParseProfile(raw []byte) (*AbiProfile, error) {
	if err := strictScan(raw); err != nil {
		return nil, fmt.Errorf("%w: abi profile: %v", ErrInvalid, err)
	}
	if err := checkStrictLimits(raw); err != nil {
		return nil, fmt.Errorf("%w: abi profile: %v", ErrInvalid, err)
	}
	var p AbiProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%w: abi profile: invalid JSON: %v", ErrInvalid, err)
	}
	if p.Kind != "AbiProfile" {
		return nil, fmt.Errorf("%w: abi profile: kind must be \"AbiProfile\", got %q", ErrInvalid, p.Kind)
	}
	if p.ProfileVersion != 1 {
		return nil, fmt.Errorf("%w: abi profile: profile_version must be 1, got %d", ErrInvalid, p.ProfileVersion)
	}
	if !profileIDPattern.MatchString(p.ProfileID) {
		return nil, fmt.Errorf("%w: abi profile: profile_id %q does not match <namespace>:<name>@<semver>", ErrInvalid, p.ProfileID)
	}
	if len(p.Operations) == 0 {
		return nil, fmt.Errorf("%w: abi profile: operations must be non-empty", ErrInvalid)
	}
	for i, op := range p.Operations {
		if err := validateOperationShape(op); err != nil {
			return nil, fmt.Errorf("%w: abi profile: operation %d (%q): %v", ErrInvalid, i, op.Name, err)
		}
	}
	seenCap := map[string]bool{}
	for _, c := range p.RequiredHostCapabilities {
		if seenCap[c] {
			return nil, fmt.Errorf("%w: abi profile: duplicate required_host_capabilities entry %q", ErrInvalid, c)
		}
		seenCap[c] = true
		if !hostCapPattern.MatchString(c) {
			return nil, fmt.Errorf("%w: abi profile: required_host_capabilities entry %q does not match <name>@<n>", ErrInvalid, c)
		}
	}
	return &p, nil
}

// validateOperationShape checks the per-operation structural rules the schema
// expresses (name pattern; host_boundary/effect enums; effect_resolution
// required iff effect is contract-dependent; host_boundary "none" implies
// effect "local"; schema refs carry a digest in sha256:<hex> form).
func validateOperationShape(op AbiOperation) error {
	if !opNamePattern.MatchString(op.Name) {
		return fmt.Errorf("name %q does not match ^[a-z][a-z0-9_]*$", op.Name)
	}
	switch op.HostBoundary {
	case "none", "sign+send":
	default:
		return fmt.Errorf("host_boundary %q is not one of none, sign+send", op.HostBoundary)
	}
	switch op.Effect {
	case "local", "external-read", "external-write", "contract-dependent":
	default:
		return fmt.Errorf("effect %q is not one of local, external-read, external-write, contract-dependent", op.Effect)
	}
	if op.Effect == "contract-dependent" && op.EffectResolution == nil {
		return fmt.Errorf("effect_resolution is required when effect is contract-dependent")
	}
	if op.Effect != "contract-dependent" && op.EffectResolution != nil {
		return fmt.Errorf("effect_resolution is not allowed when effect is %q", op.Effect)
	}
	if op.HostBoundary == "none" && op.Effect != "local" {
		return fmt.Errorf("host_boundary \"none\" requires effect \"local\", got %q", op.Effect)
	}
	if op.EffectResolution != nil {
		er := op.EffectResolution
		if er.Source != "request_field" {
			return fmt.Errorf("effect_resolution.source must be \"request_field\", got %q", er.Source)
		}
		if len(er.Map) == 0 {
			return fmt.Errorf("effect_resolution.map must be non-empty")
		}
		if er.Default != "external-write" {
			return fmt.Errorf("effect_resolution.default must be \"external-write\" (fail-closed), got %q", er.Default)
		}
		for v, mapped := range er.Map {
			switch mapped {
			case "local", "external-read", "external-write":
			default:
				return fmt.Errorf("effect_resolution.map[%q] = %q is not one of local, external-read, external-write", v, mapped)
			}
		}
	}
	for _, ref := range []AbiSchemaRef{op.RequestSchema, op.ResponseSchema} {
		if ref.ID == "" {
			return fmt.Errorf("schema reference id is required")
		}
		if !schemaDigestFormat.MatchString(ref.Digest) {
			return fmt.Errorf("schema reference %q digest %q is not sha256:<64 lowercase hex>", ref.ID, ref.Digest)
		}
	}
	return nil
}

// ProfileDigest is ABI-05 (ref 02 §3): the sha256 of the profile artifact's
// published raw bytes, exactly as received — no re-serialization, no
// canonicalization.
func ProfileDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ProfileSignedBytes builds the CICABIPROFv1 signed domain (ref 02 §3):
//
//	CICABIPROFv1
//	profile_sha256=sha256:<hex>
//	signer_key_id=<signerKeyID>
//	signer_alg=<signerAlg>
//
// ASCII, LF-terminated, fixed field order, lowercase hex — the same family of
// rules as CICMODENVv1 (ref 01 §2). This is the byte-exact input to signature
// verification; ProfileRegistry.Admit passes it to the (currently mocked, see
// abi_profile_registry.go) ProfileSignatureVerifier, never a re-derived form.
func ProfileSignedBytes(raw []byte, signerKeyID, signerAlg string) []byte {
	digest := ProfileDigest(raw)
	s := fmt.Sprintf("CICABIPROFv1\nprofile_sha256=%s\nsigner_key_id=%s\nsigner_alg=%s\n", digest, signerKeyID, signerAlg)
	return []byte(s)
}

// ResolveEffect is the runtime half of ABI-02 (ref 02 §7): for a
// "contract-dependent" operation, pick the effect for one concrete request by
// looking up requestFieldValue in the operation's effect_resolution.map, and
// falling back to the deterministic, fail-closed default ("external-write")
// when the value is not in the map. For any other operation, the effect is
// simply op.Effect. ResolveEffect is pure and component-level only in this job
// (WP-03): it is not called from Setx's dispatch path because that path only
// executes "host_boundary: none" operations (host_boundary: "sign+send" is
// WP-04 scope, see abi_profile_dispatch.go), and every "none" operation's
// effect is fixed to "local" by the ABI-01..04 rules this file enforces — so
// ResolveEffect currently has no "contract-dependent" input to resolve in
// production. It is exported and tested so WP-04 can call it unchanged.
func ResolveEffect(op AbiOperation, requestFieldValue string) string {
	if op.Effect != "contract-dependent" || op.EffectResolution == nil {
		return op.Effect
	}
	if mapped, ok := op.EffectResolution.Map[requestFieldValue]; ok {
		return mapped
	}
	return op.EffectResolution.Default
}
