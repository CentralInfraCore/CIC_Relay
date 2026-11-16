# Artifact authentication for modules (#162): architecture proposal

Status: **concept. Design only; no code, no behaviour change.** Implementation is on hold until this proposal is reviewed. Companion: [artifact-authentication-test-plan.md](artifact-authentication-test-plan.md).

Revised after the architectural review of #200 (findings F1 to F5: the `cic-flow` binding is weaker than git, instance identity versus authority, authorization as a separate decision, verified bytes versus executed bytes, producer compatibility).

Basis: CIC_Relay `main@e58344a`; cic-primitives `devel@01cb30b` (fresh shallow clone, 2026-11-05); cic-module-oracle-cloud `devel@299733c`. Statuses below follow the three-level rule (implemented / scaffold / concept) and are backed by the file named.

## 1. The question

After #155 and #197 the relay decides which host operations a module may perform and refuses modules without a verifying certificate. The next question is: **is the module that receives these permissions provably the one they were issued to?** Today the answer is "it carries a certificate chaining to a trusted CA", which does not authenticate the module or its capability manifest (issue #162).

## 2. What is true today

| Layer | Status | Where |
|---|---|---|
| Certificate chain of the `.pem` (EKU CodeSigning, CRL, freshness) | implemented | `cmd/relay/bootstrap.go` `loadModule`; `core/cabinet/pki_verify.go` |
| WASM digest and size against the descriptor (`wasmDigest`, `wasmSize`) | implemented | `loadModule` |
| Signer certificate chains to the CICSourceCA anchor before a `cic-flow`/`git` capability is bound | implemented | `cmd/relay/pki_companion.go:69` `verifyCICFlowProvenance` |
| **git**: grant bound to the stored artifact checksum, staged until the artifact is stored, instance bound to the manifest digest | implemented | `cmd/relay/cic_flow_registry.go` `stageVerified`, `commitGrant`, `bindingFor`, `authorizeGit` |
| **cic-flow** (`sign`, `actuate`): deps published at once under the module id; the lookup looks at the module id only; no artifact checksum, no manifest digest, no staging | implemented, **weaker than git** | `cmd/relay/cic_flow_registry.go` `publish` (`r.deps[moduleID]`), `lookup` |
| **A signature, made with the certificate's key, over the WASM and the manifest** | **concept** | the bridge breaks at `loadModule`: no signature made with the certificate key is verified. `metadata.cicSign` is parsed (`cic_flow_manifest.go:124`) and never verified; `verifyBuildHash` (`cic_flow_manifest.go:312`) is called from tests only |

The git grant and instance binding is real, but it binds **what was loaded**, not **what was authenticated**: nothing proves that the manifest the relay read was approved by anyone. The `cic-flow` capability is weaker still: it is keyed by the module id alone, so by reading `publish` and `lookup` an instance created before a reload resolves the deps of the module id as they are after the reload. (Read in the code; this survey did not run a test for it.) There are two security levels today, and the design must end with one. The descriptor that carries `capability_manifest` is authenticated only by the presence of a valid certificate next to it.

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

**Producer compatibility, measured.** A real, compiler-produced envelope 3 record exists: the release record of `primitives/@v0.2.1` (tag commit `5e8c321`, `provenance.source_commit` `106965b`, 30 artifacts, countersignature present). The reference verifier (`tools/release_record_conformance.py`, run on the fresh clone) reports `record` PASS and `crypto` PASS (`build_hash`, `signature`, `countersign` each PASS); `blobs` is NOT_RUN (no repository given) and the verifier itself states that who signed is not proven. So the verifier side is demonstrated on real compiler output. **The module side is not:** the release tooling of cic-module-oracle-cloud (the base-repo template compiler) has no `release_artifacts` declaration, no `artifacts`/`manifest_digest` and no `source_commit` (searched in `tools/`, `mk/`, `project.yaml`); its `build-hash` step writes the SHA-256 of the WASM into `metadata.buildHash`. The `envelopeVersion: 1` in its `project.yaml` is the WASM ABI envelope, not a release record. A module therefore cannot get an envelope 3 record from its own tooling today; that producer work is part of #162 and is a precondition of any real module-level test.

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

**4.3 Authority (the owner's direction).** The key that signs must be provably tied to the **CICSourceCA** trust chain. Vault Transit alone is not an authorization model. Concretely: the countersignature certificate must chain to the pinned CICSourceCA root (the relay already loads `SourceRoots`), carry EKU CodeSigning and not be revoked; `release.sign` must chain to the trust store. A valid chain says "CICSource attests this `build_hash`". It does **not** say "this signer may release a module named X with capability Y": **the signed artifact is not itself an authorization; it is one authenticated input to the authorization decision.** The decision is a separate step: verified envelope -> verified artifact and manifest -> signer identity and chain -> a *declared, authenticated authorization rule* -> the set of modules and capabilities this signer may release -> an artifact-bound grant (denied if any step fails). The rule is a declared policy the relay verifies, not a pattern encoded in a certificate subject/SAN (D4). Open inside D4: who signs the policy and where it lives.

**4.4 Binding, one mechanism for git and cic-flow.** The capability is bound to `(module id, build_hash, wasm digest, manifest digest, signer fingerprint)`, not to the module id, and **both** `git` and `cic-flow` use the same artifact-bound, staged-then-committed grant that git has today; `cic-flow` is moved onto it (today it is name-keyed and immediate). There must not be two security levels. A module loaded under a known name with a different digest does not inherit the earlier grant.

**4.5 Policy.** In `prod` there is no automatic fallback to the certificate-only model: a module without a verifying record does not receive a capability and is not loaded (scope: D3). Existing modules are re-released; no record is rewritten or upgraded (primitives contract section 9). `dev` keeps the explicit opt-in of #197, and it admits only loading **without artifact authentication**: it never bypasses the verification of a bad record, and it never produces an authenticated capability grant.

**4.6 Instance identity is immutable, its authority is revocable.** An instance keeps the identity it was created with (module id, artifact, binding). What it is *allowed to do* is decided at each call and can be withdrawn:

| Event | Authority of the earlier instance |
|---|---|
| a new manifest is loaded | DENY |
| a different artifact is loaded | DENY |
| a reload fails | DENY |
| the grant is revoked | DENY |
| the binding is unchanged and the grant active | ALLOW, within the rules |

An A -> B -> A life cycle is tested apart: restoring the earlier identity does not by itself re-authorize an instance that was denied in between. (This replaces the earlier wording "keeps its authority across a manifest change", which contradicted the fail-closed behaviour of #155.)

**4.7 The verified bytes are the executed bytes.** (a) The instance is built from the WASM bytes the verifier checked; (b) the grant is built from the descriptor the verifier checked; (c) nothing is re-read from the file system after verification; (d) the record's paths map to the loaded files exactly, with no file-name fallback; (e) the grant becomes active only after the whole verification and the successful storing of the artifact. Today the WASM is read once in `loadModule` and the same bytes feed the digest check and the descriptor; the descriptor and the `.pem` are read by their callers. The design keeps one read per file and reuses those bytes.

## 5. Bridge map

concept (this document) -> code: verifier, binding key, policy and the `cic-flow` artifact binding do not exist, and the module release tooling cannot emit an envelope 3 record (**bridge breaks here, on both the relay and the producer side**) -> runtime: the loader reads the module triple only -> audit: the load log records the certificate subject, not a verified `build_hash`. Existing, reusable links: `verifyCICFlowProvenance` (anchor), `cicFlowRegistry` (grant and instance binding), the primitives vectors and reference verifier.

## 6. Decisions requested from the owner

Recommendations below are those of the architectural review of #200; each awaits the owner's confirmation.

| # | Decision | Recommendation |
|---|---|---|
| D1 | Where the record lives | a separate `<name>.release.yaml` |
| D2 | Fate of the `.pem` | the release record is authoritative; a legacy `.pem` that contradicts it is DENY |
| D3 | Scope of "no fallback" in `prod` | every module, without exception |
| D4 | What authorizes a signer for a module name and capability set | a declared, authenticated authorization policy (not a certificate subject/SAN pattern). Open inside it: who signs the policy and where it lives |
| D5 | Time | load-time validity is acceptable as a first step; the gap is recorded |
| D6 | Verifier | Go implementation, shared test vectors, Python reference verifier as oracle |
| D7 | Revoking a single release | a separate issue; the missing guarantee is documented |

The `dev` opt-in admits only loading without artifact authentication. It never bypasses a bad-record check and never results in an authenticated capability grant.

## 7. Out of scope, and what this survey did not verify

Out of scope: the load-time check of the WASM binary's actual import list, re-introducing `git.push`, the Oracle end-to-end integration. Verified in this revision: the real `primitives/@v0.2.1` envelope 3 record passes `record` and `crypto` under the reference verifier. Not verified: a module-level record (the module tooling cannot produce one, section 3); the exact semantics of the CICSourceCA countersignature beyond `release.schema.yaml`; whether the Oracle release run produces a real signed record.
