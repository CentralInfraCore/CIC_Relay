package main

// The Intent -> Observed -> Conformance/Drift -> Evidence slice for one OCI VCN, as an execution-free, fixture-driven test
// (features/feature-011-oci-provider/vcn-slice.md). Nothing here calls OCI, signs, reads Vault or runs a workflow: it walks the
// stage artifacts of the common fixture (testdata/vcn-v1, vendored from cic-factory), builds the evidence out of the EXISTING
// ProofArtifact / cabinet.ProofTraceStep / ComputeChainHashV1 types, checks it with the EXISTING VerifyProofArtifact, and compares
// the Relay's own iac.Evaluate with the fixture's expected verdicts.
//
// Status of what this proves: the evidence shape and the claim rules are implemented as a TEST only (scaffold: no production code
// composes registry + module + engine at runtime); iac.Evaluate is implemented.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"centralrelay/core/cabinet"
	"centralrelay/core/nexus/iac"
	"centralrelay/pkg/canonicaljson"
)

const vcnFixtureDir = "testdata/vcn-v1"

// vcnInjectedTime is the timestamp every evidence record of this test carries: ProofTrace takes its clock from the caller.
const vcnInjectedTime = "2026-10-21T00:00:00Z"

func vcnRead(t testing.TB, rel ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{vcnFixtureDir}, rel...)...))
	if err != nil {
		t.Fatalf("read fixture %v: %v", rel, err)
	}
	return b
}

func vcnJSON(t testing.TB, rel ...string) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal(vcnRead(t, rel...), &v); err != nil {
		t.Fatalf("parse fixture %v: %v", rel, err)
	}
	return v
}

func vcnObj(t testing.TB, rel ...string) map[string]interface{} {
	t.Helper()
	m, ok := vcnJSON(t, rel...).(map[string]interface{})
	if !ok {
		t.Fatalf("fixture %v is not an object", rel)
	}
	return m
}

// vcnHash is the hex SHA-256 of the canonical JSON of a value: the hash of a stage artifact, independent of its formatting.
func vcnHash(t testing.TB, v interface{}) string {
	t.Helper()
	b, err := canonicaljson.ToJSON(v)
	if err != nil {
		t.Fatalf("canonical json: %v", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func vcnCases(t testing.TB) []string {
	t.Helper()
	var ids []string
	for id := range vcnObj(t, "cases.json") {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// vcnSidecar is what the evidence says about the case in words a reader can check, bound to the chain by the hash in the last
// step. ProofArtifact has only hashes; this is the human-readable part. It is a test-side struct, not a Relay schema.
type vcnSidecar struct {
	Case               string            `json:"case"`
	Object             string            `json:"object"`
	ConformanceClaimed bool              `json:"conformance_claimed"`
	DriftPaths         []string          `json:"drift_paths"`
	UnobservedPaths    []string          `json:"unobserved_paths"`
	Coverage           map[string]string `json:"coverage"`
	Verdicts           map[string]string `json:"verdicts"`
}

// vcnAggregate restates the precedence DRIFT/NOT_COMPARABLE > UNOBSERVED > CONFORMANT, so that a stored object verdict is never
// taken on trust.
func vcnAggregate(fields map[string]string) string {
	obj := "CONFORMANT"
	for _, v := range fields {
		switch v {
		case "DRIFT", "NOT_COMPARABLE":
			return "DRIFT"
		case "UNOBSERVED":
			obj = "INCOMPLETE"
		}
	}
	return obj
}

// vcnClaims derives the sidecar from the stage-4 observation and the stage-5 verdict, and REFUSES to claim conformance for
// anything that was not observed. The rule: conformance may be claimed only if the object verdict, recomputed from the field
// verdicts, is CONFORMANT, and no compared path has an unobserved or unknown coverage.
func vcnClaims(id string, coverage map[string]string, verdict map[string]interface{}) (vcnSidecar, error) {
	fields := map[string]string{}
	for p, v := range verdict["fields"].(map[string]interface{}) {
		fields[p] = v.(string)
	}
	if got, want := vcnAggregate(fields), verdict["object"].(string); got != want {
		return vcnSidecar{}, fmt.Errorf("%s: stored object verdict %q, recomputed %q", id, want, got)
	}
	sc := vcnSidecar{Case: id, Object: verdict["object"].(string), Coverage: coverage, Verdicts: fields, DriftPaths: []string{}, UnobservedPaths: []string{}}
	for p, v := range fields {
		cov, ok := coverage[p]
		if !ok {
			return vcnSidecar{}, fmt.Errorf("%s: no coverage recorded for compared path %s", id, p)
		}
		switch v {
		case "DRIFT", "NOT_COMPARABLE":
			sc.DriftPaths = append(sc.DriftPaths, p)
		case "UNOBSERVED":
			sc.UnobservedPaths = append(sc.UnobservedPaths, p)
		}
		if v == "CONFORMANT" && cov != "observed" {
			return vcnSidecar{}, fmt.Errorf("%s: %s is CONFORMANT but its coverage is %q", id, p, cov)
		}
		if (cov == "unobserved" || cov == "unknown") && v != "UNOBSERVED" {
			return vcnSidecar{}, fmt.Errorf("%s: %s has coverage %q but verdict %q", id, p, cov, v)
		}
	}
	sort.Strings(sc.DriftPaths)
	sort.Strings(sc.UnobservedPaths)
	sc.ConformanceClaimed = sc.Object == "CONFORMANT"
	return sc, nil
}

// vcnEvidence builds the evidence record of one case: one chain step per stage, hashes of the canonical stage artifacts.
func vcnEvidence(t testing.TB, id string) (*ProofArtifact, vcnSidecar, error) {
	t.Helper()
	raw := "response.json"
	if _, err := os.Stat(filepath.Join(vcnFixtureDir, "cases", id, "oci", raw)); err != nil {
		raw = "error.json"
	}
	rawDoc := vcnJSON(t, "cases", id, "oci", raw)
	intent := vcnJSON(t, "cases", id, "intent.registry.json")
	obsOCI := vcnJSON(t, "cases", id, "expected", "observed.oci.json")
	obsReg := vcnObj(t, "cases", id, "expected", "observed.registry.json")
	verdict := vcnObj(t, "cases", id, "expected", "verdict.fields.json")
	return vcnEvidenceFrom(t, id, rawDoc, intent, obsOCI, obsReg, verdict)
}

func vcnEvidenceFrom(t testing.TB, id string, rawDoc, intent, obsOCI interface{}, obsReg, verdict map[string]interface{}) (*ProofArtifact, vcnSidecar, error) {
	t.Helper()
	coverage := map[string]string{}
	for p, v := range obsReg["coverage"].(map[string]interface{}) {
		coverage[p] = v.(string)
	}
	sc, err := vcnClaims(id, coverage, verdict)
	if err != nil {
		return nil, vcnSidecar{}, err
	}
	conformanceIn := map[string]interface{}{"intent": intent, "observed": obsReg["config"], "coverage": obsReg["coverage"], "plan": vcnJSON(t, "plan.json")}
	scDoc := vcnToDoc(t, sc)
	steps := []ProofArtifactStep{
		{Name: "normalize-oci", Module: "vcn-slice/fixture", InputHash: vcnHash(t, rawDoc), OutputHash: vcnHash(t, obsOCI)},
		{Name: "bind-registry", Module: "vcn-slice/fixture", InputHash: vcnHash(t, obsOCI), OutputHash: vcnHash(t, obsReg)},
		{Name: "conformance", Module: "vcn-slice/fixture", InputHash: vcnHash(t, conformanceIn), OutputHash: vcnHash(t, verdict)},
		{Name: "claims", Module: "vcn-slice/fixture", InputHash: vcnHash(t, verdict), OutputHash: vcnHash(t, scDoc)},
	}
	manifest := sha256.Sum256(vcnRead(t, "MANIFEST.sha256"))
	source := hex.EncodeToString(manifest[:])
	cs := make([]cabinet.ProofTraceStep, len(steps))
	for i, s := range steps {
		cs[i] = cabinet.ProofTraceStep{Name: s.Name, Module: s.Module, InputHash: s.InputHash, OutputHash: s.OutputHash}
	}
	wf := "vcn-slice/" + id
	// PoSEResult stays unset on purpose: pose_result (VERIFIED|DRIFT|SKIPPED) is about the proof-of-state-existence check, not
	// about conformance; filling it with the conformance object would conflate two different "drift"s.
	return &ProofArtifact{WorkflowID: wf, SourceDigest: source, ChainHash: cabinet.ComputeChainHashV1(wf, source, cs), Steps: steps, Timestamp: vcnInjectedTime}, sc, nil
}

func vcnToDoc(t testing.TB, v interface{}) interface{} {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// vcnSidecarBound reports whether the sidecar is the one the last step of the chain was made over.
func vcnSidecarBound(t testing.TB, a *ProofArtifact, sc vcnSidecar) bool {
	t.Helper()
	return a.Steps[len(a.Steps)-1].OutputHash == vcnHash(t, vcnToDoc(t, sc))
}

func TestVCNSlice_FixtureIsIntact(t *testing.T) {
	rows := strings.Split(strings.TrimSpace(string(vcnRead(t, "MANIFEST.sha256"))), "\n")
	if len(rows) < 50 {
		t.Fatalf("manifest has %d rows", len(rows))
	}
	for _, r := range rows {
		digest, name, ok := strings.Cut(r, "  ")
		if !ok {
			t.Fatalf("bad manifest row %q", r)
		}
		sum := sha256.Sum256(vcnRead(t, filepath.FromSlash(name)))
		if hex.EncodeToString(sum[:]) != digest {
			t.Errorf("%s: fixture file changed since it was vendored", name)
		}
	}
	if got := len(vcnCases(t)); got != 9 {
		t.Errorf("%d cases, want 9", got)
	}
}

func TestVCNSlice_EvidenceChainVerifiesAndMatchesTheExpectedClaims(t *testing.T) {
	for _, id := range vcnCases(t) {
		t.Run(id, func(t *testing.T) {
			a, sc, err := vcnEvidence(t, id)
			if err != nil {
				t.Fatalf("evidence: %v", err)
			}
			res := VerifyProofArtifact(a)
			if !res.Valid || len(res.Errors) != 0 || len(res.Warnings) != 0 {
				t.Fatalf("VerifyProofArtifact: %+v", res)
			}
			if !vcnSidecarBound(t, a, sc) {
				t.Fatal("the sidecar is not the one the chain was made over")
			}
			want := vcnObj(t, "cases", id, "expected", "evidence.claims.json")
			if sc.ConformanceClaimed != want["conformance_claimed"].(bool) || sc.Object != want["object"].(string) {
				t.Errorf("claim %v/%s, expected %v/%s", sc.ConformanceClaimed, sc.Object, want["conformance_claimed"], want["object"])
			}
			for key, got := range map[string][]string{"drift_paths": sc.DriftPaths, "unobserved_paths": sc.UnobservedPaths} {
				var w []string
				for _, p := range want[key].([]interface{}) {
					w = append(w, p.(string))
				}
				if strings.Join(got, "|") != strings.Join(w, "|") {
					t.Errorf("%s = %v, expected %v", key, got, w)
				}
			}
			// the same inputs give the same chain hash: nothing in the record depends on the clock or on map order
			a2, _, _ := vcnEvidence(t, id)
			if a2.ChainHash != a.ChainHash {
				t.Errorf("chain hash not deterministic: %s vs %s", a.ChainHash, a2.ChainHash)
			}
		})
	}
}

func TestVCNSlice_ConformanceIsClaimedOnlyWhenEverythingWasObserved(t *testing.T) {
	claimed := map[string]bool{}
	for _, id := range vcnCases(t) {
		_, sc, err := vcnEvidence(t, id)
		if err != nil {
			t.Fatal(err)
		}
		claimed[id] = sc.ConformanceClaimed
		if sc.ConformanceClaimed {
			for p, cov := range sc.Coverage {
				if cov == "unobserved" || cov == "unknown" {
					t.Errorf("%s claims conformance with %s %s", id, p, cov)
				}
			}
			if len(sc.DriftPaths)+len(sc.UnobservedPaths) != 0 {
				t.Errorf("%s claims conformance with drift or unobserved paths", id)
			}
		}
	}
	want := map[string]bool{"conformant": true, "desired-absent": true, "drift": false, "field-absent": false, "desired-absent-observed-present": false,
		"partial-read": false, "malformed-cidr": false, "not-found": false, "transport-error": false}
	for id, w := range want {
		if claimed[id] != w {
			t.Errorf("%s: conformance_claimed = %v, want %v", id, claimed[id], w)
		}
	}
}

func TestVCNSlice_UnknownAndUnobservedAreExplicitInTheEvidence(t *testing.T) {
	cov := func(id string) map[string]string {
		_, sc, err := vcnEvidence(t, id)
		if err != nil {
			t.Fatal(err)
		}
		return sc.Coverage
	}
	for p, c := range cov("not-found") { // HTTP 404 NotAuthorizedOrNotFound: deleted and forbidden look the same, so absence is not authoritative
		if c != "unknown" {
			t.Errorf("not-found %s = %q, want unknown", p, c)
		}
	}
	for p, c := range cov("transport-error") { // no answer at all: not observed
		if c != "unobserved" {
			t.Errorf("transport-error %s = %q, want unobserved", p, c)
		}
	}
	if c := cov("partial-read")["/ipv6_enabled"]; c != "unobserved" { // a missing source field is not "false"
		t.Errorf("partial-read /ipv6_enabled = %q, want unobserved", c)
	}
	if c := cov("malformed-cidr")["/cidr_block"]; c != "unknown" {
		t.Errorf("malformed-cidr /cidr_block = %q, want unknown", c)
	}
	_, sc, _ := vcnEvidence(t, "partial-read")
	if sc.Verdicts["/ipv6_enabled"] != "UNOBSERVED" || sc.Object != "INCOMPLETE" {
		t.Errorf("partial-read verdicts %v object %s", sc.Verdicts, sc.Object)
	}
}

func TestVCNSlice_TheEvidenceRefusesWhatTheStagesDoNotSupport(t *testing.T) {
	id := "not-found"
	rawDoc := vcnJSON(t, "cases", id, "oci", "error.json")
	intent := vcnJSON(t, "cases", id, "intent.registry.json")
	obsOCI := vcnJSON(t, "cases", id, "expected", "observed.oci.json")
	obsReg := vcnObj(t, "cases", id, "expected", "observed.registry.json")
	verdict := vcnObj(t, "cases", id, "expected", "verdict.fields.json")

	// (a) a verdict that says CONFORMANT for a path whose coverage is unknown
	bad := map[string]interface{}{"object": "CONFORMANT", "fields": map[string]interface{}{}}
	for p := range verdict["fields"].(map[string]interface{}) {
		bad["fields"].(map[string]interface{})[p] = "CONFORMANT"
	}
	if _, _, err := vcnEvidenceFrom(t, id, rawDoc, intent, obsOCI, obsReg, bad); err == nil {
		t.Error("a CONFORMANT verdict over unknown coverage must be refused")
	}
	// (b) an object verdict that is not what the field verdicts add up to
	lie := map[string]interface{}{"object": "CONFORMANT", "fields": verdict["fields"]}
	if _, _, err := vcnEvidenceFrom(t, id, rawDoc, intent, obsOCI, obsReg, lie); err == nil {
		t.Error("an object verdict that contradicts its fields must be refused")
	}
	// (c) a compared path without any coverage
	short := vcnObj(t, "cases", id, "expected", "observed.registry.json")
	delete(short["coverage"].(map[string]interface{}), "/dns_label")
	if _, _, err := vcnEvidenceFrom(t, id, rawDoc, intent, obsOCI, short, verdict); err == nil {
		t.Error("a compared path without coverage must be refused")
	}
}

func TestVCNSlice_TamperingBreaksTheChain(t *testing.T) {
	a, sc, err := vcnEvidence(t, "conformant")
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Steps {
		cp := *a
		cp.Steps = append([]ProofArtifactStep(nil), a.Steps...)
		cp.Steps[i].OutputHash = strings.Repeat("0", 64)
		if VerifyProofArtifact(&cp).Valid {
			t.Errorf("a changed output of step %d (%s) must break the chain", i, a.Steps[i].Name)
		}
	}
	// the claims sidecar cannot be rewritten without the chain noticing: flipping the claim changes its hash
	forged := sc
	forged.ConformanceClaimed = !sc.ConformanceClaimed
	if vcnSidecarBound(t, a, forged) {
		t.Error("a forged claim must not be bound to the chain")
	}
	// and the stage before it: another verdict is another chain
	other, _, _ := vcnEvidence(t, "drift")
	if other.ChainHash == a.ChainHash || other.Steps[2].OutputHash == a.Steps[2].OutputHash {
		t.Error("different cases must give different chains")
	}
}

// --- differential against the Relay's own iac.Evaluate -------------------------------------------------------------------------

func vcnPlan() iac.ConformancePlan {
	return iac.ConformancePlan{
		Scalars: []iac.FieldPlan{{Path: "/cidr_block", Compare: iac.CompareExact}, {Path: "/dns_label", Compare: iac.CompareExact}, {Path: "/ipv6_enabled", Compare: iac.CompareExact}},
		Collections: []iac.CollectionPlan{{
			Path:       "/tags",
			Collection: iac.Collection{Topology: iac.TopologyMap, Keys: []string{"key"}},
			Elements:   []iac.FieldPlan{{Path: "value", Compare: iac.CompareExact}},
		}},
	}
}

// vcnGoEvaluate runs iac.Evaluate for a case: the fixture's registry-side observed state, coverage mapped into the two lists the Go
// Observation has. A coverage state the Go envelope has no name for (unknown) ends up in neither list, i.e. unobserved.
func vcnGoEvaluate(t testing.TB, id string) iac.ConformanceResult {
	t.Helper()
	intent := vcnObj(t, "cases", id, "intent.registry.json")
	obsReg := vcnObj(t, "cases", id, "expected", "observed.registry.json")
	observed, _ := obsReg["config"].(map[string]interface{})
	var obs iac.Observation
	obs.Observed, obs.AuthoritativeAbsent = []string{}, []string{}
	for p, c := range obsReg["coverage"].(map[string]interface{}) {
		switch c.(string) {
		case "observed":
			obs.Observed = append(obs.Observed, p)
		case "absent":
			obs.AuthoritativeAbsent = append(obs.AuthoritativeAbsent, p)
		}
	}
	sort.Strings(obs.Observed)
	sort.Strings(obs.AuthoritativeAbsent)
	res, err := iac.Evaluate(intent, observed, obs, vcnPlan())
	if err != nil {
		t.Fatalf("iac.Evaluate %s: %v", id, err)
	}
	return res
}

// vcnDivergences lists every difference between iac.Evaluate and the fixture's expected verdict, as "case: what".
func vcnDivergences(t testing.TB) []string {
	t.Helper()
	var out []string
	for _, id := range vcnCases(t) {
		res := vcnGoEvaluate(t, id)
		want := vcnObj(t, "cases", id, "expected", "verdict.fields.json")
		if string(res.Object) != want["object"].(string) {
			out = append(out, fmt.Sprintf("%s: object %s, expected %s", id, res.Object, want["object"]))
		}
		wf := want["fields"].(map[string]interface{})
		paths := map[string]bool{}
		for p := range wf {
			paths[p] = true
		}
		for p := range res.Fields {
			paths[p] = true
		}
		var ps []string
		for p := range paths {
			ps = append(ps, p)
		}
		sort.Strings(ps)
		for _, p := range ps {
			g, gok := res.Fields[p]
			w, wok := wf[p]
			switch {
			case !gok:
				out = append(out, fmt.Sprintf("%s: %s missing in iac", id, p))
			case !wok:
				out = append(out, fmt.Sprintf("%s: %s only in iac (%s)", id, p, g))
			case string(g) != w.(string):
				out = append(out, fmt.Sprintf("%s: %s iac %s, expected %s", id, p, g, w))
			}
		}
		// coverage fidelity: the Go envelope has two lists, the fixture three states plus unobserved
		obsReg := vcnObj(t, "cases", id, "expected", "observed.registry.json")
		var unknown []string
		for p, c := range obsReg["coverage"].(map[string]interface{}) {
			if c.(string) == "unknown" {
				unknown = append(unknown, p)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			out = append(out, fmt.Sprintf("%s: coverage unknown (%d paths) is folded into unobserved by the Go Observation", id, len(unknown)))
		}
	}
	return out
}

// TestVCNSlice_GoIacDifferential freezes the exact set of differences between iac.Evaluate (an earlier parallel implementation,
// implemented, with no `unknown` coverage state) and the fixture. A new difference, or a vanished one, fails the test: the table
// in features/feature-011-oci-provider/vcn-slice.md is meant to stay true.
func TestVCNSlice_GoIacDifferential(t *testing.T) {
	got := vcnDivergences(t)
	want := []string{
		"malformed-cidr: coverage unknown (1 paths) is folded into unobserved by the Go Observation",
		"not-found: coverage unknown (5 paths) is folded into unobserved by the Go Observation",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("divergences changed.\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	for _, d := range got {
		t.Log(d)
	}
	// verdict level: iac agrees with the fixture on every field and every object verdict, including the open-question case
	for _, id := range vcnCases(t) {
		res := vcnGoEvaluate(t, id)
		want := vcnObj(t, "cases", id, "expected", "verdict.fields.json")
		if string(res.Object) != want["object"].(string) {
			t.Errorf("%s: object %s vs %s", id, res.Object, want["object"])
		}
	}
	if r := vcnGoEvaluate(t, "desired-absent-observed-present"); r.Fields["/dns_label"] != iac.VerdictNotComparable {
		t.Errorf("open-question case: iac gives %s for /dns_label, the fixture says NOT_COMPARABLE", r.Fields["/dns_label"])
	}
}
