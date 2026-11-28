package main

// O-1 end to end (INT-14): a real WASM module is executed through the real cabinet under an authenticated principal, a pinned
// realization context, an authenticated ABI-profile binding, the operator's effect policy and the pinned schema bodies; the
// production source records the evidence, the recorder persists and attests it, and the independent verifier, with its own
// authorised identity, checks all of it. Then one negative per identity that can be wrong.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/o1verify"
	"centralrelay/pkg/o1verify/attest"
	"centralrelay/pkg/realization"
)

// responseWasm is a minimal guest with the cic ABI whose Call returns the fixed JSON document `response` (read from linear
// memory at 0); allocate hands out a high fixed address so the response is not overwritten by the inputs.
func responseWasm(response string) []byte {
	code := func(body ...byte) []byte { return append(uleb(len(body)), body...) }
	packed := len(response) << 32
	callBody := append(append([]byte{0x00, 0x42}, sleb(packed)...), 0x0b)
	m := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	m = append(m, section(1, vec([]byte{0x60, 1, 0x7e, 1, 0x7e}, []byte{0x60, 2, 0x7e, 0x7e, 0}, []byte{0x60, 6, 0x7e, 0x7e, 0x7e, 0x7e, 0x7e, 0x7e, 1, 0x7e}))...)
	m = append(m, section(3, []byte{3, 0, 1, 2})...)
	m = append(m, section(5, []byte{1, 0x00, 0x01})...)
	m = append(m, section(7, vec(append(name("memory"), 0x02, 0), append(name("allocate"), 0x00, 0), append(name("deallocate"), 0x00, 1), append(name("Call"), 0x00, 2)))...)
	m = append(m, section(10, vec(code(append(append([]byte{0x00, 0x42}, sleb(4096)...), 0x0b)...), code(0x00, 0x0b), code(callBody...)))...)
	m = append(m, section(11, vec(append(append([]byte{0x00, 0x41, 0x00, 0x0b}, uleb(len(response))...), response...)))...)
	return m
}

type e2eKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	spki []byte
	id   string
}

func newE2EKey(t *testing.T) e2eKey {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	spki, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	return e2eKey{pub, priv, spki, sha256Of(spki)}
}

// e2eRun is one execution and the files the recorder wrote for it.
type e2eRun struct {
	dir   string
	art   *cabinet.ProofArtifactV2
	rec   e2eKey
	chain string
}

const realizationDoc = `{"kind":"CICRealizationContext","version":1,"context_id":"oci-fra-obs","provider":"oci","region":"eu-frankfurt-1",` +
	`"host":"iaas.eu-frankfurt-1.oraclecloud.com","base_path":"/20160918","scope_id":"ocid1.compartment.oc1..aaaa"}`

// runE2E executes one describe step. withPrincipal and withRealization switch those inputs off for the negative runs; wrap
// lets a negative run replace the trace source by one that lies about one thing.
func runE2E(t *testing.T, withPrincipal, withRealization bool, wrap func(*traceSource) cabinet.TraceV2Source) *e2eRun {
	t.Helper()
	ctx := context.Background()
	profileOperator = profileFixtureOperator()
	admittedRealization = nil
	if withRealization {
		a, err := realization.Admit([]byte(realizationDoc), realization.Digest([]byte(realizationDoc)))
		require.NoError(t, err)
		admittedRealization = a
	}
	t.Cleanup(func() { profileOperator, admittedRealization = nil, nil })

	raw := profileJSON(t, nil)
	profile, err := cabinet.ParseProfile(raw)
	require.NoError(t, err)
	mod, err := cabinet.NewModuleDescriptor(ctx, cabinet.BaseBlock{Name: "e2e.mod", Version: "1.0"}, "wasm", nil, nil, responseWasm(`{"data":{"status":"ok"},"error":null}`))
	require.NoError(t, err)
	envDigest := d64("12")
	binding, err := cabinet.NewModuleProfileBinding(mod, envDigest, profile, cabinet.ProfileDigest(raw), cabinet.PinnedProfile{ID: profile.ProfileID, Digest: cabinet.ProfileDigest(raw)}, []string{"describe"})
	require.NoError(t, err)

	reg := newIdentityRegistry()
	reg.set(mod.ID(), mod.Checksum(), executedIdentityOf(strings.Repeat("ab", 32), mod, []byte("manifest"), envDigest, binding))
	src := newTraceSource(reg, "prod", nil)
	var source cabinet.TraceV2Source = src
	if wrap != nil {
		source = wrap(src)
	}

	run := &e2eRun{dir: t.TempDir(), rec: newE2EKey(t)}
	rk := &recorderKey{priv: run.rec.priv, spki: run.rec.spki, id: run.rec.id}
	sink := src.traceSink(run.dir, rk, logrus.New())
	mods := cabinet.NewMemModuleStore()
	mods.Put(mod)
	svc, err := cabinet.NewCabinetService(ctx, cabinet.NewMemSchemaStore(), mods, cabinet.NewMemWorkflowStore(),
		cabinet.WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 5},
		cabinet.WithGenericDispatch(true), cabinet.WithEffectPolicy(cabinet.EffectLocal, cabinet.EffectExternalRead), cabinet.WithPayloadValidator(operatorValidator{}),
		cabinet.WithRealizationContext(realizationDigest),
		cabinet.WithTraceV2(source, func(c context.Context, a *cabinet.ProofArtifactV2) { run.art = a; sink(c, a) }))
	require.NoError(t, err)
	t.Cleanup(func() { svc.Close(ctx) })
	svc.(interface {
		SetModuleProfile(string, *cabinet.ModuleProfileBinding)
	}).SetModuleProfile(mod.ID(), binding)

	wf, err := cabinet.NewWorkflow(ctx, cabinet.WorkflowIdentity{Version: "1.0"}, cabinet.WorkflowConfig{Name: "wf.e2e.o1", Steps: []string{mod.ID() + ".describe(input)"}}, cabinet.WorkflowSigning{})
	require.NoError(t, err)
	require.NoError(t, svc.PutWorkflow(wf))
	callCtx := ctx
	if withPrincipal {
		callCtx = cabinet.WithPrincipal(ctx, cabinet.Principal{ID: "observer-1", Method: "bearer", Type: cabinet.PrincipalTypeService})
	}
	_, err = svc.Setx(callCtx, &cabinet.SetPayload{WorkflowID: wf.ID(), SourceDigest: "git:abc", Payload: map[string]any{"k": "v"}})
	require.NoError(t, err)
	require.NotNil(t, run.art)
	run.chain = run.art.ChainHash
	return run
}

// inputs are everything the verifier is given; a negative run changes exactly one.
type inputs struct {
	doc          []byte
	interactions map[string][]map[string]any
	decisions    map[string]map[string]any
	contexts     map[string]map[string]any
	persistence  *o1verify.Persistence
	recorderAuth map[string]bool
	material     attest.Material
}

func readJSONFile(t *testing.T, path string, into any) {
	data, err := os.ReadFile(path)
	require.NoError(t, err, path)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	require.NoError(t, dec.Decode(into), path)
}

func (r *e2eRun) load(t *testing.T) *inputs {
	name := strings.TrimPrefix(r.chain, "sha256:")
	in := &inputs{interactions: map[string][]map[string]any{}, decisions: map[string]map[string]any{}, contexts: map[string]map[string]any{}, recorderAuth: map[string]bool{r.rec.id: true}}
	var err error
	in.doc, err = os.ReadFile(filepath.Join(r.dir, name+".json"))
	require.NoError(t, err)
	readJSONFile(t, filepath.Join(r.dir, name+".interactions.json"), &in.interactions)
	readJSONFile(t, filepath.Join(r.dir, name+".decisions.json"), &in.decisions)
	readJSONFile(t, filepath.Join(r.dir, name+".contexts.json"), &in.contexts)
	var p o1verify.Persistence
	readJSONFile(t, filepath.Join(r.dir, name+".persistence.json"), &p)
	in.persistence = &p
	var m struct {
		Sig  string `json:"signature_b64"`
		SPKI string `json:"spki_b64"`
	}
	readJSONFile(t, filepath.Join(r.dir, name+".attestation.json"), &m)
	in.material.Signature, _ = base64.StdEncoding.DecodeString(m.Sig)
	in.material.SPKI, _ = base64.StdEncoding.DecodeString(m.SPKI)
	if emptyDigest, err := o1verify.InteractionsDigest(nil); err == nil {
		if _, ok := in.interactions[emptyDigest]; !ok {
			in.interactions[emptyDigest] = []map[string]any{}
		}
	}
	return in
}

func (in *inputs) verify() o1verify.Result {
	return o1verify.Verify(in.doc, o1verify.Options{
		Verifier:         o1verify.VerifierInfo{ID: "o1verify", Version: "1", ImplementationDigest: d64("ab")},
		HostInteractions: in.interactions, AuthorizationDecisions: in.decisions, ExecutionContexts: in.contexts, Persistence: in.persistence,
		Attestations: attest.RecorderVerifier{Authorities: in.recorderAuth, Lookup: func(string) (attest.Material, bool) { return in.material, true }},
		PolicyDigest: d64("cc"), PersistenceRequired: true})
}

func problems(res o1verify.Result) string { return strings.Join(res.Problems, " | ") }

func TestO1E2E_PositiveAndEveryIdentityNegative(t *testing.T) {
	good := runE2E(t, true, true, nil)
	in := good.load(t)
	res := in.verify()
	require.Empty(t, res.Problems, "the full chain verifies")
	assert.Equal(t, o1verify.VerificationComplete, res.Dimensions.Verification)
	assert.Equal(t, "SUCCEEDED", res.Dimensions.Execution)
	assert.Equal(t, "ALLOWED", res.Dimensions.Authorization)
	assert.Equal(t, o1verify.EvidencePersisted, res.Dimensions.Evidence)
	assert.Equal(t, o1verify.VerdictVerified, res.EvidenceVerdict)
	assert.Equal(t, o1verify.AcceptanceAccepted, res.Acceptance, "a verified, allowed, persisted, successful execution under a policy is accepted")

	// the decision the artifact references is bound to everything it is about
	dd := good.art.Steps[0].References.AuthorizationDecision.Digest
	rec := in.decisions[dd]
	assert.Equal(t, "service:observer-1/bearer", rec["principal"])
	assert.Equal(t, realization.Digest([]byte(realizationDoc)), rec["realization_context_digest"])
	assert.Equal(t, *good.art.Steps[0].Executed.EnvelopeDigest, rec["envelope_digest"])
	assert.Equal(t, good.art.Steps[0].InputDigest, rec["input_digest"])

	// the verifier's own identity: its key holds verifier:attest, and the signed result is attributable to it
	ver := newE2EKey(t)
	sr, err := attest.SignResult(res, ver.spki, func(m []byte) ([]byte, error) { return ed25519.Sign(ver.priv, m), nil })
	require.NoError(t, err)
	_, err = attest.VerifyResult(*sr, map[string]bool{ver.id: true}, nil)
	require.NoError(t, err)

	t.Run("artifact identity: a changed artifact", func(t *testing.T) {
		in := good.load(t)
		in.doc = bytes.Replace(in.doc, []byte("git:abc"), []byte("git:abd"), 1)
		res := in.verify()
		assert.Contains(t, problems(res), "CHAIN_HASH_MISMATCH")
		assert.Equal(t, o1verify.VerdictNotVerified, res.EvidenceVerdict)
	})
	t.Run("authorization: the record is missing", func(t *testing.T) {
		in := good.load(t)
		in.decisions = map[string]map[string]any{}
		res := in.verify()
		assert.Equal(t, o1verify.VerificationIncomplete, res.Dimensions.Verification, "a digest without its record is unresolved, not passed")
		assert.Equal(t, o1verify.VerdictNotVerified, res.EvidenceVerdict)
	})
	t.Run("authorization: the record was altered", func(t *testing.T) {
		in := good.load(t)
		in.decisions[dd]["effect"] = "external-write"
		assert.Contains(t, problems(in.verify()), "AUTHORIZATION_DECISION_DIGEST_MISMATCH")
	})
	t.Run("authorization: a record that was re-digested to hide an alteration is not the referenced one", func(t *testing.T) {
		in := good.load(t)
		altered := map[string]any{}
		for k, v := range in.decisions[dd] {
			altered[k] = v
		}
		altered["operation"] = "destroy"
		in.decisions[dd] = altered
		assert.Contains(t, problems(in.verify()), "AUTHORIZATION_DECISION_DIGEST_MISMATCH")
	})
	t.Run("context: the context record was altered", func(t *testing.T) {
		in := good.load(t)
		cd := good.art.Steps[0].References.ExecutionContext.Digest
		in.contexts[cd]["principal"] = "service:someone-else/bearer"
		assert.Contains(t, problems(in.verify()), "EXECUTION_CONTEXT_DIGEST_MISMATCH")
	})
	t.Run("host interaction: a list that is not the referenced one", func(t *testing.T) {
		in := good.load(t)
		empty, _ := o1verify.InteractionsDigest(nil)
		in.interactions[empty] = []map[string]any{{"interaction_id": "i-0001", "seq": json.Number("0")}}
		assert.NotEmpty(t, in.verify().Problems)
	})
	t.Run("persistence: attested by a key the bundle does not authorise", func(t *testing.T) {
		in := good.load(t)
		in.recorderAuth = map[string]bool{good.rec.id: false}
		res := in.verify()
		assert.Contains(t, problems(res), "PERSISTENCE_ATTESTATION_INVALID")
		assert.NotEqual(t, o1verify.EvidencePersisted, res.Dimensions.Evidence)
	})
	t.Run("persistence: the attestation signs another artifact", func(t *testing.T) {
		in := good.load(t)
		other := newE2EKey(t)
		sig := ed25519.Sign(other.priv, attest.RecorderSignedBytes(d64("99"), other.id, attest.AlgEd25519))
		in.material = attest.Material{Signature: sig, SPKI: other.spki}
		in.persistence.Attestation = &o1verify.Attestation{Kind: "recorder-signature", SignerKeyID: other.id, SignatureDigest: sha256Of(sig)}
		in.recorderAuth = map[string]bool{other.id: true}
		assert.Contains(t, problems(in.verify()), "PERSISTENCE_ATTESTATION_INVALID")
	})
	t.Run("verifier identity: a key without verifier:attest", func(t *testing.T) {
		_, err := attest.VerifyResult(*sr, map[string]bool{ver.id: false}, nil)
		assert.Error(t, err)
		_, err = attest.VerifyResult(*sr, map[string]bool{newE2EKey(t).id: true}, nil)
		assert.Error(t, err)
	})

	// the negatives that need a producer that lies about exactly one thing: each is a self-consistent artifact whose
	// decision does not match what it says about the step
	lies := map[string]struct {
		wrap func(*traceSource) cabinet.TraceV2Source
		want string
	}{
		"module envelope: the executed identity is another envelope than the decision's": {
			func(s *traceSource) cabinet.TraceV2Source { return &lyingSource{traceSource: s, envelope: d64("77")} }, "AUTHORIZATION_BINDING_MISMATCH"},
		"principal: the context names another principal than the decision": {
			func(s *traceSource) cabinet.TraceV2Source {
				return &lyingSource{traceSource: s, principal: "service:impostor/bearer"}
			}, "AUTHORIZATION_PRINCIPAL_MISMATCH"},
		"realization context: the context names another one than the decision": {
			func(s *traceSource) cabinet.TraceV2Source {
				return &lyingSource{traceSource: s, realization: d64("55")}
			}, "AUTHORIZATION_REALIZATION_MISMATCH"},
	}
	for name, l := range lies {
		t.Run(name, func(t *testing.T) {
			run := runE2E(t, true, true, l.wrap)
			res := run.load(t).verify()
			assert.Contains(t, problems(res), l.want)
			assert.Equal(t, o1verify.VerdictNotVerified, res.EvidenceVerdict)
			assert.NotEqual(t, o1verify.AcceptanceAccepted, res.Acceptance)
		})
	}

	t.Run("principal: an allowed decision without an authenticated principal", func(t *testing.T) {
		run := runE2E(t, false, true, nil)
		res := run.load(t).verify()
		assert.Contains(t, problems(res), "AUTHORIZATION_WITHOUT_PRINCIPAL")
		assert.NotEqual(t, o1verify.AcceptanceAccepted, res.Acceptance)
	})
	t.Run("realization context: an allowed decision without one", func(t *testing.T) {
		run := runE2E(t, true, false, nil)
		res := run.load(t).verify()
		assert.Contains(t, problems(res), "AUTHORIZATION_UNBOUND_FIELD")
		assert.NotEqual(t, o1verify.AcceptanceAccepted, res.Acceptance)
	})
}

// lyingSource is a production source that says one thing differently from the decision the cabinet recorded.
type lyingSource struct {
	*traceSource
	envelope, principal, realization string
}

func (l *lyingSource) ExecutedIdentity(m *cabinet.ModuleDescriptor, dispatched string) (cabinet.ExecutedIdentityV2, bool) {
	id, ok := l.traceSource.ExecutedIdentity(m, dispatched)
	if ok && l.envelope != "" {
		e := l.envelope
		id.EnvelopeDigest = &e
	}
	return id, ok
}

func (l *lyingSource) ExecutionContext(ctx context.Context, workflowID string) string {
	p, ok := cabinet.PrincipalFrom(ctx)
	f := map[string]string{"workflow_id": workflowID, "trust_mode": "prod", "principal": cabinet.PrincipalString(p, ok), "realization_context_digest": realizationDigest()}
	if l.principal != "" {
		f["principal"] = l.principal
	}
	if l.realization != "" {
		f["realization_context_digest"] = l.realization
	}
	return l.putContext(f)
}

// TestDumpO1Fixtures writes the artifact and its records of one positive run to $CIC_DUMP_O1_FIXTURES (a maintenance helper:
// pkg/o1verify's authorization tests are built on this real artifact). It does nothing when the variable is unset.
func TestDumpO1Fixtures(t *testing.T) {
	dir := os.Getenv("CIC_DUMP_O1_FIXTURES")
	if dir == "" {
		t.Skip("CIC_DUMP_O1_FIXTURES is not set")
	}
	run := runE2E(t, true, true, nil)
	in := run.load(t)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, v := range map[string]any{"decisions.json": in.decisions, "contexts.json": in.contexts} {
		b, err := json.MarshalIndent(v, "", " ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "artifact.json"), in.doc, 0o644))
}
