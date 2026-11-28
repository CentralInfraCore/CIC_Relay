// Command o1verify is the standalone independent verifier of ProofTrace V2 artifacts (O-1 INT-10).
//
//	o1verify --artifact A.json [--interactions I.json] [--decisions D.json] [--contexts C.json] [--persistence P.json --attestation-material M.json]
//	         [--bundle B.json --bundle-pin sha256:… ] [--policy-digest sha256:… --require-persistence]
//	         [--sign-key K.pem] [--pinned-implementation sha256:…,…]
//
// It reads the artifact as generic JSON and shares no code with the cabinet that produced it. The trust inputs are the
// operator's: the trust bundle (admitted by its pinned digest) says which keys may attest persistence and sign verifier
// results. Output is the VerificationResult (and, with --sign-key, its signature). Exit 0: the evidence is VERIFIED; 1:
// NOT_VERIFIED; 2: a usage or input error. VERIFIED is not ACCEPTED: acceptance needs --policy-digest and is reported
// separately.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"centralrelay/pkg/o1verify"
	"centralrelay/pkg/o1verify/attest"
	"centralrelay/pkg/trustbundle"
)

// readJSON reads one JSON file with numbers as json.Number (the verifier reads integers exactly).
func readJSON(path string, into any) error {
	data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied input path
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: unexpected data after the JSON value", path)
	}
	return nil
}

// run executes the command; it returns the exit code.
func run(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("o1verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	artifact := fs.String("artifact", "", "the ProofTrace V2 artifact (JSON)")
	interactions := fs.String("interactions", "", "the host-interaction lists, a JSON map of list digest to records")
	decisions := fs.String("decisions", "", "the authorization records behind the artifact's decision digests (JSON map of digest to record)")
	contexts := fs.String("contexts", "", "the execution-context records behind the artifact's context digests (JSON map of digest to record)")
	persistence := fs.String("persistence", "", "the persistence object (JSON)")
	material := fs.String("attestation-material", "", "the recorder attestation material ({signature_b64, spki_b64})")
	bundlePath := fs.String("bundle", "", "the operator's trust bundle")
	bundlePin := fs.String("bundle-pin", "", "the sha256:<hex> of the exact bundle bytes the operator pinned")
	policy := fs.String("policy-digest", "", "the acceptance policy digest (empty: acceptance is not evaluated)")
	requirePersist := fs.Bool("require-persistence", false, "the policy requires an attested persistence")
	signKey := fs.String("sign-key", "", "a PKCS#8 PEM ed25519 key to sign the result with (it must hold verifier:attest in the bundle)")
	pinned := fs.String("pinned-implementation", "", "comma-separated implementation digests the operator pinned")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(format string, a ...any) int { fmt.Fprintf(stderr, "o1verify: "+format+"\n", a...); return 2 }
	if *artifact == "" {
		return fail("--artifact is required")
	}
	doc, err := os.ReadFile(*artifact) // #nosec G304 -- operator-supplied input path
	if err != nil {
		return fail("%v", err)
	}

	impl, err := attest.RuntimeImplementationDigest()
	if err != nil {
		return fail("cannot compute the implementation digest: %v", err)
	}
	opt := o1verify.Options{Verifier: o1verify.VerifierInfo{ID: "o1verify", Version: "1", ImplementationDigest: impl}, PolicyDigest: *policy, PersistenceRequired: *requirePersist}

	if *interactions != "" {
		if err := readJSON(*interactions, &opt.HostInteractions); err != nil {
			return fail("%v", err)
		}
	}
	for path, into := range map[string]*map[string]map[string]any{*decisions: &opt.AuthorizationDecisions, *contexts: &opt.ExecutionContexts} {
		if path == "" {
			continue
		}
		if err := readJSON(path, into); err != nil {
			return fail("%v", err)
		}
	}
	// The empty interaction list is a constant the verifier derives itself: a step without host calls needs no records file.
	if opt.HostInteractions == nil {
		opt.HostInteractions = map[string][]map[string]any{}
	}
	if emptyDigest, err := o1verify.InteractionsDigest(nil); err == nil {
		if _, given := opt.HostInteractions[emptyDigest]; !given {
			opt.HostInteractions[emptyDigest] = []map[string]any{}
		}
	}
	var adm *trustbundle.Admitted
	if *bundlePath != "" {
		raw, err := os.ReadFile(*bundlePath) // #nosec G304 -- operator-supplied trust-domain path
		if err != nil {
			return fail("%v", err)
		}
		if adm, err = trustbundle.Admit(raw, trustbundle.Policy{PinnedDigest: *bundlePin}, trustbundle.State{}, now); err != nil {
			return fail("the trust bundle is not admitted: %v", err)
		}
	}
	if *persistence != "" {
		var p o1verify.Persistence
		if err := readJSON(*persistence, &p); err != nil {
			return fail("%v", err)
		}
		opt.Persistence = &p
		if p.Attestation != nil {
			if adm == nil || *material == "" {
				return fail("a persistence attestation needs --bundle (with --bundle-pin) and --attestation-material to be checked")
			}
			var m struct {
				Sig  string `json:"signature_b64"`
				SPKI string `json:"spki_b64"`
			}
			if err := readJSON(*material, &m); err != nil {
				return fail("%v", err)
			}
			sig, e1 := base64.StdEncoding.DecodeString(m.Sig)
			spki, e2 := base64.StdEncoding.DecodeString(m.SPKI)
			if e1 != nil || e2 != nil {
				return fail("the attestation material is not base64")
			}
			opt.Attestations = attest.RecorderVerifier{Authorities: adm.RecorderAttest, Lookup: func(d string) (attest.Material, bool) {
				return attest.Material{Signature: sig, SPKI: spki}, true // the verifier re-hashes it against signature_digest
			}}
		}
	}

	res := o1verify.Verify(doc, opt)
	out := map[string]any{"result": res}
	if *signKey != "" {
		if adm == nil {
			return fail("--sign-key needs --bundle: the signer's authority comes from the bundle")
		}
		pemData, err := os.ReadFile(*signKey) // #nosec G304 -- operator-supplied key path
		if err != nil {
			return fail("%v", err)
		}
		block, _ := pem.Decode(pemData)
		if block == nil {
			return fail("--sign-key is not PEM")
		}
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		priv, ok := k.(ed25519.PrivateKey)
		if err != nil || !ok {
			return fail("--sign-key must be a PKCS#8 ed25519 key")
		}
		spki, _ := x509.MarshalPKIXPublicKey(priv.Public())
		sr, err := attest.SignResult(res, spki, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil })
		if err != nil {
			return fail("%v", err)
		}
		pins := map[string]bool{}
		for _, d := range strings.Split(*pinned, ",") {
			if d = strings.TrimSpace(d); d != "" {
				pins[d] = true
			}
		}
		attr, err := attest.VerifyResult(*sr, adm.VerifierAttest, pins) // refuse to emit a result its own key cannot stand behind
		if err != nil {
			return fail("the signing key is not authorised to attest this result: %v", err)
		}
		out["signature"] = map[string]any{"signer_key_id": sr.SignerKey, "alg": sr.Alg, "signature_b64": base64.StdEncoding.EncodeToString(sr.Signature),
			"spki_b64": base64.StdEncoding.EncodeToString(sr.SPKI), "implementation_pinned": attr.ImplementationPinned}
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fail("%v", err)
	}
	if res.EvidenceVerdict != o1verify.VerdictVerified {
		return 1
	}
	return 0
}

// main runs the command against the process streams.
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now())) }
