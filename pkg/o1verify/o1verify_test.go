package o1verify

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vectorFile = "../../testdata/o1-vectors/chain-and-interaction-vectors.json"

type vectors struct {
	ChainCases []struct {
		ID             string          `json:"id"`
		Artifact       json.RawMessage `json:"artifact"`
		ExpectHash     string          `json:"expect_chain_hash"`
		ExpectInputB64 string          `json:"expect_hash_input_b64"`
		MustDifferFrom string          `json:"must_differ_from"`
	} `json:"chain_cases"`
	InteractionCases []struct {
		ID          string           `json:"id"`
		Records     []map[string]any `json:"records"`
		ExpectDig   string           `json:"expect_digest"`
		ExpectError string           `json:"expect_error"`
	} `json:"interaction_cases"`
}

func load(t *testing.T) vectors {
	t.Helper()
	b, err := os.ReadFile(vectorFile)
	require.NoError(t, err)
	var v vectors
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber() // the verifier accepts integers only as json.Number, never as float64
	require.NoError(t, dec.Decode(&v))
	return v
}

// The independent reading reproduces the shared vectors: the hash input byte for byte and the hash.
func TestChainInputAndHashMatchTheSharedVectors(t *testing.T) {
	v := load(t)
	require.Len(t, v.ChainCases, 3)
	hashes := map[string]string{}
	for _, c := range v.ChainCases {
		in, err := ChainInput(c.Artifact)
		require.NoError(t, err, c.ID)
		want, _ := base64.StdEncoding.DecodeString(c.ExpectInputB64)
		assert.Equal(t, string(want), string(in), c.ID)
		h, err := ChainHash(c.Artifact)
		require.NoError(t, err)
		assert.Equal(t, c.ExpectHash, h, c.ID)
		hashes[c.ID] = h
	}
	assert.NotEqual(t, hashes["chain-ok"], hashes["chain-executed-digest-changed"])
}

func TestInteractionsDigestMatchesTheSharedVectors(t *testing.T) {
	for _, c := range load(t).InteractionCases {
		d, err := InteractionsDigest(c.Records)
		if c.ExpectError != "" {
			require.Error(t, err, c.ID)
			continue
		}
		require.NoError(t, err, c.ID)
		assert.Equal(t, c.ExpectDig, d, c.ID)
	}
}

type fakeAtt struct{ err error }

func (f fakeAtt) VerifyAttestation(a Attestation, chain string) error { return f.err }

// okArtifact is the shared vectors' success artifact, made self-consistent: it references the real two-record
// host-interaction list of the vectors (digest and count) and carries the chain hash this package computes.
func okArtifact(t *testing.T) ([]byte, string, map[string][]map[string]any) {
	v := load(t)
	recs := map[string][]map[string]any{}
	var realDigest string
	var records []map[string]any
	for _, c := range v.InteractionCases {
		if c.ID == "interactions-two" {
			realDigest, records = c.ExpectDig, c.Records
			recs[c.ExpectDig] = c.Records
		}
	}
	require.NotEmpty(t, realDigest)
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(string(v.ChainCases[0].Artifact)))
	d.UseNumber()
	require.NoError(t, d.Decode(&m))
	hi := m["steps"].([]any)[0].(map[string]any)["references"].(map[string]any)["host_interactions"].(map[string]any)
	hi["digest"], hi["count"] = realDigest, len(records)
	m["chain_hash"] = ""
	doc, _ := json.Marshal(m)
	chain, err := ChainHash(doc)
	require.NoError(t, err)
	m["chain_hash"] = chain
	doc, _ = json.Marshal(m)
	return doc, chain, recs
}

func opts() Options {
	return Options{Verifier: VerifierInfo{ID: "o1verify", Version: "0.1.0", ImplementationDigest: "sha256:" + strings.Repeat("00", 32)}}
}

func TestVerifyReportsUnresolvedReferencesNotPass(t *testing.T) {
	doc, _, _ := okArtifact(t)
	r := Verify(doc, opts()) // no record lists supplied
	assert.Equal(t, VerificationIncomplete, r.Dimensions.Verification)
	assert.Equal(t, VerdictNotVerified, r.EvidenceVerdict)
	require.Len(t, r.UnresolvedReferences, 1)
	assert.True(t, strings.HasPrefix(r.UnresolvedReferences[0], "host_interactions:sha256:"))
	assert.Equal(t, AcceptanceNotEvaluated, r.Acceptance)
}

func persisted(chain string) *Persistence {
	return &Persistence{ArtifactChainHash: chain, Persisted: true, Recorder: "git-state-recorder", Commit: "abc123",
		Attestation: &Attestation{Kind: "git-signed-commit", SignerKeyID: "sha256:" + strings.Repeat("ef", 32), SignatureDigest: "sha256:" + strings.Repeat("cd", 32)}}
}

func TestVerifyCompleteAndAccepted(t *testing.T) {
	doc, chain, recs := okArtifact(t)
	o := opts()
	o.HostInteractions = recs
	o.Persistence, o.Attestations, o.PolicyDigest, o.PersistenceRequired = persisted(chain), fakeAtt{}, "sha256:"+strings.Repeat("11", 32), true
	r := Verify(doc, o)
	require.Empty(t, r.Problems)
	require.Empty(t, r.UnresolvedReferences)
	assert.Equal(t, Dimensions{Execution: "SUCCEEDED", Authorization: "ALLOWED", Evidence: EvidencePersisted, Verification: VerificationComplete}, r.Dimensions)
	assert.Equal(t, VerdictVerified, r.EvidenceVerdict)
	assert.Equal(t, AcceptanceAccepted, r.Acceptance)
}

// #186: a persistence failure is a fact about the evidence, not about the operation: the execution dimension stays
// SUCCEEDED, the evidence is still VERIFIED, and the acceptance is NOT_ACCEPTED.
func TestPersistenceFailureKeepsExecutionAndVerdictSeparate(t *testing.T) {
	doc, chain, recs := okArtifact(t)
	o := opts()
	o.HostInteractions = recs
	o.Persistence = &Persistence{ArtifactChainHash: chain, Persisted: false, Recorder: "git-state-recorder", ErrorCode: "recorder_unavailable"}
	o.PolicyDigest, o.PersistenceRequired = "sha256:"+strings.Repeat("11", 32), true
	r := Verify(doc, o)
	assert.Equal(t, "SUCCEEDED", r.Dimensions.Execution)
	assert.Equal(t, EvidenceNotPersisted, r.Dimensions.Evidence)
	assert.Equal(t, VerdictVerified, r.EvidenceVerdict)
	assert.Equal(t, AcceptanceNotAccepted, r.Acceptance)
}

func TestAttestationIsCheckedNotTrusted(t *testing.T) {
	doc, chain, recs := okArtifact(t)
	base := opts()
	base.HostInteractions = recs
	t.Run("no verifier: unresolved, not persisted", func(t *testing.T) {
		o := base
		o.Persistence = persisted(chain)
		r := Verify(doc, o)
		assert.Equal(t, VerificationIncomplete, r.Dimensions.Verification)
		assert.Contains(t, r.UnresolvedReferences, "persistence:attestation-unverifiable")
		assert.NotEqual(t, EvidencePersisted, r.Dimensions.Evidence)
	})
	t.Run("a failing attestation", func(t *testing.T) {
		o := base
		o.Persistence, o.Attestations = persisted(chain), fakeAtt{err: errors.New("bad signature")}
		r := Verify(doc, o)
		assert.Equal(t, VerificationFailed, r.Dimensions.Verification)
		assert.Contains(t, strings.Join(r.Problems, "|"), "PERSISTENCE_ATTESTATION_INVALID")
	})
	t.Run("persistence of another artifact", func(t *testing.T) {
		o := base
		o.Persistence, o.Attestations = persisted("sha256:"+strings.Repeat("99", 32)), fakeAtt{}
		r := Verify(doc, o)
		assert.Contains(t, strings.Join(r.Problems, "|"), "PERSISTENCE_NOT_BOUND_TO_THIS_ARTIFACT")
	})
	t.Run("persisted without an attestation", func(t *testing.T) {
		o := base
		p := persisted(chain)
		p.Attestation = nil
		o.Persistence, o.Attestations = p, fakeAtt{}
		assert.Contains(t, strings.Join(Verify(doc, o).Problems, "|"), "PERSISTED_WITHOUT_ATTESTATION")
	})
}

// Every refusal is for its named reason (the artifact is edited, and the hash is NOT resealed unless stated).
func TestVerifyRefusesForTheNamedReason(t *testing.T) {
	doc, _, recs := okArtifact(t)
	edit := func(f func(m map[string]any)) []byte {
		var m map[string]any
		d := json.NewDecoder(strings.NewReader(string(doc)))
		d.UseNumber()
		require.NoError(t, d.Decode(&m))
		f(m)
		b, _ := json.Marshal(m)
		return b
	}
	step := func(m map[string]any) map[string]any { return m["steps"].([]any)[0].(map[string]any) }
	cases := []struct {
		name string
		doc  []byte
		want string
	}{
		{"edited digest, hash not resealed", edit(func(m map[string]any) {
			step(m)["executed"].(map[string]any)["executed_digest"] = "sha256:" + strings.Repeat("77", 32)
		}), "CHAIN_HASH_MISMATCH"},
		{"V1 domain", edit(func(m map[string]any) { m["version"], m["chain_version"] = "1.0", "CICCHAINv1" }), "WRONG_DOMAIN"},
		{"rejected claims an executed identity", edit(func(m map[string]any) { step(m)["outcome"].(map[string]any)["execution"] = "REJECTED" }), "REJECTED_WITH_EXECUTED"},
		{"rejected without a rejection record", edit(func(m map[string]any) {
			step(m)["outcome"].(map[string]any)["execution"] = "REJECTED"
			delete(step(m), "executed")
		}), "REJECTED_WITHOUT_REJECTION"},
		{"authenticated without an envelope digest", edit(func(m map[string]any) { step(m)["executed"].(map[string]any)["envelope_digest"] = nil }), "AUTHENTICATED_WITHOUT_ENVELOPE_DIGEST"},
		{"allowed without a decision reference", edit(func(m map[string]any) { delete(step(m)["references"].(map[string]any), "authorization_decision") }), "AUTHORIZATION_DECISION_REFERENCE_REQUIRED"},
		{"succeeded without an executed identity", edit(func(m map[string]any) { delete(step(m), "executed") }), "EXECUTED_REQUIRED"},
		{"a value with a line break in the hash input", edit(func(m map[string]any) { m["workflow"].(map[string]any)["id"] = "a\nb" }), "HASH_INPUT_UNREPRESENTABLE"},
		{"not JSON", []byte("nope"), "ARTIFACT_UNREADABLE"},
		{"trailing data", append(append([]byte(nil), doc...), []byte(" {}")...), "ARTIFACT_UNREADABLE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := opts()
			o.HostInteractions = recs
			r := Verify(c.doc, o)
			assert.Equal(t, VerificationFailed, r.Dimensions.Verification)
			assert.Equal(t, VerdictNotVerified, r.EvidenceVerdict)
			assert.Contains(t, strings.Join(r.Problems, "|"), c.want)
		})
	}
}

func TestHostInteractionRecordsAreRecomputedNotTrusted(t *testing.T) {
	doc, _, recs := okArtifact(t)
	for dig, rs := range recs {
		t.Run("a record is altered", func(t *testing.T) {
			altered := []map[string]any{}
			b, _ := json.Marshal(rs)
			d := json.NewDecoder(strings.NewReader(string(b)))
			d.UseNumber()
			require.NoError(t, d.Decode(&altered))
			altered[1]["code"] = "POLICY_HOST"
			o := opts()
			o.HostInteractions = map[string][]map[string]any{dig: altered}
			assert.Contains(t, strings.Join(Verify(doc, o).Problems, "|"), "HOST_INTERACTIONS_DIGEST_MISMATCH")
		})
		t.Run("the denied record is dropped", func(t *testing.T) {
			o := opts()
			o.HostInteractions = map[string][]map[string]any{dig: rs[:1]}
			assert.Contains(t, strings.Join(Verify(doc, o).Problems, "|"), "HOST_INTERACTIONS_DIGEST_MISMATCH")
		})
	}
}

func TestResultJSONShape(t *testing.T) {
	doc, _, recs := okArtifact(t)
	o := opts()
	o.HostInteractions = recs
	b, err := json.Marshal(Verify(doc, o))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	for _, k := range []string{"kind", "artifact_version", "verifier", "subject", "dimensions", "evidence_verdict", "acceptance"} {
		assert.Contains(t, m, k)
	}
}
