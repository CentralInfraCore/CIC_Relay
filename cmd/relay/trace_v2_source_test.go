package main

// INT-08/09: the production TraceV2Source, from the admission record to the independent verifier.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/hostsign"
	"centralrelay/pkg/o1verify"
	"centralrelay/pkg/o1verify/attest"
)

func d64(c string) string { return "sha256:" + strings.Repeat(c, 32) }

// a record as the engine writes it (the log assigns seq and interaction_id)
func rec(decision string) hostsign.Record {
	s := 200
	return hostsign.Record{Decision: decision, Method: "GET", Host: "iaas.eu-frankfurt-1.oraclecloud.com", PathSHA256: d64("11"), Status: &s,
		PolicyDigest: d64("22"), ProfileDigest: d64("33"), SigningProfileDigest: d64("44"),
		Intent: hostsign.RecordIntent{Operation: "observe", ResourceID: "ocid1.vcn.oc1..a", BindingDigest: d64("55")}}
}

func TestTraceSourceHostInteractionsAreScopedPerStep(t *testing.T) {
	src := newTraceSource(newIdentityRegistry(), "prod", nil)
	step := cabinet.ExecutionGraphStep{}
	ctxA, ctxB := src.ScopeStep(context.Background(), step), src.ScopeStep(context.Background(), step)

	logA, ok := hostsign.LogFrom(ctxA)
	require.True(t, ok)
	_, _, err := logA.Append(rec("allowed"))
	require.NoError(t, err)
	_, _, err = logA.Append(rec("denied"))
	require.NoError(t, err)

	dA, nA := src.HostInteractions(ctxA, step)
	dB, nB := src.HostInteractions(ctxB, step)
	assert.Equal(t, 2, nA, "denied calls are part of the list")
	assert.Equal(t, 0, nB, "another step's scope is empty")
	assert.Equal(t, cabinet.EmptyHostInteractionsDigestV2(), dB, "the empty list has the empty-list digest")
	assert.NotEqual(t, dA, dB)

	recs, ok := src.Interactions(dA)
	require.True(t, ok)
	want, err := hostsign.InteractionsDigest(recs)
	require.NoError(t, err)
	assert.Equal(t, want, dA)

	// no scope at all (V2 on, step not scoped): the empty list
	d0, n0 := src.HostInteractions(context.Background(), step)
	assert.Equal(t, cabinet.EmptyHostInteractionsDigestV2(), d0)
	assert.Equal(t, 0, n0)
	// an unknown digest has no records
	_, ok = src.Interactions(d64("ee"))
	assert.False(t, ok)
}

func TestIdentityIsBoundToTheAdmittedArtifact(t *testing.T) {
	ctx := context.Background()
	mod, err := cabinet.NewModuleDescriptor(ctx, cabinet.BaseBlock{Name: "adm.mod", Version: "1.0.0"}, "wasm", nil, nil, []byte{0, 0x61, 0x73, 0x6d, 1, 0, 0, 0})
	require.NoError(t, err)
	b := &cabinet.ModuleProfileBinding{ProfileID: "cic:provider@0.1.0", ProfileDigest: d64("34")}
	ident := executedIdentityOf(strings.Repeat("ab", 32), mod, []byte("manifest"), d64("12"), b)
	assert.Equal(t, "sha256:"+strings.Repeat("ab", 32), ident.ArtifactDigest)
	assert.Equal(t, sha256Of(mod.WasmCode), ident.ExecutedDigest)
	assert.Equal(t, sha256Of([]byte("manifest")), ident.ManifestDigest)
	assert.Equal(t, d64("12"), *ident.EnvelopeDigest)
	assert.Equal(t, cabinet.ProfileRefV2{ID: "cic:provider@0.1.0", Digest: d64("34")}, ident.Profile)
	assert.Equal(t, cabinet.TrustStateAuthenticated, ident.TrustState)

	reg := newIdentityRegistry()
	src := newTraceSource(reg, "prod", nil)
	_, ok := src.ExecutedIdentity(mod, "observe")
	assert.False(t, ok, "never admitted: no executed identity may be claimed")

	reg.set(mod.ID(), mod.Checksum(), ident)
	got, ok := src.ExecutedIdentity(mod, "observe")
	require.True(t, ok)
	assert.Equal(t, "observe", got.Dispatched, "the dispatched operation is the call's")

	reg.set(mod.ID(), "another-checksum", ident)
	_, ok = src.ExecutedIdentity(mod, "observe")
	assert.False(t, ok, "a different artifact of the same id does not inherit the record")

	reg.set(mod.ID(), mod.Checksum(), ident)
	reg.forget(mod.ID())
	_, ok = src.ExecutedIdentity(mod, "observe")
	assert.False(t, ok, "a reload that is not admitted with a profile drops it")
}

func TestAuthorizationAndContextRecords(t *testing.T) {
	src := newTraceSource(newIdentityRegistry(), "prod", []string{"cic.artifact.sign@1.0"})
	out, dec := src.Authorization(context.Background(), cabinet.ExecutionGraphStep{ComponentID: "cic.artifact.sign@1.0"})
	assert.Equal(t, cabinet.AuthorizationDenied, out)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, dec)
	out2, dec2 := src.Authorization(context.Background(), cabinet.ExecutionGraphStep{ComponentID: "cic.artifact.sign@1.0"})
	assert.Equal(t, dec, dec2, "the decision record digest is deterministic")
	out, dec = src.Authorization(context.Background(), cabinet.ExecutionGraphStep{ComponentID: "echo@1.0"})
	assert.Equal(t, cabinet.AuthorizationNotEvaluated, out)
	assert.Empty(t, dec)
	_ = out2

	a := src.ExecutionContext(context.Background(), "wf.1")
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, a)
	assert.NotEqual(t, a, src.ExecutionContext(context.Background(), "wf.2"), "bound to the workflow")
	assert.NotEqual(t, a, newTraceSource(newIdentityRegistry(), "dev", nil).ExecutionContext(context.Background(), "wf.1"), "bound to the trust mode")
}

// The whole chain: a Setx run through the real cabinet with the production source; the module's host calls write into the
// step's scope the way the send engine does; the sealed artifact is accepted by the INDEPENDENT verifier with exactly the
// archived records, and the executed identity is the admission record.
func TestProductionSourceEndToEndWithTheIndependentVerifier(t *testing.T) {
	ctx := context.Background()
	reg := newIdentityRegistry()
	src := newTraceSource(reg, "prod", nil)
	var sealed []*cabinet.ProofArtifactV2

	mods := cabinet.NewMemModuleStore()
	mod, err := cabinet.NewModuleDescriptor(ctx, cabinet.BaseBlock{Name: "echo", Version: "1.0"}, "native", nil, nil, nil)
	require.NoError(t, err)
	mod.NativeImpl = cabinet.NativeFunctions{"echo": func(ctx context.Context, in interface{}) (interface{}, error) {
		l, ok := hostsign.LogFrom(ctx)
		require.True(t, ok, "the module's host calls run under the step's scope")
		_, _, err := l.Append(rec("allowed"))
		require.NoError(t, err)
		_, _, err = l.Append(rec("failed"))
		require.NoError(t, err)
		return in, nil
	}}
	mods.Put(mod)
	reg.set(mod.ID(), mod.Checksum(), executedIdentityOf(strings.Repeat("ab", 32), mod, []byte("manifest"), d64("12"),
		&cabinet.ModuleProfileBinding{ProfileID: "cic:provider@0.1.0", ProfileDigest: d64("34")}))

	svc, err := cabinet.NewCabinetService(ctx, cabinet.NewMemSchemaStore(), mods, cabinet.NewMemWorkflowStore(),
		cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1},
		cabinet.WithTraceV2(src, func(_ context.Context, a *cabinet.ProofArtifactV2) { sealed = append(sealed, a) }))
	require.NoError(t, err)
	wf, err := cabinet.NewWorkflow(ctx, cabinet.WorkflowIdentity{Version: "1.0"}, cabinet.WorkflowConfig{Name: "wf.e2e", Steps: []string{"echo@1.0.echo(input) -> out"}}, cabinet.WorkflowSigning{})
	require.NoError(t, err)
	require.NoError(t, svc.PutWorkflow(wf))
	_, err = svc.Setx(ctx, &cabinet.SetPayload{WorkflowID: wf.ID(), SourceDigest: "git:abc", Payload: map[string]any{"input": map[string]any{"k": "v"}}})
	require.NoError(t, err)

	require.Len(t, sealed, 1)
	a := sealed[0]
	st := a.Steps[0]
	require.NotNil(t, st.Executed)
	assert.Equal(t, d64("12"), *st.Executed.EnvelopeDigest, "the executed identity is the admission record's envelope identity")
	assert.Equal(t, cabinet.TrustStateAuthenticated, st.Executed.TrustState)
	assert.Equal(t, 2, st.References.HostInteractions.Count)

	recs, ok := src.Interactions(st.References.HostInteractions.Digest)
	require.True(t, ok)
	asMaps := make([]map[string]any, len(recs))
	for i, r := range recs {
		b, _ := json.Marshal(r)
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber() // the independent verifier reads integers as json.Number only
		require.NoError(t, dec.Decode(&asMaps[i]))
	}
	doc, err := json.Marshal(a)
	require.NoError(t, err)
	res := o1verify.Verify(doc, o1verify.Options{Verifier: o1verify.VerifierInfo{ID: "o1verify", Version: "0"},
		HostInteractions: map[string][]map[string]any{st.References.HostInteractions.Digest: asMaps}})
	assert.Empty(t, res.Problems, "the independent verifier accepts the production artifact with exactly the archived records")
	assert.Equal(t, o1verify.VerdictVerified, res.EvidenceVerdict)

	// the same artifact with a record dropped is refused by the verifier
	res = o1verify.Verify(doc, o1verify.Options{Verifier: o1verify.VerifierInfo{ID: "o1verify", Version: "0"},
		HostInteractions: map[string][]map[string]any{st.References.HostInteractions.Digest: asMaps[:1]}})
	assert.NotEmpty(t, res.Problems, "a dropped host interaction does not verify")
	assert.NotEqual(t, o1verify.VerdictVerified, res.EvidenceVerdict)
}

func TestTraceV2OptionsAndSink(t *testing.T) {
	t.Setenv(traceV2DirEnv, "")
	opts, err := traceV2OptionsFromEnv(logrus.New(), nil)
	require.NoError(t, err)
	assert.Empty(t, opts, "unset: V2 is off")

	dir := filepath.Join(t.TempDir(), "v2")
	t.Setenv(traceV2DirEnv, dir)
	opts, err = traceV2OptionsFromEnv(logrus.New(), nil)
	require.NoError(t, err)
	assert.Len(t, opts, 1)
	_, err = os.Stat(dir)
	assert.NoError(t, err, "the directory is created")

	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	t.Setenv(traceV2DirEnv, filepath.Join(file, "sub"))
	_, err = traceV2OptionsFromEnv(logrus.New(), nil)
	assert.Error(t, err, "an unusable directory stops the start")

	// the sink writes the artifact and its interaction lists under the chain hash
	src := newTraceSource(newIdentityRegistry(), "prod", nil)
	sink := src.traceSink(dir, logrus.New())
	a := &cabinet.ProofArtifactV2{ChainHash: "sha256:" + strings.Repeat("cd", 32)}
	sink(context.Background(), a)
	for _, suffix := range []string{".json", ".interactions.json"} {
		fi, err := os.Stat(filepath.Join(dir, strings.Repeat("cd", 32)+suffix))
		require.NoError(t, err, suffix)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
}

// productionArtifact runs one step through the real cabinet with the production source and returns the sealed artifact, the
// interaction lists the verifier needs, and the source.
func productionArtifact(t *testing.T) (*cabinet.ProofArtifactV2, map[string][]map[string]any) {
	t.Helper()
	ctx := context.Background()
	reg := newIdentityRegistry()
	src := newTraceSource(reg, "prod", nil)
	var sealed []*cabinet.ProofArtifactV2
	mods := cabinet.NewMemModuleStore()
	mod, err := cabinet.NewModuleDescriptor(ctx, cabinet.BaseBlock{Name: "echo", Version: "1.0"}, "native", nil, nil, nil)
	require.NoError(t, err)
	mod.NativeImpl = cabinet.NativeFunctions{"echo": func(ctx context.Context, in interface{}) (interface{}, error) {
		l, _ := hostsign.LogFrom(ctx)
		_, _, _ = l.Append(rec("allowed"))
		return in, nil
	}}
	mods.Put(mod)
	reg.set(mod.ID(), mod.Checksum(), executedIdentityOf(strings.Repeat("ab", 32), mod, []byte("manifest"), d64("12"),
		&cabinet.ModuleProfileBinding{ProfileID: "cic:provider@0.1.0", ProfileDigest: d64("34")}))
	svc, err := cabinet.NewCabinetService(ctx, cabinet.NewMemSchemaStore(), mods, cabinet.NewMemWorkflowStore(),
		cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1},
		cabinet.WithTraceV2(src, func(_ context.Context, a *cabinet.ProofArtifactV2) { sealed = append(sealed, a) }))
	require.NoError(t, err)
	wf, err := cabinet.NewWorkflow(ctx, cabinet.WorkflowIdentity{Version: "1.0"}, cabinet.WorkflowConfig{Name: "wf.int10", Steps: []string{"echo@1.0.echo(input) -> out"}}, cabinet.WorkflowSigning{})
	require.NoError(t, err)
	require.NoError(t, svc.PutWorkflow(wf))
	_, err = svc.Setx(ctx, &cabinet.SetPayload{WorkflowID: wf.ID(), SourceDigest: "git:abc", Payload: map[string]any{"k": "v"}})
	require.NoError(t, err)
	require.Len(t, sealed, 1)
	lists := map[string][]map[string]any{}
	for _, st := range sealed[0].Steps {
		recs, _ := src.Interactions(st.References.HostInteractions.Digest)
		maps := make([]map[string]any, len(recs))
		for i, r := range recs {
			b, _ := json.Marshal(r)
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.UseNumber()
			require.NoError(t, dec.Decode(&maps[i]))
		}
		lists[st.References.HostInteractions.Digest] = maps
	}
	return sealed[0], lists
}

// INT-10: the persistence claim of a production artifact is believed only through a recorder key the bundle authorises, and
// the verifier's own result is attributable to a verifier key the bundle authorises. VERIFIED is not ACCEPTED.
func TestINT10_AttestedPersistenceAndAttributableVerifierResult(t *testing.T) {
	art, lists := productionArtifact(t)
	doc, err := json.Marshal(art)
	require.NoError(t, err)

	recPub, recPriv, _ := ed25519.GenerateKey(rand.Reader)
	recSPKI, _ := x509.MarshalPKIXPublicKey(recPub)
	recID := sha256Of(recSPKI)
	sig := ed25519.Sign(recPriv, attest.RecorderSignedBytes(art.ChainHash, recID, attest.AlgEd25519))
	persistence := &o1verify.Persistence{ArtifactChainHash: art.ChainHash, Persisted: true, Recorder: "relay", Commit: "abc123",
		Attestation: &o1verify.Attestation{Kind: "recorder-signature", SignerKeyID: recID, SignatureDigest: sha256Of(sig)}}
	material := func(d string) (attest.Material, bool) {
		if d == sha256Of(sig) {
			return attest.Material{Signature: sig, SPKI: recSPKI}, true
		}
		return attest.Material{}, false
	}
	verify := func(auth bool, p *o1verify.Persistence) o1verify.Result {
		return o1verify.Verify(doc, o1verify.Options{
			Verifier: o1verify.VerifierInfo{ID: "o1verify", Version: "1", ImplementationDigest: d64("ab")}, HostInteractions: lists, Persistence: p,
			Attestations: attest.RecorderVerifier{Authorities: map[string]bool{recID: auth}, Lookup: material},
			PolicyDigest: d64("cc"), PersistenceRequired: true})
	}

	res := verify(true, persistence)
	assert.Empty(t, res.Problems)
	assert.Equal(t, o1verify.EvidencePersisted, res.Dimensions.Evidence)
	assert.Equal(t, o1verify.VerdictVerified, res.EvidenceVerdict)
	assert.Equal(t, o1verify.AcceptanceNotAccepted, res.Acceptance,
		"VERIFIED evidence is not ACCEPTED execution: authorization was never evaluated for this step")

	res = verify(false, persistence)
	assert.NotEqual(t, o1verify.EvidencePersisted, res.Dimensions.Evidence, "a recorder key without recorder:attest does not make the artifact persisted")
	assert.NotEmpty(t, res.Problems)
	assert.Equal(t, o1verify.VerdictNotVerified, res.EvidenceVerdict)

	other := *persistence
	other.ArtifactChainHash = d64("99")
	res = verify(true, &other)
	assert.NotEmpty(t, res.Problems, "an attestation of another artifact does not count")

	// the verifier's own result: attributable only to an authorised verifier key
	vPub, vPriv, _ := ed25519.GenerateKey(rand.Reader)
	vSPKI, _ := x509.MarshalPKIXPublicKey(vPub)
	vID := sha256Of(vSPKI)
	good := verify(true, persistence)
	sr, err := attest.SignResult(good, vSPKI, func(m []byte) ([]byte, error) { return ed25519.Sign(vPriv, m), nil })
	require.NoError(t, err)
	attr, err := attest.VerifyResult(*sr, map[string]bool{vID: true}, nil)
	require.NoError(t, err)
	assert.False(t, attr.ImplementationPinned, "no pinned implementation digests: attributable, not pinned")
	attr, err = attest.VerifyResult(*sr, map[string]bool{vID: true}, map[string]bool{d64("ab"): true})
	require.NoError(t, err)
	assert.True(t, attr.ImplementationPinned)
	_, err = attest.VerifyResult(*sr, map[string]bool{recID: true}, nil)
	assert.Error(t, err, "the recorder's key is not the verifier's")
}
