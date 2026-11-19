# Primitives and Forms fit and gap survey for the release record and the authorization policy (#162)

Status: **concept (survey).** It reads and measures; it adds no schema, no grammar, no code and no validation architecture, and it changes neither cic-primitives nor Forms. It follows [module-release-record-plan.md](module-release-record-plan.md) (merged in #211) and answers the question that plan left open: how much of the release record and of the authorization policy the existing CIC machinery already covers, and what is truly missing. Relay implementation, the base-repo emitter, the Oracle end-to-end test and production approval stay on HOLD.

## 1. Rule of the survey

No second Form, no second validation system, no parallel grammar. A missing **general structural** capability is a finding for Forms, not for the Relay or a release compiler. A finding here is a gap only when no existing component covers it; "exists but not applied to this document kind" is a different row.

## 2. Sources and what was run

Fresh shallow clones, `main` unless stated: `cic-primitive-forms` `2f02a75`, `cic-primitive-engine` `a6a0680`, `cic-schema-registry` `ac567ae`, `cic-primitives` `devel` `01cb30b` with the tag `primitives/@v0.2.1`.

Run: the primitives reference verifier `tools/release_record_conformance.py` on the two release records in the `v0.2.1` tree; a PyYAML parse of a mixed-scalar sample; `openssl` on the certificates inside the `v0.2.1` record; keyword searches across the four repositories. Also run (review F3): the Forms fit probe of section 5.1, a representative `ModuleRelease` schema through the Forms Rust and Go libraries (`docs/evidence/forms-fit-probe/`). **Not run:** the Forms test suites, an engine build, any record schema other than the probe's. Statements about what Forms can express beyond the probe are read from its documents and are marked as such.

## 3. Component boundaries as found

| Component | What the documents say it does | Applies to a release record or policy today? |
|---|---|---|
| cic-primitives | Atoms and aggregates, the access grammar `access-policy/1`, the compiler, the release record contract (envelope 3), the reference verifier | The record contract and the verifier: yes. The record schema `release.schema.yaml` is **JSON Schema** and is written for `PrimitiveRelease` only. |
| cic-schema-registry | Consolidated schema repository | No release-record or policy schema was found there. |
| Forms | Parse, emit, schema-bound validation against a pinned CIC schema, expand/collapse, normalize with origin kept, effective access/modify rule as data (`PolicyResolution`), diff, patch, the P5 raw-YAML, admission, trust-model and release-origin contracts | Not applied to a release record. Its own README: "no authorization decisions". |
| Primitive Engine | Materialization, the conformance verdict, canonical bytes and digests of a **materialized object** | No. Its digests are semantic digests of materialized objects, not the release `build_hash`. |
| Release verifier (primitives) | Layers `record`, `blobs`, `crypto`; reason identifiers | Yes. Trust gates are explicitly out of its scope. |
| CICSourceCA service | Countersigns `build_hash`; the `workflow` is a caller-supplied string and not allowlisted | It is **not** authorization-bound today (see the plan, section 6). |
| Relay | Loads modules; decides trust, grants capabilities, executes | Holds no CertPattern matcher and no record verifier yet. |

## 4. Gap analysis

Status words: **implemented**, **scaffold**, **concept**, **gap** (nothing covers it).

| # | Needed | What exists | Status | Finding |
|---|---|---|---|---|
| G1 | A hash and verification rule that is common to all record kinds | `_release_hash_payload` for envelope 2 and 3 excludes only `release.sign`, `release.build_hash` and `cic_countersign`; it does not look at `kind` or `specs`. The verifier reads `envelope`, `source_commit`, `artifacts`. | implemented | The mechanism is already kind-agnostic. Nothing to build. |
| G2 | A record **schema** common part plus one schema per kind | `release.schema.yaml` requires `kind` const `PrimitiveRelease` and non-empty `specs`; its envelope 3 conditions repeat inside it. | gap | Split the schema into a common part and per-kind parts in cic-primitives. This is the contract change the review asked for. |
| G3 | Describe the record structure in the CIC schema language (Forms Shapes) | A representative `ModuleRelease` schema compiled with `compile_schema` and ten documents validated by the Forms Rust and Go libraries (the probe, section 5). | **measured** (probe) | Structure is expressible and the two implementations agree on all ten cases, byte for byte. Value constraints are not expressible (`kind` const, `envelope` equals 3, 40-hex `source_commit`, `sha256:` form, order, manifest consistency): none exists in the supported subset, and those checks stay in the verifier. **No Forms change is proposed**; none is proven necessary. The probe is architectural evidence, not a production implementation, and it ran under the development grammar pin (section 5). |
| G4 | The bytes that were authenticated are the bytes that are read | Forms P5 admission contract: admit only when permitted by the normative table, read the same by the readers relied on, and provenance-verified; the reader runs on the verified bytes (decision D8). | concept (contract), not wired to releases | Directly applicable. This is the correct front door for the Relay's record reading. |
| G5 | The record is read identically by every reader that matters | The `v0.2.1` record holds only `str`, `dict`, `list`, `bool`, `None` and `int` (842, 333, 72, 34, 1, 6) and quotes its timestamps and dates. The contract does not state how a record is admitted when readers differ. | gap | A closed value domain does not close it (5.2): the interpretation identity of the readers must be required by the record layer. |
| G6 | Identity of list elements | Forms `PathOf` names a list element by its declared key; the verifier today checks path duplicates by itself. | implemented (Forms), duplicate check implemented (verifier) | No new capability. |
| G7 | Normalization and defaults | Forms `normalize` keeps the origin of every value and `denormalize` returns the authored document. | implemented | Usable as a read-side view only. It must never replace the bytes or the tree that a digest or a signature covers. Section 6 separates the three digest layers. |
| G8 | Effective policy resolution | `PolicyResolution` yields the effective `access`/`modify` rule of positions, as data. Its design states: no allow or deny, no CertPattern matching, no authentication. | implemented, for another purpose | It resolves rules for fields of managed objects. It is not a release-authorization resolver, and it does not decide. The Relay decides. |
| G9 | A policy family for who may sign and countersign | Forms `P5-TRUST-MODEL-PLAN` section 7 already defines a policy kept outside the record and outside the signers' control, with rows: anchors, release signers, countersigners, a both-required and distinct-identity rule, time evidence, revocation, algorithms. Owner decisions T1 to T8. | plan (concept) | The `ReleaseAuthorizationPolicy` of the plan must not be a new parallel document. It is the module-scoped extension of this trust policy. What is new is only the module scope and the capability ceilings. |
| G10 | A schema for `capability_manifest` | The keyword is found in none of the four repositories. It exists only in the Relay and in the module `project.yaml` files. | gap | Ceilings cannot be written or validated against a CIC schema until the capability manifest has one. It is a type-specific schema (registry), not a Form feature. |
| G11 | Match a certificate to a CertPattern | `IsCertPattern` checks syntax only. No matcher in Relay, Forms, primitives or the engine. | gap | New code, in the Relay work package. Not a Form capability (it needs a verified certificate, which Forms must not hold). |
| G12 | A digest of the policy to record with a grant | The engine digests materialized objects (semantic). The release record digests raw artifact bytes (`artifacts.files[].digest`) and hashes the canonical record tree (`build_hash`). | three digest layers | The digest bound to a grant is the **artifact digest of the authenticated policy bytes**, the first layer of section 6. A semantic digest may be an additional field, never a substitute. |
| G13 | Time and chain validity | Forms trust plan sections 1 to 6: the certificates of the present chain end on 2026-12-31 (exact times in section 7); a renewal does not make old signatures valid; evidence that something existed before the expiry has to be created before it; the countersigned payload is the constant `build_hash`, so it carries no time. | concept (plan), **deadline** | Section 7. |
| G14 | Old records stay verifiable | The reference verifier refuses an envelope 1 record (`v0.1.5`) with `envelope_too_old`, by contract; the `v0.2.1` record passes `record` and `crypto`. The `blobs` layer was `NOT_RUN` (the source commit is not in the shallow clone). The `v0.2.0` record is not in the `v0.2.1` tree and was not run. | implemented | "Unchanged verifiability" means: envelope 1 and 2 stay verified by the compiler's own path, envelope 3 bytes and the verifier's verdicts stay identical. The generalization needs a golden vector for each existing record. |

## 5. The probe and the parser boundary, measured (G3, G4, G5)

### 5.1 The Forms fit probe

A representative `ModuleRelease` schema (`docs/evidence/forms-fit-probe/schema.yaml`: nested composites, mandatory members, a list `artifacts.files` keyed by `path`, `min_items`) was compiled with `compile_schema`, and ten documents derived from the published `v0.2.1` record (identity and certificates replaced by placeholders) were validated and addressed with `path_of`, by the Rust and the Go library of `cic-primitive-forms` `2f02a75`. The Go and Rust outputs are identical, byte for byte (`expected-output.txt`).

| Case | Result, Rust and Go alike |
|---|---|
| schema compile | OK |
| 01 well-formed document | valid; `path_of` names the list element by identity: `$.artifacts.files[{path="dependency.yaml"}]` |
| 02 `provenance.source_commit` missing | `MISSING_MANDATORY` at `$.provenance.source_commit` |
| 03 an unknown member inside `provenance` | `UNKNOWN_FIELD` at `$.provenance.grammar_sha256` |
| 04 a top-level `specs` | `UNKNOWN_FIELD` at `$.specs` |
| 05 two `files` entries with the same `path` | `PATH_DUPLICATE_IDENTITY` at `$.artifacts.files[{path="LICENSE.md"}]` |
| 06 `envelope: '3'` (a quoted string where an integer is declared) | refused: `UNSUPPORTED_SYNTAX` (an error, not a violation list) |
| 07 `files: []` | `MIN_ITEMS_NOT_MET` at `$.artifacts.files` |
| 08 `timestamp: 1.5` | refused at parse: `UNSUPPORTED_SYNTAX` |
| 09 `timestamp: yes` | **valid**: read as the string `yes` |
| 10 `timestamp: 2026-11-02T18:57:58+00:00` unquoted | **valid**: read as a string with `T` |

Cases 02 to 08 show the probe can fail; 01 shows it can pass. What it proves: the structural part of a record kind is expressible in the CIC schema language, the unknown-member refusal, list identity with duplicate detection and `min_items` work, and the two implementations agree. What it does not prove: value constraints (none exist), production use, or that the real `ModuleRelease` schema will compile (the probe schema is a minimum). **Caveat:** `compile_schema` accepts only the grammar profile `cic-primitives-73ae469-6a188078`, a development commit of cic-primitives that the Forms source itself calls a temporary compatibility profile, not a release. A record schema built on Forms is therefore bound to a non-release grammar pin until Forms carries a released profile.

### 5.2 Cases 09 and 10: why a closed value domain is not enough (review F1)

The record hash is computed over a Python value tree (`json.dumps(sort_keys=True, separators=(",",":"), default=str)` over what PyYAML returned). Forms reads a deliberately smaller YAML subset: every plain scalar other than `null`, `true`, `false` and 64-bit decimal integers is a string, and floats, hex, octal and leading-zero integers are refused. PyYAML sample, canonicalized as the compiler does:

| Input scalar | PyYAML then canonical JSON | Forms |
|---|---|---|
| `1.0` | number `1.0` | refused (`UNSUPPORTED_SYNTAX`) |
| `0x10` | number `16` | refused |
| `1_000` | number `1000` | a string |
| `yes` | `true` | the string `yes` |
| `2026-11-02T18:57:58+00:00` unquoted | the string `2026-11-02 18:57:58+00:00` (a space, via `default=str`) | the string with `T` |
| `1e3` | the string `1e3` | refused |

An earlier draft of this survey proposed to state a closed value domain (string, integer, boolean, null, list, mapping). **That is not sufficient**, as the reviewer showed: `yes` and `1_000` are inside the domain and are still read differently (a boolean and an integer against two strings), and the probe confirms that Forms alone admits them (cases 09 and 10 are valid). Stating the domain and trusting the emitter to quote is also not a defence, because the verifier must hold even for a record written by someone else.

The admission condition is therefore the three-part condition of the Forms admission contract, with the second part made explicit and checked by the verifier: **a record is admitted only when it is permitted, read identically by the readers relied on, and provenance-verified** (`P_allowed` and `P_parity` and `VerifiedProvenance`).

* **Interpretation identity (`P_parity`).** The record layer of the verifier reads the record with the reader that computes `build_hash` and with a second, independent reader (the Forms reader, Rust or Go), and refuses with a new reason (proposed name `scalar_reading_differs`) when the two typed trees differ in any scalar, including a scalar that is a string in one and a boolean, integer or date in the other. A plain scalar whose reading depends on the YAML dialect is thereby refused; a quoted string is read alike by all.
* **The emitter** writes quoted strings, so that its own records pass; that is convenience, never the defence.
* **Vectors.** Cases 09 and 10, `yes`, `1_000`, `0x10`, `1.0`, `1e3` and an unquoted date are conformance vectors that must be refused by the record layer, with the Python reference verifier as the oracle of difference.
* The hash computation itself does not change.

### 5.3 The Relay builds on the existing Forms Go implementation (review F2)

The Relay's reader of records and policies is the existing **Forms Go library** (`github.com/CentralInfraCore/cic-primitive-forms/go`), not a reimplementation of the same subset. A Relay-owned structural validator, a second parser or a second copy of the admitted subset is **not accepted**: it would be a second Form. A deviation is acceptable only for a **documented, measured technical obstacle** (for example an unresolvable dependency or toolchain conflict, shown by a measurement), recorded as a decision with the measurement. The import must pin an exact version of Forms, and the Forms schema-binding pin of 5.1 applies.

## 6. The three digest layers, kept apart (review F4)

An earlier line of this survey said that "signature and digest are always over the authored bytes". That is not exact for envelope 3. Three different digests exist and answer three different questions:

| Layer | Over what | Where it is checked | Answers |
|---|---|---|---|
| **Artifact digest** (`artifacts.files[].digest`, `manifest_digest`) | the **original bytes** of each released file, as stored in the git blob, with no filter | the `blobs` layer (and the Relay's single read of the loaded file) | are these the bytes that were released |
| **Release hash** (`release.build_hash`), and the signatures over it | the **canonical value tree of the record itself** (the bundle without `release.sign`, `release.build_hash`, `cic_countersign`), after the record file was parsed | the `crypto` layer | is this record, as an interpreted tree, what the signer and the countersigner signed |
| **Semantic digest** (the engine) | the **interpreted, materialized object** | the engine | what does this object mean, after interpretation |

Consequences. The signatures do **not** cover the bytes of the record file; they cover the tree a parser derived from it. That is exactly why the interpretation identity of 5.2 is a security property of the record layer and not a convenience. The authored bytes of the released artifacts are bound separately, by the artifact digests. A normalized view (Forms `normalize`) belongs to the third layer at most and never replaces either of the first two: it may be shown after admission and is never the input of a digest or a signature. The digest recorded with a grant is the first-layer artifact digest of the authenticated policy bytes (G12).

## 7. Chain validity and the 2026-12-31 expiry (review F5)

**Facts.** From the Forms trust-model plan (section 1, measured there on 2026-11-02): CIC Developer CA ends 2026-12-31 13:22:46 UTC, CIC Source CA 2026-12-31 13:22:39 UTC, the developer certificate 2026-12-31 13:22:59 UTC, the CIC Root CA 2026-12-31 23:59:59 UTC. **Re-measured here from the `v0.2.1` record:** the developer certificate ends 2026-12-31 13:22:59 GMT and the Source CA 13:22:39 GMT. The Developer CA and the Root dates are taken from the Forms plan and were not re-measured. A record needs both the release signature and the countersignature, so the first moment at which a check made at "now" fails is **2026-12-31 13:22:39 UTC** (the Source CA). The developer commitment of the repository runs to 2027-05-25 but rests on the developer certificate above. Today is 2026-11-17, 44 days before.

**What a renewal does not do.** A new certificate or a new chain does **not** make an old signature valid. An old signature verifies under the old public key and the old certificate, and only the old chain's validity window applies to it. A new chain can sign new records, and it can sign a new statement about an old record (a renewal statement); it cannot reach back. This is the position of the Forms trust plan (section 4), which this survey follows and does not restate in other words.

**What follows for a record signed under the present chain** (the plan's C1 to C6, as conditions of a verifier policy): after 2026-12-31 it is acceptable only with independent evidence that both signatures existed before the expiry (the signer's own `timestamp` and the unsigned `ts` do not count), validity judged at that proven time and not at "now", the complete chain at that time (the Developer CA is not carried in the record), a stated position on revocation, the old root pinned as a historical anchor with its window, and acceptable algorithms. The evidence has to be created before the expiry; evidence obtained later proves only "not later than" a date after the chain ended. Whether and how to do that for `v0.2.1` and for older releases is the owner's decision T3 of that plan, with its deadline.

**What follows for #162.** The Relay design checks validity at load time first. Without the as-of mode and its evidence, every module signed under the present chain stops loading from 2026-12-31 13:22:39 UTC. The Oracle PoC and any test record must therefore be signed with a chain that outlives the test, or use test certificates (the test plan already uses test-keyed records for the vectors). This is a scheduling constraint on the PoC; it does not change the design.

## 8. What this survey proposes

1. **Contract generalization (cic-primitives, the next implementation PR once cleared):** a common envelope part (the members of contract section 4, the hash rule, the verifier layers), per-kind schemas (`PrimitiveRelease`, `ModuleRelease`, and the policy kind), the **interpretation-identity condition** of 5.2 in the record layer with its vectors (not a stated value domain alone), and one golden vector for each existing record that proves its verdict does not move. No change to the hash mechanism.
2. **No change in Forms**: no general structural capability was proven missing, and the probe of 5.1 shows the structural part is expressible. Forms is used where it already fits: the admission front door (G4), list identity (G6), and a read-side normalized view (G7). New Forms capabilities stay on HOLD.
3. **The Relay uses the existing Forms Go library** (5.3). A Relay-owned structural validator is not accepted.
4. **D4 as an extension of the trust policy of Forms section 7** (G9), not a new document family: add module scope and capability ceilings; keep the anchors, signers, countersigners, both-required and time rows as they are. The Relay decides; Forms resolves only what is data.
5. **Registry:** a schema for `capability_manifest` (G10) before any ceiling can be validated.
6. **Relay work package** (still HOLD): the CertPattern matcher (G11), the record reader through Forms Go, the policy digest bound to the grant from the authenticated policy bytes (G12).
7. **Owner decisions to raise now:** T3 and T6 of the Forms trust plan (time evidence and the chain succession, before 2026-12-31 13:22:39 UTC); and the Forms grammar pin used for record schemas (5.1 caveat).

## 9. Not verified

The probe schema is a minimum, not the real `ModuleRelease` schema; it ran under the development grammar profile only. No Forms or engine test suite was run, only the probe. The reason `scalar_reading_differs` is a proposal: no verifier implements it and no vector exists yet. Whether `PolicyResolution` can be applied to a policy document with the existing atoms was not tried; it was read as designed for object fields. The `v0.2.0` record and the `blobs` layer on the real source commit were not run. No search outside the four repositories was made for a `capability_manifest` schema. The count of `capability_manifest` occurrences in the Relay and the Oracle module was not re-measured here. The Forms trust plan is dated 2026-11-02; only the two certificate dates named in section 7 were re-measured. The effect of importing the Forms Go library on the Relay's dependency and release pipeline was not measured.
