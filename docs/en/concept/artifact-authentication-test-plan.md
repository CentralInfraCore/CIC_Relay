# Artifact authentication (#162): test plan

Status: **concept.** Companion of [artifact-authentication.md](artifact-authentication.md); section numbers there. Nothing here is implemented; every test below is written to **fail on the current `main`** before the implementation exists.

## 1. Acceptance (negative tests)

Each test builds a real module with a real, test-keyed release record (keys derived from fixed labels, as in the primitives vectors) and loads it through both loaders (embedded, installed) in `prod`.

| # | Test | Expected |
|---|---|---|
| A1 | A valid certificate (chaining to the trust store and the CICSourceCA anchor) copied next to a module built by someone else, with no record or a record that does not cover this WASM | refused; no capability |
| A2 | One byte of the `.wasm` changed after signing | refused (`shipped`: digest mismatch) |
| A3 | One permission of `capability_manifest` changed after signing (wider egress, extra secrets entry, extra git rule) | refused |
| A4 | A different artifact loaded under the same module name and version | refused or loaded without the earlier grant; the earlier module's capability is not inherited |
| A5 | A running instance after a reload of the same module id with a different manifest | keeps the binding it was created with; no widening, no narrowing |
| A6 | The record covers the WASM but not the descriptor (or the reverse) | refused (4.1) |
| A7 | `release.sign` valid, `cic_countersign` absent, wrong, or by a certificate not chaining to CICSourceCA | refused in `prod`; reported as `NOT_RUN` / `FAIL`, never as a pass |
| A8 | Envelope 1 or 2 record | refused, `envelope_too_old`; not upgraded |

## 2. Fail-closed policy table

The decision table of `module_signing_test.go` is extended: modules (no record, valid record, each invalid record kind) x environments (`prod`, `prod`+opt-in, `dev`, `dev`+opt-in) x loaders. Required: no opt-in admits an invalid record; `prod` never loads a module without a verifying record (D3); the `dev` opt-in admits only "no record".

## 3. Verifier conformance

* The primitives golden and negative vectors, copied with their provenance (commit, SHA-256), run through the Go verifier: every reason of the contract's section 11 has at least one vector and the same reason is reported.
* The signature-coverage series: each defect of the series fails at the layer the primitives document names.
* Differential: generated records (valid and mutated) verified by the Go verifier and by `tools/release_record_conformance.py`; any difference in verdict or reason fails the test.
* Planted defects: each check of the Go verifier is switched off in turn; a vector must fail for each.

## 4. Lifecycle and audit

* The grant key includes `build_hash`, wasm digest, manifest digest and signer fingerprint (4.4); a unit test per component.
* The load log records the verified `build_hash` and signer fingerprint (audit bridge), and a refusal records the layer and the reason.
* Concurrent loads of the same module id (the existing `moduleLoadMu` tests) still hold with the new key.

## 5. Evidence required for the implementation PR

Mutation evidence (killed / equivalent / survived-then-strengthened) for the verifier, the policy and the binding code; the CI-equivalent run of #197 (gofmt -s, vet, staticcheck, govulncheck, deadcode, golden verify, symbol/manifest drift, `-race -count=2 -shuffle=on`, coverage thresholds); the signed commit chain; EN/HU docs. Decisions D1 to D7 are resolved in the design before any code is written.

## 6. Not planned here

The WASM import-list check, `git.push` re-introduction and the Oracle end-to-end test have their own plans.
