// Package o1verify is an independent verifier of ProofTrace V2 artifacts (O-1 WP-05, AC-V2-08/10/12/13/15).
//
// Independence is structural, not a claim: this package imports nothing from core/cabinet (the producer), reads
// the artifact as generic JSON, and derives the CICCHAINv2 hash input and the CICHOSTINTv1 list digest from its own
// code. Its agreement with the producer is therefore evidence of two implementations reading the same contract,
// checked against the shared vectors, not of shared code.
//
// It makes two separate assertions (V2-02): EvidenceVerdict (the evidence is authentic and a complete
// representation of what happened, whatever happened) and Acceptance (the operation met the mandatory policy and
// evidence conditions). The Relay cannot make either about its own artifact.
//
// What a schema cannot prove is proved here: the chain hash is recomputed, the host-interaction list is recomputed
// from the records and compared, and a persistence claim counts only if its attestation is verified by a verifier
// the caller supplies (the trust input), against this very artifact's chain hash. The verifier's own identity
// (VerifierInfo.ImplementationDigest) is a DECLARATION; that the declared implementation is the one that ran is
// not proved by this package and must be established separately (AC-V2-13).
package o1verify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Dimension values of a VerificationResult.
const (
	// VerificationComplete is a value of a VerificationResult field (V2-02).
	VerificationComplete = "COMPLETE"
	// VerificationIncomplete is a value of a VerificationResult field (V2-02).
	VerificationIncomplete = "INCOMPLETE"
	// VerificationFailed is a value of a VerificationResult field (V2-02).
	VerificationFailed = "FAILED"

	// EvidencePersisted is a value of a VerificationResult field (V2-02).
	EvidencePersisted = "PERSISTED"
	// EvidenceNotPersisted is a value of a VerificationResult field (V2-02).
	EvidenceNotPersisted = "NOT_PERSISTED"
	// EvidenceNotRequired is a value of a VerificationResult field (V2-02).
	EvidenceNotRequired = "NOT_REQUIRED"
	// EvidenceUnknown is a value of a VerificationResult field (V2-02).
	EvidenceUnknown = "UNKNOWN"

	// VerdictVerified is a value of a VerificationResult field (V2-02).
	VerdictVerified = "VERIFIED"
	// VerdictNotVerified is a value of a VerificationResult field (V2-02).
	VerdictNotVerified = "NOT_VERIFIED"

	// AcceptanceAccepted is a value of a VerificationResult field (V2-02).
	AcceptanceAccepted = "ACCEPTED"
	// AcceptanceNotAccepted is a value of a VerificationResult field (V2-02).
	AcceptanceNotAccepted = "NOT_ACCEPTED"
	// AcceptanceNotEvaluated is a value of a VerificationResult field (V2-02).
	AcceptanceNotEvaluated = "NOT_EVALUATED"
)

// Dimensions are the four independent result dimensions.
type Dimensions struct {
	Execution     string `json:"execution"`
	Authorization string `json:"authorization"`
	Evidence      string `json:"evidence"`
	Verification  string `json:"verification"`
}

// VerifierInfo identifies the verifier. ImplementationDigest is a declaration, not a proof (see the package doc).
type VerifierInfo struct {
	ID                   string `json:"id"`
	Version              string `json:"version"`
	ImplementationDigest string `json:"implementation_digest"`
}

// Subject names what was verified.
type Subject struct {
	ArtifactChainHash string `json:"artifact_chain_hash"`
}

// Result is the VerificationResult (verification-result.schema.json).
type Result struct {
	Kind                   string       `json:"kind"`
	ArtifactVersion        string       `json:"artifact_version"`
	Verifier               VerifierInfo `json:"verifier"`
	Subject                Subject      `json:"subject"`
	Dimensions             Dimensions   `json:"dimensions"`
	UnresolvedReferences   []string     `json:"unresolved_references,omitempty"`
	Problems               []string     `json:"problems,omitempty"` // named reasons for a FAILED verification
	EvidenceVerdict        string       `json:"evidence_verdict"`
	Acceptance             string       `json:"acceptance"`
	AcceptancePolicyDigest string       `json:"acceptance_policy_digest,omitempty"`
}

// Attestation is the persistence attestation as carried by the persistence object.
type Attestation struct {
	Kind            string `json:"kind"`
	SignerKeyID     string `json:"signer_key_id"`
	SignatureDigest string `json:"signature_digest"`
}

// AttestationVerifier is the verifier's trust input: it checks, cryptographically and against the caller's pinned
// recorder keys, that the attestation really binds chainHash. A nil verifier means the attestation cannot be checked.
type AttestationVerifier interface {
	VerifyAttestation(att Attestation, artifactChainHash string) error
}

// Persistence is the persistence object kept outside the artifact (prooftrace-v2-persistence.schema.json).
type Persistence struct {
	ArtifactChainHash string       `json:"artifact_chain_hash"`
	Persisted         bool         `json:"persisted"`
	Recorder          string       `json:"recorder"`
	Commit            string       `json:"commit,omitempty"`
	ErrorCode         string       `json:"error_code,omitempty"`
	Attestation       *Attestation `json:"attestation,omitempty"`
}

// Options are the inputs of one verification besides the artifact itself.
type Options struct {
	Verifier     VerifierInfo
	Persistence  *Persistence        // nil: no persistence evidence was supplied
	Attestations AttestationVerifier // trust input; nil: attestations are unverifiable
	// HostInteractions holds the records of the host-interaction lists, keyed by the list digest the artifact
	// references. A reference whose list is absent here is reported as unresolved, never as passed.
	HostInteractions map[string][]map[string]any
	// PolicyDigest identifies the acceptance policy; empty means no policy was applied (acceptance NOT_EVALUATED).
	PolicyDigest string
	// PersistenceRequired is the policy's evidence condition.
	PersistenceRequired bool
}

var (
	// digestRE is a package-level lookup table or pattern of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// rejCodeRE is a package-level lookup table or pattern of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	rejCodeRE = regexp.MustCompile(`^[A-Z_]+$`)
	// stepCommon is a package-level lookup table or pattern of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	stepCommon = []string{"index", "requested.module_id", "requested.operation", "requested.presented_artifact_digest", "outcome.execution", "outcome.authorization", "input_digest", "output_digest", "references.execution_context.digest", "references.authorization_decision.digest", "references.host_interactions.digest", "references.host_interactions.count"}
	// executedFld is a package-level lookup table or pattern of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	executedFld = []string{"artifact_digest", "executed_digest", "manifest_digest", "envelope_digest", "profile.id", "profile.digest", "dispatched", "trust_state"}
	// recordFld is a package-level lookup table or pattern of the ProofTrace V2 contract (04-prooftrace-v2-wire.md).
	recordFld = []string{"interaction_id", "seq", "decision", "code", "method", "host", "path_sha256", "request_body_sha256", "status", "response_body_sha256", "policy_digest", "profile_digest", "signing_profile_digest"}
)

// sha is a small helper of the ProofTrace V2 implementation.
func sha(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }

// walk reads a dotted path from a decoded JSON object; absent and null are both (nil, false).
func walk(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, p := range strings.Split(path, ".") {
		o, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = o[p]
		if !ok || cur == nil {
			return nil, false
		}
	}
	return cur, true
}

// scalar renders one hash-input value: null for absent/null, the string, or the decimal integer; anything else
// (a bool, a float, a container, a line break, a non-ASCII byte) cannot be represented and is refused.
func scalar(v any, present bool) (string, error) {
	if !present {
		return "null", nil
	}
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case json.Number:
		if _, err := strconv.ParseInt(x.String(), 10, 64); err != nil {
			return "", fmt.Errorf("a non-integer number %q", x.String())
		}
		s = x.String()
	default:
		return "", fmt.Errorf("a value of type %T cannot be represented", v)
	}
	if strings.ContainsAny(s, "\r\n") || strings.IndexFunc(s, func(r rune) bool { return r > 127 }) >= 0 {
		return "", fmt.Errorf("a value with a line break or a non-ASCII byte")
	}
	return s, nil
}

// decode is a small helper of the ProofTrace V2 implementation.
func decode(doc []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("not a JSON object: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("data after the artifact")
	}
	return m, nil
}

// ChainInput derives the CICCHAINv2 hash input of an artifact document from its own reading of the JSON.
func ChainInput(doc []byte) ([]byte, error) {
	a, err := decode(doc)
	if err != nil {
		return nil, err
	}
	return chainInput(a)
}

// chainInput is a small helper of the ProofTrace V2 implementation.
func chainInput(a map[string]any) ([]byte, error) {
	lines := []string{"CICCHAINv2"}
	add := func(key string, v any, present bool) error {
		s, err := scalar(v, present)
		if err != nil {
			return fmt.Errorf("%s: %v", key, err)
		}
		lines = append(lines, key+"="+s)
		return nil
	}
	for _, p := range []string{"version", "workflow.id", "workflow.source_digest", "outcome.execution", "outcome.authorization"} {
		v, ok := walk(a, p)
		if err := add(p, v, ok); err != nil {
			return nil, err
		}
	}
	steps, _ := a["steps"].([]any)
	lines = append(lines, "steps="+strconv.Itoa(len(steps)))
	for pos, raw := range steps {
		st, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("steps[%d] is not an object", pos)
		}
		idx, _ := walk(st, "index")
		i, err := scalar(idx, idx != nil)
		if err != nil {
			return nil, fmt.Errorf("steps[%d].index: %v", pos, err)
		}
		name, _ := st["name"].(string)
		lines = append(lines, fmt.Sprintf("step.%s.name_sha256=%s", i, sha([]byte(name))))
		for _, f := range stepCommon {
			v, ok := walk(st, f)
			if err := add(fmt.Sprintf("step.%s.%s", i, f), v, ok); err != nil {
				return nil, err
			}
		}
		rej, hasRej := st["rejection"].(map[string]any)
		lines = append(lines, fmt.Sprintf("step.%s.rejection=%s", i, map[bool]string{true: "present", false: "absent"}[hasRej]))
		if hasRej {
			for _, f := range []string{"phase", "code"} {
				v, ok := walk(rej, f)
				if err := add(fmt.Sprintf("step.%s.rejection.%s", i, f), v, ok); err != nil {
					return nil, err
				}
			}
		}
		exe, hasExe := st["executed"].(map[string]any)
		lines = append(lines, fmt.Sprintf("step.%s.executed=%s", i, map[bool]string{true: "present", false: "absent"}[hasExe]))
		if hasExe {
			for _, f := range executedFld {
				v, ok := walk(exe, f)
				if err := add(fmt.Sprintf("step.%s.executed.%s", i, f), v, ok); err != nil {
					return nil, err
				}
			}
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// ChainHash is sha256 of the hash input, in the sha256:<hex> form.
func ChainHash(doc []byte) (string, error) {
	in, err := ChainInput(doc)
	if err != nil {
		return "", err
	}
	return sha(in), nil
}

// InteractionsDigest recomputes the CICHOSTINTv1 list digest from the records (sequence contiguous from 0).
func InteractionsDigest(records []map[string]any) (string, error) {
	lines := []string{"CICHOSTINTv1", "count=" + strconv.Itoa(len(records))}
	for want, r := range records {
		seq, _ := walk(r, "seq")
		s, err := scalar(seq, seq != nil)
		if err != nil || s != strconv.Itoa(want) {
			return "", fmt.Errorf("record %d: the sequence is not contiguous from 0", want)
		}
		var b strings.Builder
		for _, f := range recordFld {
			v, ok := walk(r, f)
			sv, err := scalar(v, ok)
			if err != nil {
				return "", fmt.Errorf("record %d: %s: %v", want, f, err)
			}
			b.WriteString(f + "=" + sv + "\n")
		}
		for _, f := range []string{"operation", "resource_id", "binding_digest"} {
			v, ok := walk(r, "intent."+f)
			sv, err := scalar(v, ok)
			if err != nil {
				return "", fmt.Errorf("record %d: intent.%s: %v", want, f, err)
			}
			b.WriteString("intent." + f + "=" + sv + "\n")
		}
		lines = append(lines, fmt.Sprintf("%d:%s", want, sha([]byte(b.String()))))
	}
	return sha([]byte(strings.Join(lines, "\n") + "\n")), nil
}

// Verify checks one artifact document and returns the VerificationResult. It never panics on hostile input: every
// refusal is a named problem on a FAILED verification.
func Verify(doc []byte, opt Options) Result {
	res := Result{Kind: "VerificationResult", ArtifactVersion: "2.0", Verifier: opt.Verifier,
		Dimensions: Dimensions{Execution: "UNKNOWN", Authorization: "UNKNOWN", Evidence: EvidenceUnknown, Verification: VerificationComplete}}
	fail := func(format string, a ...any) { res.Problems = append(res.Problems, fmt.Sprintf(format, a...)) }
	art, err := decode(doc)
	if err != nil {
		fail("ARTIFACT_UNREADABLE: %v", err)
		return finish(&res, opt)
	}
	str := func(p string) string { v, _ := walk(art, p); s, _ := v.(string); return s }
	if str("kind") != "ProofArtifact" || str("version") != "2.0" || str("chain_version") != "CICCHAINv2" {
		fail("WRONG_DOMAIN: not a ProofArtifact 2.0 / CICCHAINv2 (a V1 record cannot satisfy a V2 claim)")
	}
	res.Dimensions.Execution, res.Dimensions.Authorization = orUnknown(str("outcome.execution")), orUnknown(str("outcome.authorization"))
	claimed := str("chain_hash")
	res.Subject.ArtifactChainHash = claimed
	if got, err := ChainHash(doc); err != nil {
		fail("HASH_INPUT_UNREPRESENTABLE: %v", err)
	} else if got != claimed {
		fail("CHAIN_HASH_MISMATCH: recomputed %s, artifact says %s", got, claimed)
	}
	steps, _ := art["steps"].([]any)
	if len(steps) == 0 {
		fail("STEPS_REQUIRED")
	}
	var refs []struct {
		digest string
		count  int
	}
	for pos, raw := range steps {
		st, ok := raw.(map[string]any)
		if !ok {
			fail("STEP_NOT_AN_OBJECT: %d", pos)
			continue
		}
		checkStep(pos, st, fail)
		d, _ := walk(st, "references.host_interactions.digest")
		c, _ := walk(st, "references.host_interactions.count")
		ds, _ := d.(string)
		cn, _ := c.(json.Number)
		n, _ := cn.Int64()
		refs = append(refs, struct {
			digest string
			count  int
		}{ds, int(n)})
	}
	for _, r := range refs {
		recs, have := opt.HostInteractions[r.digest]
		if !have {
			res.UnresolvedReferences = append(res.UnresolvedReferences, "host_interactions:"+r.digest)
			continue
		}
		got, err := InteractionsDigest(recs)
		switch {
		case err != nil:
			fail("HOST_INTERACTIONS_INVALID: %v", err)
		case got != r.digest:
			fail("HOST_INTERACTIONS_DIGEST_MISMATCH: recomputed %s, referenced %s", got, r.digest)
		case len(recs) != r.count:
			fail("HOST_INTERACTIONS_COUNT_MISMATCH: %d records, count says %d", len(recs), r.count)
		}
	}
	checkPersistence(&res, opt, claimed)
	return finish(&res, opt)
}

// orUnknown is a small helper of the ProofTrace V2 implementation.
func orUnknown(s string) string {
	if s == "" {
		return "UNKNOWN"
	}
	return s
}

// checkStep applies the V2-01 identity rules and the authenticated / authorization-decision rules.
func checkStep(pos int, st map[string]any, fail func(string, ...any)) {
	exec, _ := walk(st, "outcome.execution")
	authz, _ := walk(st, "outcome.authorization")
	_, hasExe := st["executed"].(map[string]any)
	_, hasRej := st["rejection"].(map[string]any)
	if _, ok := walk(st, "requested.module_id"); !ok {
		fail("REQUESTED_IDENTITY_REQUIRED: step %d", pos)
	}
	switch exec {
	case "REJECTED":
		if hasExe {
			fail("REJECTED_WITH_EXECUTED: step %d claims an executed identity it never had", pos)
		}
		if !hasRej {
			fail("REJECTED_WITHOUT_REJECTION: step %d", pos)
		}
		if v, ok := walk(st, "output_digest"); ok && v != nil {
			fail("REJECTED_WITH_OUTPUT: step %d", pos)
		}
		if c, _ := walk(st, "references.host_interactions.count"); c != json.Number("0") {
			fail("REJECTED_WITH_HOST_INTERACTIONS: step %d", pos)
		}
	case "SUCCEEDED", "FAILED", "ABORTED":
		if !hasExe {
			fail("EXECUTED_REQUIRED: step %d", pos)
		}
		if hasRej {
			fail("EXECUTED_WITH_REJECTION: step %d", pos)
		}
	default:
		fail("EXECUTION_OUTCOME_INVALID: step %d", pos)
	}
	if ts, _ := walk(st, "executed.trust_state"); ts == "authenticated" {
		if v, ok := walk(st, "executed.envelope_digest"); !ok || !digestRE.MatchString(fmt.Sprint(v)) {
			fail("AUTHENTICATED_WITHOUT_ENVELOPE_DIGEST: step %d", pos)
		}
	}
	if authz == "ALLOWED" || authz == "DENIED" {
		if _, ok := walk(st, "references.authorization_decision.digest"); !ok {
			fail("AUTHORIZATION_DECISION_REFERENCE_REQUIRED: step %d", pos)
		}
	}
	if hasRej {
		if c, _ := walk(st, "rejection.code"); !rejCodeRE.MatchString(fmt.Sprint(c)) {
			fail("REJECTION_CODE_INVALID: step %d", pos)
		}
	}
}

// checkPersistence decides the evidence dimension. A persistence claim counts only if it names this artifact's
// chain hash and, when it says "persisted", its attestation is verified by the caller's trust input.
func checkPersistence(res *Result, opt Options, chain string) {
	p := opt.Persistence
	if p == nil {
		if !opt.PersistenceRequired {
			res.Dimensions.Evidence = EvidenceNotRequired
		}
		return
	}
	if p.ArtifactChainHash != chain {
		res.Problems = append(res.Problems, "PERSISTENCE_NOT_BOUND_TO_THIS_ARTIFACT")
		return
	}
	if !p.Persisted {
		if p.Commit != "" || p.Attestation != nil || p.ErrorCode == "" {
			res.Problems = append(res.Problems, "PERSISTENCE_INCONSISTENT: a not-persisted record carries a commit or an attestation, or has no error code")
			return
		}
		res.Dimensions.Evidence = EvidenceNotPersisted // the operation is not claimed not to have happened
		return
	}
	if p.Commit == "" || p.Attestation == nil {
		res.Problems = append(res.Problems, "PERSISTED_WITHOUT_ATTESTATION")
		return
	}
	if opt.Attestations == nil {
		res.UnresolvedReferences = append(res.UnresolvedReferences, "persistence:attestation-unverifiable")
		return
	}
	if err := opt.Attestations.VerifyAttestation(*p.Attestation, chain); err != nil {
		res.Problems = append(res.Problems, "PERSISTENCE_ATTESTATION_INVALID: "+err.Error())
		return
	}
	res.Dimensions.Evidence = EvidencePersisted
}

// finish derives verification, the evidence verdict and the acceptance from the collected findings.
func finish(res *Result, opt Options) Result {
	switch {
	case len(res.Problems) > 0:
		res.Dimensions.Verification = VerificationFailed
	case len(res.UnresolvedReferences) > 0:
		res.Dimensions.Verification = VerificationIncomplete
	default:
		res.Dimensions.Verification = VerificationComplete
	}
	res.EvidenceVerdict = VerdictNotVerified
	if res.Dimensions.Verification == VerificationComplete {
		res.EvidenceVerdict = VerdictVerified
	}
	res.Acceptance = AcceptanceNotEvaluated
	if res.EvidenceVerdict == VerdictVerified && opt.PolicyDigest != "" {
		res.AcceptancePolicyDigest = opt.PolicyDigest
		evidenceOK := res.Dimensions.Evidence == EvidencePersisted || (!opt.PersistenceRequired && res.Dimensions.Evidence == EvidenceNotRequired)
		if res.Dimensions.Execution == "SUCCEEDED" && res.Dimensions.Authorization == "ALLOWED" && evidenceOK {
			res.Acceptance = AcceptanceAccepted
		} else {
			res.Acceptance = AcceptanceNotAccepted
		}
	}
	return *res
}
