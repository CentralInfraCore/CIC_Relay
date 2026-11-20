# Module release record emitter and the D4 policy survey (#162, work package 1)

Status: **concept.** Nothing here is implemented. This document is the plan for review before any producer code is written. It continues [artifact-authentication.md](artifact-authentication.md) (sections 3 and 6) and its [test plan](artifact-authentication-test-plan.md) (A19). The Relay implementation, the Oracle end-to-end test and production approval stay on HOLD.

## 1. What the review is asked to decide

1. The concrete design of the module release record emitter (section 4).
2. The D4 proposal: how the authorization policy can be expressed (section 6).
3. The open points listed in section 7.

Decided by the owner and not reopened: the target branch is base-repo `wasm/main` (D1), the legacy `cicSign` is left unchanged (D2), and the work is a selective adoption with its own emitter, not a port of the primitives compiler (D3). The single normative release contract is cic-primitives envelope 3 (`docs/en/RELEASE-RECORD-CONTRACT.md`, `primitives/@v0.2.1`). There is no WASM-specific envelope and no second canonicalization.

## 2. What the survey found (base-repo, read-only, fresh clones)

Status words follow the three-level scale.

| Where | Finding | Status |
|---|---|---|
| `wasm/main` (`ae79934`) | The release phases are `tools/infra.py` `ReleaseManager`: checksum plus a Vault signature over `{name, version, checksum, build_timestamp}`. `tools/finalize_release.py` is marked deprecated dead code on this path (merged by `wasm/f/finalize-deprecation`, `4b27eca`). **This corrects my earlier statement:** the whole-`project.yaml` `cicSign` of `finalize_release.py` is not the active mechanism on `wasm/main`. | implemented |
| `wasm/main` `tools/schemalib/artifact.py` | `generate_signed_artifact` writes `buildHash`, `cicSign` and `cicSignedCA.certificate` as empty placeholders ("filled by relay"). Nothing fills them. | scaffold |
| `wasm/main` `tools/verify_release.py` | An offline readiness check: schema, rebuild against `buildHash`, ABI exports, `MANIFEST.sha256`. Provenance fields are reported `OK/TBD/MISSING` only, with no signature verification. | implemented, explicitly not cryptographic |
| `wasm/main` `docs/contracts/en/release-artifact.md` | States the target "provable signed release bundle" (wasm + `project.yaml` + signature, verifiable offline) and says its format is out of scope, blocked by a release-pipeline audit. **That is the item this work package delivers.** | concept |
| `wasm/f/release-contracts` (`c0ec14a`) | The contract documents and `verify_release.py`, not merged into `wasm/main` as a branch (the file exists on `wasm/main`). No envelope, `source_commit`, artifact digest or countersignature terms. | no overlap |
| `feature/release-phases-port` (core `devel` line) | `run_check`, `run_prepare_release`, `run_finalize_release`. The real close is **refused** because the finalization merges the release branch back, which the release model forbids. No envelope 3 terms. | scaffold, in flight, no open PR |
| `feature/signing-from-factory-core` (core `devel` line) | The commit and tag signing toolchain. Its tag manifest is a different digest from `artifacts.manifest_digest`. | implemented there, separate concern |
| base-repo open PRs | #41 (manifest regeneration), #42 (base version 0.8.0). Neither touches release records. | no overlap |

Conclusion: no branch builds an envelope 3 record, so the emitter does not duplicate in-flight work. Two couplings must be respected: the emitter must not depend on the `ReleaseManager` close path (it is refused until the core finalization conforms), and it must not reuse `schemalib.generate_signed_artifact`, whose placeholders belong to the legacy shape.

## 3. Reused mechanisms and how they stay equal to primitives

The emitter re-implements the primitives mechanisms in its own module, because the primitives `compiler.py` is a 1300-line monolith whose architecture is not to be ported back. Equality with the contract is not trusted: it is **tested against the primitives code and the primitives reference verifier**.

| Mechanism (primitives `tools/compiler.py` at `primitives/@v0.2.1`) | In the emitter |
|---|---|
| `_release_hash_payload`, envelope 3 rule | The whole bundle except `release.sign`, `release.build_hash`, `cic_countersign`; canonical JSON `sort_keys`, separators `(",",":")`, `default=str`. |
| `_release_artifact_path_problem` | The same path rules (contract section 7) with the same reason identifiers. |
| `_validate_release_artifact_declaration`, `_build_release_artifacts` | Declaration read from the **source commit's** `project.yaml`, resolved only against the commit tree, blob bytes with no filter, `sha256:` digests, UTF-8 byte order, `sha256sum`-format manifest digest. |
| `_vault_sign` (prehashed ECDSA), certificate bound inside the hash | Via the existing base-repo `tools/releaselib/vault_service.py`. |
| CICSourceCA countersignature over `build_hash` | Through an injected countersigner; production uses the existing CICSourceCA agent flow (`vault-rootCA-sign-agent.sh` and `cic-countersign`). |

A pin file records the primitives commit and the SHA-256 of each copied reference file, as the Relay test plan requires for the vectors.

## 4. The emitter

Location: a new module `tools/release_record/` on a feature branch `wasm/f/release-record-envelope3` from `wasm/main`. `infra.py`, `finalize_release.py` and the legacy `cicSign` are not edited.

**Sequence (the record is the last step, over frozen bytes):**

1. `make wasm.build` sets `metadata.buildHash` in `project.yaml`. The WASM and the final `project.yaml` are committed. That commit is the source commit.
2. `emit` reads the declaration, the blobs and the tree **from that commit only**. It refuses if the shipped working-tree bytes of any declared path differ from the commit's blob (`shipped_differs_from_commit`): the record must bind the bytes that are actually shipped.
3. It builds the bundle, computes `build_hash`, obtains `release.sign` and then `cic_countersign`, and writes `<name>.release.yaml` (D1). The record is not itself an artifact, so there is no circularity. It is committed afterwards, and the tag follows (release model: devel to main to tag, no merge back).
4. **Publication check (review F2).** The pre-emit check proves the record against the source commit; it does not prove what is published. After the tag and for every published copy, `verify-published` checks: the tag resolves to a commit whose tree differs from the source commit only by the record file (and no other added or changed path); every declared artifact's blob at the tag equals the digest in the signed record; and every distributed copy of an artifact (a release asset, an OCI or archive payload, a copy the Relay later loads) is byte-compared with the record's digest. Any difference is a refusal with its own reason (`tag_tree_differs`, `published_artifact_mismatch`, `published_artifact_absent`). The record may be correct while other content is published; this step is what rules that out, and it runs in the producer's release job before the release is announced.

**Declaration.** `compiler_settings.release_artifacts.paths` in `project.yaml`, exactly the key and rules of contract section 6 (explicit paths, no glob, same refusal identifiers). `project.schema.yaml` declares `additionalProperties: false` on `compiler_settings`, so the schema gains this key. The emitter adds one **producer-side** rule **on the declaration**: `module/module.wasm` and `project.yaml` must both be declared (`declaration_required_path_missing`; requirement P01 of cic-primitives #38 (merge commit `e364548`)). This is how the record binds the WASM and the complete descriptor (`abi`, `capability_manifest`) together. The same fact is also a **verifier-side** obligation on the finished record (V09, section 10): the producer rule does not replace it, and neither replaces the other. The declaration rule alone changes nothing in the contract; the verifier-side rule is a requirement of the ModuleRelease part of the release schema set (section 10).

**Record members.** `kind: ModuleRelease`, `name`, `version`, `timestamp`, `validity`, `createdBy`, `provenance{envelope:3, source_commit}`, `artifacts`, `release{envelope:3, createdBy, build_hash, sign}`, `cic_countersign`. `specs` is omitted. **`name` is a mandatory ModuleRelease-specific member** (decided in cic-primitives #38 (merge commit `e364548`), P5.1): it is not in the common envelope and a PrimitiveRelease record does not carry it; it is part of the signed identity of the release and is **bound to the identity of the authenticated module descriptor** (V08, section 10), not a mere artifact reference. No further pointer members for the descriptor or the WASM are added to the envelope: `artifacts.files` is the binding.

**Contract boundary (review F3).** The primitives envelope 3 contract and its `release.schema.yaml` are written for `PrimitiveRelease`: the schema requires `kind: PrimitiveRelease` (a `const`) and a non-empty `specs`. A `ModuleRelease` record therefore **fails the current schema**, and a pass of the reference verifier does not show schema conformance (its record layer reads `envelope`, `source_commit` and `artifacts`, and `specs` only for a duplicate check). So the emitter is blocked on a contract step, which this plan makes a precondition and not an assumption:

1. A separate cic-primitives change (owner-merged, not part of this repository) that separates the **common envelope** (`provenance.envelope`, `provenance.source_commit`, `artifacts`, `release`, `cic_countersign`, the hash rule) from the **type-specific members** (`PrimitiveRelease`: `specs` and the primitive provenance digests; `ModuleRelease`: `name` and the artifact-set rules), with one schema per `kind` that shares the common part, and with the verifier's behaviour for each `kind` stated. **Status:** the design of this separation is merged as drafts in cic-primitives #38 (merge commit `e364548`) (`docs/design/release-schemas/`: `common.yaml`, `primitive-release.yaml`, `module-release.yaml`, the composition rules and the conformance declaration). The drafts are **not final** (finalization waits for the node-name rule of the grammar, cic-primitives #39) and no derived JSON Schema exists.
2. Until the per-kind schemas are final, no `ModuleRelease` record is emitted for a consumer. The producer work may start against vectors that are marked provisional.
3. The record is validated against the per-kind schema in addition to the verifier (section 5, V1).

**Legacy separation (review F1).** Two statements, kept apart:

* **Trust.** The content of `cicSign`, `sign` or `checksum` in `project.yaml` is never a trust input. The emitter never reads it for a decision, no consumer may accept it in place of the record, and the record is not a fallback for it.
* **Binding.** `project.yaml` is bound as raw bytes, so those fields are part of the bound bytes like any other. **Changing them after the record is signed changes the descriptor digest, and the record then fails** (`artifact_digest_mismatch` in the blobs layer; the Relay refuses). An earlier draft of this plan said the opposite; that was wrong.

The sequence follows: every legacy field must be in its final form (a filled value or `TBD`) **before** the source commit. A legacy finalization that rewrites `project.yaml` after the record is signed invalidates the record, so it must not run after the record, and the emitter refuses when the working tree is not the committed bytes.

**Cross-language risk.** Python `json.dumps` over YAML-typed values is the canonicalization the contract inherits. The Go verifier (later) is held to the same bytes by shared vectors; the emitter adds a fixed corpus of tricky values (non-ASCII, nested, timestamps, floats) whose canonical bytes are asserted against the primitives function.

## 5. Proof plan (producer-side, self-contained)

Acceptance is: a record emitted by the emitter passes the primitives reference verifier `tools/release_record_conformance.py` on all three layers, **blobs included**, in a temporary git repository with a real commit. The Relay is not needed.

* **V1 (member set).** A module record without `specs`, with `kind: ModuleRelease` and a `name` passes `record`, `blobs` and `crypto`. A record without `name`, and a PrimitiveRelease record that carries a `name`, are refused (structural, observed on the unchanged Form in cic-primitives #38 (merge commit `e364548`)).
* **Positive.** Test keys are derived from fixed labels (as in the primitives vectors); a test certificate chain stands in for Vault and CICSourceCA; real `git` commits.
* **Differential.** Emitter `build_hash` and `artifacts` against the primitives `_release_hash_payload` and `_build_release_artifacts` over generated inputs; any difference fails.
* **Negative (emitter refuses).** Each declaration reason, `declaration_required_path_missing`, `shipped_differs_from_commit`, a symlink or submodule path, a path absent from the commit, a missing source commit.
* **Negative (verifier refuses emitted-then-mutated records).** One mutation per reason of contract section 11, each failing with that reason: envelope, `source_commit`, artifact digest, manifest digest, order, a changed WASM byte (blobs layer), a changed `project.yaml` byte or one `capability_manifest` entry, a swapped certificate, `build_hash`, `release.sign`, `cic_countersign`.
* **Descriptor and WASM binding.** Dropping either from the declaration is refused by the emitter; removing it from a finished record fails `build_hash`.
* **Separation (F1).** A tree with a valid `cicSign` and no record gets no record-based pass. A record over a `project.yaml` whose `cicSign` (or `sign`, `checksum`) is changed after signing **fails** the blobs layer with an artifact digest reason; a record is not rescued by a valid `cicSign`, and a valid record is not affected by the legacy value being wrong at emit time (it is only bound, never trusted).
* **Publication (F2).** A tag whose tree differs from the source commit by anything but the record file, a declared artifact changed at the tag, a distributed copy with one changed byte, and an absent copy each fail `verify-published` with the named reason; an unchanged publication passes.
* **Schema (F3).** The emitted record validates against the per-kind schema once it is final; until then the drafts of cic-primitives #38 (merge commit `e364548`) are the reference and the vectors stay provisional. A record with `kind: ModuleRelease` is expected to fail the current `PrimitiveRelease` schema (a recorded negative).
* **Mutation evidence** on the emitter: path sort, digest prefix, hash exclusion set, blob versus working tree, newline filtering, order of signing steps, each as a planted defect that a test must kill.

## 6. D4 survey: the authorization policy

**What exists.**

* cic-primitives defines `CertPattern` as a type and uses it in `Access` (`access`, `modify`) and `PolicySurface` (`default_access`, `default_modify`, `surface_rules`), grammar `access-policy/1`. These say **who may read or write fields of a managed object**, evaluated against an mTLS certificate at runtime. The pattern syntax is quoted, with `AND` inside a pattern (`"OU=adapters,O=cic"`) and `OR` across list elements.
* Forms implements `IsCertPattern` (syntax) and policy **resolution** that produces an effective rule as data (`list<CertPattern> | open | owner | none | unknown`). Its design states the limit itself: no allow or deny, no matching of a CertPattern, no owner trust or identity, no authentication.
* The Relay has no CertPattern matcher (a search of the Go sources finds none).
* `cic-countersign` accepts a caller-supplied `workflow` string and does not allowlist it; the placeholder default says countersigning is not yet scoped per workflow.

**What is missing.** Nothing in primitives or Forms says *which signer may release which module with which capability ceiling*, and nothing matches a certificate to a pattern. The existing vocabulary describes object fields, not release rights.

**Proposal.**

1. Reuse the **CertPattern grammar** for signer selection, to avoid a second pattern language. Do **not** reuse `PolicySurface` inheritance: a release rule is flat and default-deny.
2. Add a new document kind `ReleaseAuthorizationPolicy`: a list of rules `{module, signers: list<CertPattern>, ceilings: {egress, secrets, git, ...}}`. Absent rule means DENY. The `capability_manifest` of the verified module must be within the matched rule's ceilings (A10).
3. The policy is itself a **release record** of the same envelope 3, verified by the same verifier, over a policy file. One authentication mechanism, no second signature format.
4. A separate **policy-issue right**, expressed as a CertPattern pinned in the Relay's trust configuration and not obtainable from a module signature (no self-grant, A21, A22). The trust root is the CICSourceCA chain; the issuer pattern is the new decision.
5. A matcher for CertPattern against the verified signer certificate is **new Relay code** and does not exist yet. It belongs to the Relay work package, with the Forms `IsCertPattern` vectors reused for syntax.
6. **The countersigner's authority limit (review F4).** The countersign scope gap (above) is a trust gate to close before the policy is meaningful, and an allowlist of the `workflow` string is not enough, because that string is caller-supplied. The service must itself check, per request: **who** asks (an authenticated requester identity, not a free field); **which record kind** that requester is entitled to have countersigned (a requester-to-kind table held by the service); that the **content really is that kind** (the service parses the record, checks `kind`, the schema and the envelope, and does not countersign an opaque hash); and that **a module publisher cannot extend its own rights**, so the right to have a `ModuleRelease` countersigned is not the right to have a `ReleaseAuthorizationPolicy` countersigned. Policy issuance and module issuance are two trust boundaries with two kinds and two requester sets. This is a change in `cic-countersign` (a separate repository and work item); `cic-module-release@v1` may remain a label, never the authority.
7. **Policy semantics (review F5, separate work package, does not block the emitter).** To be fixed in the D4 policy specification: the exact capability-ceiling comparison (per capability class, set inclusion or numeric bound, and what "wider" means for each), the handling of overlapping rules (proposal: the most specific module match wins, equal specificity is an error and DENY), and what happens to grants already issued when the policy changes (proposal: a grant records the policy digest, a changed policy invalidates it at the next load, running instances follow the revocation rules of the design).

## 7. Open points for the reviewer

* ~~Is `kind: ModuleRelease` without `specs` acceptable~~ **Decided** (cic-primitives #38 (merge commit `e364548`)): the ModuleRelease part carries no `specs`; it has its own mandatory `name`; the common/kind separation is in the release schema set drafts.
* ~~The producer-side rule that `module/module.wasm` and `project.yaml` must be declared~~ **Decided** (P5.2): producer-side (P01) **and** verifier-side (V09), each exactly once, further artifacts allowed (section 10).
* Workflow identifier and the allowlist change in `cic-countersign`.
* Policy-issuer pattern: where it is pinned and how it is rotated.
* Whether the signer of a module and the countersigner can be the same person's key path (separation of duty).
* The per-`kind` contract change in cic-primitives (F3): the design is merged as drafts (cic-primitives #38 (merge commit `e364548`)); open: finalization (waits for cic-primitives #39), the derived JSON Schema, and whether the provisional vectors may start (they may, marked provisional).
* Whether `verify-published` also covers copies the Relay itself downloads later (F2), or only the producer's release job.

## 8. What this survey did not verify

The primitives `release_record_conformance.py` was run only on the published `v0.2.1` record, not on a module record. Whether the countersign service accepts a module-release request, and which certificate subject it would produce, was not tested. No Forms runtime was run. `wasm/f/hardening`, `wasm/f/manifest-fix` and `wasm/f/ci-followup` were not read beyond their tips. The Oracle module's own tree was not re-surveyed in this step.

## 9. Sequence

Review of this plan; the cic-primitives per-`kind` contract change (F3); then producer vectors first (each test failing before the emitter exists), then the emitter, then a base-repo PR to `wasm/main`. Relay implementation starts only after the record contract and the D4 policy contract are final.

## 10. Synchronization with the release schema set (cic-primitives #38)

Source: cic-primitives #38 (merge commit `e364548`), `docs/design/release-schemas/contract-conformance.yaml` (requirements V08, V09, P01) and `docs/en/RELEASE-SCHEMA-SET-DESIGN.md` section 7 (P5). Documentation only: nothing here changes code, a schema, a contract or a verifier. The producer and verifier obligations are kept apart:

| Id | Executor | Status there | Obligation for a ModuleRelease record |
|---|---|---|---|
| P01 | producer (the emitter) | planned | refuse a declaration that lacks `module/module.wasm` or `project.yaml` (`declaration_required_path_missing`) before any record is emitted |
| V08 | verifier | planned | the top-level `name` must equal the identity of the **authenticated module descriptor** (`project.yaml`, bound through `artifacts.files`); reason identifier `module_identity_mismatch` is a proposal of that draft, not yet part of the contract |
| V09 | verifier | planned | `module/module.wasm` and `project.yaml` must each appear **exactly once** in the authenticated artifact list; a missing or repeated one is refused (`kind_schema_violation`) **even when the record is cryptographically valid**: the signature proves the content, not the semantic completeness of the module release; further artifacts are allowed |

What the unchanged Form was observed to do with the drafts (same PR, `docs/evidence/release-schema-draft-probe/`, Rust and Go byte-identical): a missing `name` is refused, a `name` in a PrimitiveRelease record is refused; a name that differs from the descriptor identity, a wrong `kind` and a missing module artifact are **accepted** (the Form evaluates no contract), so V08 and V09 stay with the verifier, and the surface-level `must` entries are not even reported by the Form (cic-primitive-forms #167).

Not verified here: that any of V08, V09, P01 is implemented (all three are `planned`); the exact descriptor field the identity is read from (to be fixed when V08 is specified for implementation); the reason identifier `module_identity_mismatch`; the derived JSON Schema (does not exist). The Relay and base-repo implementation stay on hold.
