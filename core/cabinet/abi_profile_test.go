package cabinet

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Reference digests used for the test schema registry (the values are arbitrary but fixed).
var (
	tdDescribeReq = "sha256:" + strings.Repeat("ab", 32)
	tdManifest    = "sha256:" + strings.Repeat("cd", 32)
	tdObserveReq  = "sha256:" + strings.Repeat("ef", 32)
	tdObservation = "sha256:" + strings.Repeat("12", 32)
	tdOpReq       = "sha256:" + strings.Repeat("34", 32)
	tdOpResult    = "sha256:" + strings.Repeat("56", 32)
)

func testSchemas() InMemorySchemaRegistry {
	return InMemorySchemaRegistry{
		"cic:provider:describe-request":  {Digest: tdDescribeReq},
		"cic:provider:module-manifest":   {Digest: tdManifest},
		"cic:provider:observe-request":   {Digest: tdObserveReq},
		"cic:provider:observation":       {Digest: tdObservation},
		"cic:provider:operation-request": {Digest: tdOpReq, Fields: map[string][]string{"operation_role": {"read", "action"}}},
		"cic:provider:operation-result":  {Digest: tdOpResult},
	}
}

var testCaps = OperatorLimits{MaxRequestBytes: 1048576, MaxResponseBytes: 1048576, TimeoutMs: 60000}

// refProfile mirrors the reference PROF of build_o1_schemas.py (describe local, observe sign+send,
// invoke contract-dependent), as a mutable map so a test can break exactly one thing.
func refProfile() map[string]any {
	ref := func(id, digest string) map[string]any { return map[string]any{"id": id, "digest": digest} }
	return map[string]any{
		"kind": "AbiProfile", "profile_version": 1, "profile_id": "cic:provider@0.1.0",
		"required_host_capabilities": []any{"provider-http@1"},
		"limits":                     map[string]any{"max_request_bytes": 1048576, "max_response_bytes": 262144, "timeout_ms": 30000},
		"operations": []any{
			map[string]any{"name": "describe", "host_boundary": "none", "effect": "local",
				"request_schema": ref("cic:provider:describe-request", tdDescribeReq), "response_schema": ref("cic:provider:module-manifest", tdManifest)},
			map[string]any{"name": "observe", "host_boundary": "sign+send", "effect": "external-read",
				"request_schema": ref("cic:provider:observe-request", tdObserveReq), "response_schema": ref("cic:provider:observation", tdObservation)},
			map[string]any{"name": "invoke", "host_boundary": "sign+send", "effect": "contract-dependent",
				"effect_resolution": map[string]any{"source": "request_field", "field": "operation_role", "map": map[string]any{"read": "external-read", "action": "external-write"}, "default": "external-write"},
				"request_schema":    ref("cic:provider:operation-request", tdOpReq), "response_schema": ref("cic:provider:operation-result", tdOpResult)},
		},
	}
}

func opAt(p map[string]any, i int) map[string]any { return p["operations"].([]any)[i].(map[string]any) }

func rawOf(t *testing.T, p map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(p)
	require.NoError(t, err)
	return b
}

func mustParse(t *testing.T, p map[string]any) *AbiProfile {
	t.Helper()
	pr, err := ParseProfile(rawOf(t, p))
	require.NoError(t, err)
	return pr
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	got, ok := ABIProfileErrorCode(err)
	require.True(t, ok, "expected an *ABIProfileError, got %T: %v", err, err)
	assert.Equal(t, code, got)
	assert.True(t, errors.Is(err, ErrInvalid))
}

// --- AC-03-3: the ten reference cases of check_profile_semantics (validation-report.txt "abi-semantics ...") ---

func TestAC03_3_ProfileSemanticsReferenceCases(t *testing.T) {
	cases := []struct {
		name string
		mut  func(p map[string]any)
		code string // "" = valid
	}{
		{"profile-valid", func(p map[string]any) {}, ""},
		{"duplicate-operation-name", func(p map[string]any) {
			p["operations"] = append(p["operations"].([]any), opAt(p, 1))
		}, CodeDuplicateOperation},
		{"schema-ref-unknown-id", func(p map[string]any) {
			opAt(p, 1)["request_schema"].(map[string]any)["id"] = "cic:provider:nope"
		}, CodeSchemaRefUnresolved},
		{"schema-ref-digest-mismatch", func(p map[string]any) {
			opAt(p, 1)["response_schema"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("99", 32)
		}, CodeSchemaDigestMismatch},
		{"effect-field-not-in-request-schema", func(p map[string]any) {
			opAt(p, 2)["effect_resolution"].(map[string]any)["field"] = "nope"
		}, CodeEffectFieldUnresolvable},
		{"effect-map-key-outside-field-domain", func(p map[string]any) {
			opAt(p, 2)["effect_resolution"].(map[string]any)["map"].(map[string]any)["bogus"] = "external-read"
		}, CodeEffectMapKeyNotInFieldDomain},
		{"send-op-without-host-capability", func(p map[string]any) { p["required_host_capabilities"] = []any{} }, CodeHostCapabilityInconsistent},
		{"capability-without-any-send-op", func(p map[string]any) { p["operations"] = []any{opAt(p, 0)} }, CodeHostCapabilityInconsistent},
		{"limit-above-operator-cap", func(p map[string]any) { p["limits"].(map[string]any)["timeout_ms"] = 600000 }, CodeLimitExceedsOperatorCap},
		{"request-limit-above-operator-cap", func(p map[string]any) { p["limits"].(map[string]any)["max_request_bytes"] = 99999999 }, CodeLimitExceedsOperatorCap},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := refProfile()
			c.mut(p)
			pr := mustParse(t, p)
			err := CheckProfileSemantics(pr, testSchemas(), testCaps)
			if c.code == "" {
				require.NoError(t, err)
				return
			}
			requireCode(t, err, c.code)
		})
	}
}

// ResolveEffect: unmapped values of the field fall to the fail-closed default (ABI-02).
func TestResolveEffectFailClosed(t *testing.T) {
	op := mustParse(t, refProfile()).Operations[2]
	assert.Equal(t, "external-read", ResolveEffect(op, "read"))
	assert.Equal(t, "external-write", ResolveEffect(op, "action"))
	assert.Equal(t, "external-write", ResolveEffect(op, "something-unmapped"))
	assert.Equal(t, "external-write", ResolveEffect(op, ""))
	assert.Equal(t, "external-read", ResolveEffect(mustParse(t, refProfile()).Operations[1], "anything"))
}

// --- structural (schema-level) rejections: the wire schema's invalid profile examples ---

func TestParseProfileStructuralRejections(t *testing.T) {
	cases := []struct {
		name string
		mut  func(p map[string]any)
		want string
	}{
		{"unknown-effect", func(p map[string]any) { opAt(p, 1)["effect"] = "read-only" }, "effect"},
		{"contract-dependent-without-resolution", func(p map[string]any) { delete(opAt(p, 2), "effect_resolution") }, "effect_resolution is required"},
		{"local-boundary-with-external-effect", func(p map[string]any) { opAt(p, 0)["effect"] = "external-write" }, "requires effect"},
		{"schema-ref-without-digest", func(p map[string]any) {
			opAt(p, 1)["request_schema"] = map[string]any{"id": "cic:provider:observe-request"}
		}, "digest"},
		{"unresolvable-defaults-to-read", func(p map[string]any) { opAt(p, 2)["effect_resolution"].(map[string]any)["default"] = "external-read" }, "fail-closed"},
		{"empty-effect-map", func(p map[string]any) { opAt(p, 2)["effect_resolution"].(map[string]any)["map"] = map[string]any{} }, "non-empty"},
		{"wrong-kind", func(p map[string]any) { p["kind"] = "Other" }, "kind"},
		{"wrong-version", func(p map[string]any) { p["profile_version"] = 2 }, "profile_version"},
		{"bad-profile-id", func(p map[string]any) { p["profile_id"] = "Provider-1" }, "profile_id"},
		{"no-operations", func(p map[string]any) { p["operations"] = []any{} }, "non-empty"},
		{"bad-capability", func(p map[string]any) { p["required_host_capabilities"] = []any{"NoVersion"} }, "required_host_capabilities"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := refProfile()
			c.mut(p)
			_, err := ParseProfile(rawOf(t, p))
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalid))
			assert.Contains(t, err.Error(), c.want)
		})
	}
	_, err := ParseProfile([]byte("not json"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalid))
}

// --- ABI-05: digest = sha256 of the raw bytes; the signed domain matches the shared provisional vector ---

func TestABI05DigestAndSignedDomainMatchSharedVector(t *testing.T) {
	var v struct {
		RawB64      string `json:"profile_raw_b64"`
		KeyID       string `json:"signer_key_id"`
		Alg         string `json:"signer_alg"`
		ExpectedB64 string `json:"expect_signed_bytes_b64"`
	}
	b, err := os.ReadFile("testdata/abi-profile-domain-vector.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &v))
	raw, err := base64.StdEncoding.DecodeString(v.RawB64)
	require.NoError(t, err)
	want, err := base64.StdEncoding.DecodeString(v.ExpectedB64)
	require.NoError(t, err)

	assert.Equal(t, string(want), string(ProfileSignedBytes(raw, v.KeyID, v.Alg)))
	sum := sha256.Sum256(raw)
	assert.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), ProfileDigest(raw))
	// any byte of the artifact changes the domain (no re-serialization, no canonicalization)
	assert.NotEqual(t, string(want), string(ProfileSignedBytes(append(append([]byte{}, raw...), ' '), v.KeyID, v.Alg)))
}

// --- admission: operator pin list, signature seam, semantics (AC-03-2 and AC-03-1) ---

func admit(t *testing.T, reg *ProfileRegistry, p map[string]any) (*AbiProfile, string, error) {
	return reg.Admit(rawOf(t, p), ProfileSignature{SignerKeyID: "sha256:" + "ef", SignerAlg: "ed25519", Signature: []byte("sig")})
}

func registryPinning(t *testing.T, v ProfileSignatureVerifier, profiles ...map[string]any) *ProfileRegistry {
	var pins []PinnedProfile
	for _, p := range profiles {
		pins = append(pins, PinnedProfile{ID: p["profile_id"].(string), Digest: ProfileDigest(rawOf(t, p))})
	}
	return NewProfileRegistry(pins, v, testSchemas(), testCaps)
}

func TestAdmitSuccessAndOrder(t *testing.T) {
	p := refProfile()
	reg := registryPinning(t, MockProfileSignatureVerifier{}, p)
	pr, digest, err := admit(t, reg, p)
	require.NoError(t, err)
	assert.Equal(t, "cic:provider@0.1.0", pr.ProfileID)
	assert.Equal(t, ProfileDigest(rawOf(t, p)), digest)
}

func TestAC03_2_UnknownOrUnpinnedProfileRejected(t *testing.T) {
	pinned := refProfile()
	reg := registryPinning(t, MockProfileSignatureVerifier{}, pinned)

	// a different, perfectly valid profile that the operator did not pin
	other := refProfile()
	other["profile_id"] = "cic:other@0.1.0"
	_, _, err := admit(t, reg, other)
	requireCode(t, err, CodeProfileUnknown)

	// same id, one byte changed in the published artifact: the pinned digest no longer matches
	changed := refProfile()
	changed["limits"].(map[string]any)["timeout_ms"] = 29999
	_, _, err = admit(t, reg, changed)
	requireCode(t, err, CodeProfileDigestMismatch)

	// a changed profile has a different digest (AC: the modified profile changes the identity)
	assert.NotEqual(t, ProfileDigest(rawOf(t, pinned)), ProfileDigest(rawOf(t, changed)))
}

func TestAdmitSignatureFailureRejectsAndNoFallback(t *testing.T) {
	p := refProfile()
	reg := registryPinning(t, MockProfileSignatureVerifier{Err: errors.New("bad signature")}, p)
	pr, _, err := admit(t, reg, p)
	require.Error(t, err)
	assert.Nil(t, pr)
	assert.True(t, errors.Is(err, ErrInvalid))
	assert.Contains(t, err.Error(), CodeProfileSignatureInvalid)
}

func TestAdmitRunsSemanticsAfterPinAndSignature(t *testing.T) {
	// pinned and "signed", but semantically broken: a send op without the host capability
	p := refProfile()
	p["required_host_capabilities"] = []any{}
	reg := registryPinning(t, MockProfileSignatureVerifier{}, p)
	_, _, err := admit(t, reg, p)
	requireCode(t, err, CodeHostCapabilityInconsistent)
}

// AC-03-1: a second, synthetic profile is admitted purely by configuration: a new pin and new schema
// entries, with no change to ProfileRegistry or CheckProfileSemantics.
func TestAC03_1_SecondSyntheticProfileAdmittedByConfigurationOnly(t *testing.T) {
	second := map[string]any{
		"kind": "AbiProfile", "profile_version": 1, "profile_id": "acme:echo@1.0.0",
		"required_host_capabilities": []any{},
		"operations": []any{
			map[string]any{"name": "ping", "host_boundary": "none", "effect": "local",
				"request_schema":  map[string]any{"id": "acme:echo:ping-request", "digest": "sha256:" + strings.Repeat("78", 32)},
				"response_schema": map[string]any{"id": "acme:echo:ping-response", "digest": "sha256:" + strings.Repeat("79", 32)}},
		},
	}
	schemas := testSchemas()
	schemas["acme:echo:ping-request"] = SchemaRegistryEntry{Digest: "sha256:" + strings.Repeat("78", 32)}
	schemas["acme:echo:ping-response"] = SchemaRegistryEntry{Digest: "sha256:" + strings.Repeat("79", 32)}
	base := refProfile()
	reg := NewProfileRegistry([]PinnedProfile{
		{ID: "cic:provider@0.1.0", Digest: ProfileDigest(rawOf(t, base))},
		{ID: "acme:echo@1.0.0", Digest: ProfileDigest(rawOf(t, second))},
	}, MockProfileSignatureVerifier{}, schemas, testCaps)

	pr, _, err := admit(t, reg, second)
	require.NoError(t, err)
	assert.Equal(t, []string{"ping"}, pr.OperationNames())
	_, _, err = admit(t, reg, base)
	require.NoError(t, err, "the first profile is unaffected by adding the second")
}

// --- manifest side: pin and op-set (AC-03-2) ---

func admittedBase(t *testing.T) (*AbiProfile, string, PinnedProfile) {
	p := refProfile()
	reg := registryPinning(t, MockProfileSignatureVerifier{}, p)
	pr, digest, err := admit(t, reg, p)
	require.NoError(t, err)
	return pr, digest, PinnedProfile{ID: pr.ProfileID, Digest: digest}
}

var wasmHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func plainModule(t *testing.T, name string) *ModuleDescriptor {
	d, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: name, Version: "1.0"}, "wasm/test", nil, nil, wasmHeader)
	require.NoError(t, err)
	return d
}

func TestAC03_2_ManifestPinAndOpSet(t *testing.T) {
	pr, digest, pin := admittedBase(t)
	mod := plainModule(t, "prov.wasm")

	b, err := NewModuleProfileBinding(mod, pr, digest, pin, []string{"describe"})
	require.NoError(t, err)
	assert.Equal(t, pin.ID, b.ProfileID)
	assert.Equal(t, pin.Digest, b.ProfileDigest)
	assert.Equal(t, mod.Checksum(), b.ModuleChecksum)

	// a subset is fine, the whole set is fine
	_, err = NewModuleProfileBinding(mod, pr, digest, pin, []string{"describe", "observe", "invoke"})
	require.NoError(t, err)

	// the manifest pin differs: digest, then id
	_, err = NewModuleProfileBinding(mod, pr, digest, PinnedProfile{ID: pin.ID, Digest: "sha256:" + strings.Repeat("00", 32)}, []string{"describe"})
	requireCode(t, err, CodeProfilePinMismatch)
	_, err = NewModuleProfileBinding(mod, pr, digest, PinnedProfile{ID: "cic:other@0.1.0", Digest: digest}, []string{"describe"})
	requireCode(t, err, CodeProfilePinMismatch)

	// an extra manifest op that the pinned profile does not have
	_, err = NewModuleProfileBinding(mod, pr, digest, pin, []string{"describe", "destroy"})
	requireCode(t, err, CodeManifestOpNotInProfile)

	// empty op declaration, a missing profile and a missing module are rejected, not defaulted
	_, err = NewModuleProfileBinding(mod, pr, digest, pin, nil)
	require.Error(t, err)
	_, err = NewModuleProfileBinding(mod, nil, digest, pin, []string{"describe"})
	require.Error(t, err)
	_, err = NewModuleProfileBinding(nil, pr, digest, pin, []string{"describe"})
	require.Error(t, err)
}

// The profile binding is a separate value: ModuleDescriptor (public, shape-pinned) and its Checksum() are untouched.
func TestBindingDoesNotChangeModuleIdentity(t *testing.T) {
	pr, digest, pin := admittedBase(t)
	mod := plainModule(t, "prov.wasm")
	before := mod.Checksum()
	_, err := NewModuleProfileBinding(mod, pr, digest, pin, []string{"describe"})
	require.NoError(t, err)
	assert.Equal(t, before, mod.Checksum())
	assert.Equal(t, before, plainModule(t, "prov.wasm").Checksum())
}

// --- AC-03-4/5: dispatch. Off by default, the legacy four ops are untouched; the switch alone turns the profile path on ---

func runStep(t *testing.T, svc *cabinetService, mod *ModuleDescriptor, step string) (*SetResponse, error) {
	t.Helper()
	ctx := context.Background()
	wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"}, WorkflowConfig{Name: "wf." + step, Steps: []string{mod.ID() + "." + step + "(input)"}}, WorkflowSigning{})
	require.NoError(t, err)
	svc.workflows.Put(wf)
	return svc.Setx(ctx, &SetPayload{WorkflowID: wf.ID(), Payload: map[string]any{"input": map[string]any{"k": "v"}}})
}

func dispatchSvc(enabled bool, mod *ModuleDescriptor, host *MockCicWasmHost, bindings ...*ModuleProfileBinding) *cabinetService {
	modStore := NewMemModuleStore()
	modStore.Put(mod)
	mgr := new(MockWasmManager)
	mgr.On("NewHostInstance", mock.Anything, mod.ID()).Return(cicWasmHost(host), nil).Maybe()
	host.On("Close", mock.Anything).Return(nil).Maybe()
	svc := &cabinetService{modules: modStore, workflows: NewMemWorkflowStore(), wasmManager: mgr}
	WithGenericDispatch(enabled)(svc)
	WithModuleProfileBindings(bindings...)(svc)
	return svc
}

func profiledModule(t *testing.T, declared []string) (*ModuleDescriptor, *ModuleProfileBinding) {
	pr, digest, pin := admittedBase(t)
	mod := plainModule(t, "prov.wasm")
	b, err := NewModuleProfileBinding(mod, pr, digest, pin, declared)
	require.NoError(t, err)
	return mod, b
}

func TestAC03_5_DefaultOffUsesLegacyDispatchEvenForABoundModule(t *testing.T) {
	mod, b := profiledModule(t, []string{"describe", "observe", "invoke"})
	host := new(MockCicWasmHost)
	svc := dispatchSvc(false, mod, host, b)

	// the profile op "describe" is not one of the legacy four: with the switch off it is the legacy error, and Call is never used
	_, err := runStep(t, svc, mod, "describe")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support operation")
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	// a legacy op still goes through the legacy method
	host.On("Process", mock.Anything, mock.Anything, mock.Anything).Return(`{"data":"ok"}`, "null").Once()
	resp, err := runStep(t, svc, mod, "process")
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Results["result"].(map[string]interface{})["data"])
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAC03_4_GenericDispatchSwitchOnProfileOpsRun(t *testing.T) {
	mod, b := profiledModule(t, []string{"describe", "observe", "invoke"})
	host := new(MockCicWasmHost)
	svc := dispatchSvc(true, mod, host, b)

	// profile op with host_boundary "none": executed through the generic Call with the profile's op name
	host.On("Call", mock.Anything, "describe", mock.Anything, mock.Anything).Return(`{"data":"described"}`, "null").Once()
	resp, err := runStep(t, svc, mod, "describe")
	require.NoError(t, err)
	assert.Equal(t, "described", resp.Results["result"].(map[string]interface{})["data"])
	host.AssertExpectations(t)

	// sign+send ops are WP-04 scope: fail closed, no fallback, no Call
	for _, op := range []string{"observe", "invoke"} {
		_, err = runStep(t, svc, mod, op)
		require.Error(t, err, op)
		assert.True(t, errors.Is(err, ErrInvalid), op)
		assert.Contains(t, err.Error(), "host_boundary")
	}
	host.AssertNumberOfCalls(t, "Call", 1)

	// an op that the profile does not declare is rejected (not forwarded to the guest)
	_, err = runStep(t, svc, mod, "destroy")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalid))
	host.AssertNumberOfCalls(t, "Call", 1)
}

// The manifest op-set is enforced at dispatch too: a profile op the manifest does not declare is not callable.
func TestDispatchHonoursTheManifestOpSet(t *testing.T) {
	mod, b := profiledModule(t, []string{"describe"})
	host := new(MockCicWasmHost)
	svc := dispatchSvc(true, mod, host, b)
	_, err := runStep(t, svc, mod, "invoke")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalid))
	assert.Contains(t, err.Error(), "does not declare operation")
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// A reused module ID with different content does not inherit the binding (it falls back to the legacy dispatch, whose op-set is the four).
func TestReusedModuleIDWithDifferentContentDoesNotInheritBinding(t *testing.T) {
	_, b := profiledModule(t, []string{"describe"})
	other, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: "prov.wasm", Version: "1.0"}, "wasm/test", nil, []string{"x"}, wasmHeader)
	require.NoError(t, err)
	require.Equal(t, b.ModuleID, other.ID())
	require.NotEqual(t, b.ModuleChecksum, other.Checksum())
	host := new(MockCicWasmHost)
	svc := dispatchSvc(true, other, host, b)
	_, err = runStep(t, svc, other, "describe")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support operation")
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLegacyModuleUnaffectedWhenSwitchIsOn(t *testing.T) {
	legacy := plainModule(t, "legacy.wasm")
	host := new(MockCicWasmHost)
	svc := dispatchSvc(true, legacy, host)
	host.On("Init", mock.Anything, mock.Anything, mock.Anything).Return(`{"data":"inited"}`, "null").Once()
	resp, err := runStep(t, svc, legacy, "init")
	require.NoError(t, err)
	assert.Equal(t, "inited", resp.Results["result"].(map[string]interface{})["data"])
	host.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	_, err = runStep(t, svc, legacy, "describe")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support operation")
}
