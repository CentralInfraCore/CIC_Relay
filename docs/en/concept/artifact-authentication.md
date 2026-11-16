# Artifact authentication for modules (#162): architecture proposal

Status: **concept. Design only; no code, no behaviour change.** Implementation is on hold until this proposal is reviewed. Companion: [artifact-authentication-test-plan.md](artifact-authentication-test-plan.md).

Basis: CIC_Relay `main@e58344a`; cic-primitives `devel@01cb30b` (fresh shallow clone, 2026-11-05); cic-module-oracle-cloud `devel@299733c`. Statuses below follow the three-level rule (implemented / scaffold / concept) and are backed by the file named.

## 1. The question

After #155 and #197 the relay decides which host operations a module may perform and refuses modules without a verifying certificate. The next question is: **is the module that receives these permissions provably the one they were issued to?** Today the answer is "it carries a certificate chaining to a trusted CA", which does not authenticate the module or its capability manifest (issue #162).

## 2. What is true today

| Layer | Status | Where |
|---|---|---|
| Certificate chain of the `.pem` (EKU CodeSigning, CRL, freshness) | implemented | `cmd/relay/bootstrap.go` `loadModule`; `core/cabinet/pki_verify.go` |
| WASM digest and size against the descriptor (`wasmDigest`, `wasmSize`) | implemented | `loadModule` |
| Signer certificate chains to the CICSourceCA anchor before a `cic-flow`/`git` capability is bound | implemented | `cmd/relay/pki_companion.go:69` `verifyCICFlowProvenance` |
| Grant bound to the stored artifact checksum; instance bound to the manifest digest for its lifetime | implemented | `cmd/relay/cic_flow_registry.go` `stageVerified`, `commitGrant`, `bindingFor` |
| **A signature, made with the certificate's key, over the WASM and the manifest** | **concept** | the bridge breaks at `loadModule`: no signature made with the certificate key is verified. `metadata.cicSign` is parsed (`cic_flow_manifest.go:124`) and never verified; `verifyBuildHash` (`cic_flow_manifest.go:312`) is called from tests only |

The grant and instance binding is real, but it binds **what was loaded**, not **what was authenticated**: nothing proves that the manifest the relay read was approved by anyone. The descriptor that carries `capability_manifest` is authenticated only by the presence of a valid certificate next to it.

Producer side (cic-module-oracle-cloud): `project.yaml` carries `metadata.buildHash`, which that repo's `verify_release` compares to the SHA-256 of `module.wasm` ("integrity, not reproduction"); the same tool states it performs no cryptographic signature verification. In the working tree `sign` and `cicSign` are `TBD` placeholders that the release run fills.

## 3. What cic-primitives already provides (envelope 3)

Read in `docs/en/RELEASE-RECORD-CONTRACT.md`, `docs/en/RELEASE-RECORD-CONFORMANCE.md`, `tools/release_record_conformance.py`, `release.schema.yaml`.

| Piece | Reuse |
|---|---|
| Record shape: `provenance.source_commit`, `artifacts.files[{path,digest}]`, `artifacts.manifest_digest` (sha256sum format), explicit path rules | **unchanged** |
| `release.build_hash` over the canonical bundle (everything except `release.sign`, `release.build_hash`, `cic_countersign`), `release.sign` (Vault ECDSA, prehashed, certificate bound inside the hash), `cic_countersign` by CICSourceCA over `build_hash` | **unchanged** (no new signing mechanism) |
| Reason vocabulary (`envelope_*`, `artifact_*`, `build_hash_mismatch`, `signature_invalid`, `countersign_invalid`) and the order of checks | **unchanged** |
| Golden and negative vectors, the signature-coverage series, the planted-defect proof | **unchanged**, used as language-neutral test vectors for a Go verifier |
| Python reference verifier with three reported layers (record, blobs, crypto) | **unchanged**, used as the oracle for differential testing |
| Producer declaration `compiler_settings.release_artifacts.paths` | **unchanged**; the module producer lists the module's files there |

What primitives **deliberately does not provide** (contract section 2, "trust gates"): the signer's authority, the authorization of the countersignature, certificate validity on the release day, the pinned trust anchor, whether a release is genuine. These are exactly what the relay must add.

## 4. Proposal

**4.1 One record format.** A module is released with an envelope 3 release record. No relay-specific signature format. The module's `release_artifacts.paths` MUST include the `.wasm` and the descriptor/`project.yaml` that carries `abi` and `capability_manifest`; a module whose record does not cover both is refused.

**4.2 Four layers at load, reported apart.**

| Layer | Question | Source |
|---|---|---|
| record | is the record well-formed (envelope 3, sorted unique paths, manifest digest)? | primitives reasons, unchanged |
| shipped | are the bytes the relay is about to load equal to `artifacts.files` digests? (replaces the `blobs` layer: the relay has no git checkout) | new, relay |
| crypto | `build_hash` recomputed; `release.sign` and `cic_countersign` verify over it | primitives algorithm, Go implementation checked against the vectors |
| authority | may this signer release this module with these capabilities? | new, relay (4.3) |

A layer that did not run is never taken for a pass.

**4.3 Authority (the owner's direction).** The key that signs must be provably tied to the **CICSourceCA** trust chain. Vault Transit alone is not an authorization model. Concretely: the countersignature certificate must chain to the pinned CICSourceCA root (the relay already loads `SourceRoots`), carry EKU CodeSigning and not be revoked; `release.sign` must chain to the trust store. A valid chain says "CICSource attests this `build_hash`". It does **not** yet say "this signer may release a module named X with capability Y" (see decision D4).

**4.4 Binding.** The capability is bound to `(module id, build_hash, wasm digest, manifest digest, signer fingerprint)`, not to the module id. A module loaded under a known name with a different digest does not inherit the earlier grant; a running instance keeps the binding it was created with (extend the existing `InstanceBinding` to carry the `build_hash`).

**4.5 Policy.** In `prod` there is no automatic fallback to the certificate-only model: a module without a verifying record does not receive a capability and is not loaded (scope: D3). Existing modules are re-released; no record is rewritten or upgraded (primitives contract section 9). `dev` keeps the explicit opt-in of #197 for a module with **no** record; an invalid record is never admitted.

## 5. Bridge map

concept (this document) -> code: verifier, binding key and policy do not exist (**bridge breaks here**) -> runtime: the loader reads the module triple only -> audit: the load log records the certificate subject, not a verified `build_hash`. Existing, reusable links: `verifyCICFlowProvenance` (anchor), `cicFlowRegistry` (grant and instance binding), the primitives vectors and reference verifier.

## 6. Decisions requested from the owner

| # | Decision | Recommendation |
|---|---|---|
| D1 | Where the record lives: `<name>.release.yaml` next to the module, or embedded in the descriptor | separate file: the record is signed over a bundle, and embedding makes the descriptor part of its own signed input |
| D2 | Fate of the `.pem`: the record already carries the certificates | prod ignores `.pem` for trust and reads the record; `.pem` stays only for dev/legacy |
| D3 | Scope of "no fallback" in `prod`: refuse every module without a record, or only those that import `cic-flow`/`git` | refuse every module (one rule, no capability-dependent path to reason about); confirm |
| D4 | What authorizes a signer for a **module name and capability set** (certificate subject/SAN pattern, or a signed allow-list) | needs a decision; the chain alone does not answer it |
| D5 | Time: the record has no trusted timestamp; certificate validity is checked at load, not on the release day | accept load-time validity first, record the gap; a timestamp is a separate item |
| D6 | Verifier implementation | independent Go implementation proven against the primitives vectors and differentially against the Python reference (the practice already used for Forms Rust/Go) |
| D7 | Revoking a single release (the CRL covers certificates, not a `build_hash`) | out of scope here; a deny-list is a follow-up |

## 7. Out of scope, and what this survey did not verify

Out of scope: the load-time check of the WASM binary's actual import list, re-introducing `git.push`, the Oracle end-to-end integration. Not verified: how the primitives compiler's envelope 3 output behaves today (only the contract, the verifier and the vectors were read); the exact semantics of the CICSourceCA countersignature beyond `release.schema.yaml`; whether the Oracle release run produces a real signed record.
