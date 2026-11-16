# Primitives and Forms fit and gap survey for the release record and the authorization policy (#162)

Status: **concept (survey).** It reads and measures; it adds no schema, no grammar, no code and no validation architecture, and it changes neither cic-primitives nor Forms. It follows [module-release-record-plan.md](module-release-record-plan.md) (merged in #211) and answers the question that plan left open: how much of the release record and of the authorization policy the existing CIC machinery already covers, and what is truly missing. Relay implementation, the base-repo emitter, the Oracle end-to-end test and production approval stay on HOLD.

## 1. Rule of the survey

No second Form, no second validation system, no parallel grammar. A missing **general structural** capability is a finding for Forms, not for the Relay or a release compiler. A finding here is a gap only when no existing component covers it; "exists but not applied to this document kind" is a different row.

## 2. Sources and what was run

Fresh shallow clones, `main` unless stated: `cic-primitive-forms` `2f02a75`, `cic-primitive-engine` `a6a0680`, `cic-schema-registry` `ac567ae`, `cic-primitives` `devel` `01cb30b` with the tag `primitives/@v0.2.1`.

Run: the primitives reference verifier `tools/release_record_conformance.py` on the two release records in the `v0.2.1` tree; a PyYAML parse of a mixed-scalar sample; `openssl` on the certificates inside the `v0.2.1` record; keyword searches across the four repositories. **Not run:** no Forms build or test, no `compile_schema` of any record schema, no engine build. Statements about what Forms can express are read from its documents and are marked as such.

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
| G3 | Describe the record structure in the CIC schema language (Forms Shapes) | Forms validates against a pinned CIC schema with `string`, `integer`, `boolean`, list with `min_items`, composite `fields`, opaque; mandatory/optional/nullable; `UNKNOWN_FIELD`; list-element identity by declared key; positions and paths on every error. | concept (read, not run) | Structure (nesting, mandatory members, unknown members, list identity of `artifacts.files` by `path`) looks expressible. Value constraints do not: `kind` const, `envelope` equals 3, 40-hex `source_commit`, `sha256:` form, sorted order, manifest consistency. I found no pattern, enum or const in the supported subset. Those checks live in the verifier's check table and stay there. **No Forms change is proposed**; the owner's rule is that Forms changes need a proven gap, and none is proven. Whether `compile_schema` accepts such a schema is untested. |
| G4 | The bytes that were authenticated are the bytes that are read | Forms P5 admission contract: admit only when permitted by the normative table, read the same by the readers relied on, and provenance-verified; the reader runs on the verified bytes (decision D8). | concept (contract), not wired to releases | Directly applicable. This is the correct front door for the Relay's record reading. |
| G5 | A closed value domain for a record | The `v0.2.1` record holds only `str`, `dict`, `list`, `bool`, `None` and `int` (measured: 842, 333, 72, 34, 1, 6); timestamps and validity dates are quoted. The contract does not state a value domain. | gap (small) | See section 5: the domain is feasible but not stated, and the hash payload uses `default=str`, which would hide a typed scalar. |
| G6 | Identity of list elements | Forms `PathOf` names a list element by its declared key; the verifier today checks path duplicates by itself. | implemented (Forms), duplicate check implemented (verifier) | No new capability. |
| G7 | Normalization and defaults | Forms `normalize` keeps the origin of every value and `denormalize` returns the authored document. | implemented | Usable as a read-side view only. A normalized document must never replace the originally signed bytes: digest and signature are always over the authored bytes. |
| G8 | Effective policy resolution | `PolicyResolution` yields the effective `access`/`modify` rule of positions, as data. Its design states: no allow or deny, no CertPattern matching, no authentication. | implemented, for another purpose | It resolves rules for fields of managed objects. It is not a release-authorization resolver, and it does not decide. The Relay decides. |
| G9 | A policy family for who may sign and countersign | Forms `P5-TRUST-MODEL-PLAN` section 7 already defines a policy kept outside the record and outside the signers' control, with rows: anchors, release signers, countersigners, a both-required and distinct-identity rule, time evidence, revocation, algorithms. Owner decisions T1 to T8. | plan (concept) | The `ReleaseAuthorizationPolicy` of the plan must not be a new parallel document. It is the module-scoped extension of this trust policy. What is new is only the module scope and the capability ceilings. |
| G10 | A schema for `capability_manifest` | The keyword is found in none of the four repositories. It exists only in the Relay and in the module `project.yaml` files. | gap | Ceilings cannot be written or validated against a CIC schema until the capability manifest has one. It is a type-specific schema (registry), not a Form feature. |
| G11 | Match a certificate to a CertPattern | `IsCertPattern` checks syntax only. No matcher in Relay, Forms, primitives or the engine. | gap | New code, in the Relay work package. Not a Form capability (it needs a verified certificate, which Forms must not hold). |
| G12 | A digest of the policy to record with a grant | The engine digests materialized objects (semantic). The release record digests raw artifact bytes. | two digest worlds | The policy digest bound to a grant is the digest of the **authenticated policy bytes** (its `artifacts` entry). An engine semantic digest may be an additional field, never a substitute. |
| G13 | Time and chain validity | Forms trust plan section 1 to 5: every certificate of the present chain ends 2026-12-31; the verifier rejects the chain from 2027-01-01; evidence of time must exist before the expiry; the countersigned payload is the constant `build_hash`, so it carries no time. | concept (plan), **deadline** | Re-measured here from the `v0.2.1` record: developer certificate `notAfter` 2026-12-31 13:22:59 GMT, Source CA 2026-12-31 13:22:39 GMT. Today is 2026-11-16, **45 days** before. See section 6. |
| G14 | Old records stay verifiable | The reference verifier refuses an envelope 1 record (`v0.1.5`) with `envelope_too_old`, by contract; the `v0.2.1` record passes `record` and `crypto`. The `blobs` layer was `NOT_RUN` (the source commit is not in the shallow clone). The `v0.2.0` record is not in the `v0.2.1` tree and was not run. | implemented | "Unchanged verifiability" means: envelope 1 and 2 stay verified by the compiler's own path, envelope 3 bytes and the verifier's verdicts stay identical. The generalization needs a golden vector for each existing record. |

## 5. The parser boundary, measured (G4, G5)

The record hash is computed over a Python value tree (`json.dumps(sort_keys=True, separators=(",",":"), default=str)` over what PyYAML returned). Forms reads a deliberately smaller YAML subset and treats every plain scalar other than `null`, `true`, `false` and 64-bit decimal integers as a string, and it refuses floats, hex, octal and leading-zero integers (`UNSUPPORTED_SYNTAX`). The two readings differ. A sample, parsed with PyYAML `safe_load` and canonicalized as the compiler does:

| Input scalar | PyYAML then canonical JSON | Forms |
|---|---|---|
| `1.0` | number `1.0` | refused (`UNSUPPORTED_SYNTAX`) |
| `0x10` | number `16` | refused |
| `1_000` | number `1000` | a string |
| `yes` | `true` | the string `yes` |
| `2026-11-02T18:57:58+00:00` unquoted | the string `2026-11-02 18:57:58+00:00` (a space, via `default=str`) | the string with `T` |
| `1e3` | the string `1e3` | refused |

So an unquoted timestamp or a `yes` would give different canonical bytes in PyYAML and in a strict reader, and `default=str` turns a typed date into a text that was never in the file. The published record avoids all of this by quoting, and the measured value domain is closed, so the problem is real but bounded. The proposal is therefore small and belongs to the contract and the verifier, not to Forms:

* The record contract states the value domain (string, integer, boolean, null, list, mapping) and says that every other scalar type is refused by the verifier, before hashing.
* The emitter writes quoted strings.
* The Relay's reader of records and policies is run on the verified bytes under the Forms admission contract; whether the Relay imports the Forms Go library or implements the same admitted subset is a decision for the Relay work package.

## 6. Scheduling fact for the Oracle PoC (G13)

Both signing certificates of the existing chain end on 2026-12-31 (measured above). A record signed under it stops being accepted by a verifier that checks validity at "now" from 2027-01-01. The Relay design (D5) checks validity at load time, so every module signed under this chain stops loading then unless the chain is renewed or an as-of rule with preserved time evidence is adopted. The trust plan states that evidence of existence before the expiry cannot be made after it. These are owner decisions T3, T4 and T6 of that plan. For #162 this means: the Oracle PoC and any test record must be signed with a chain that outlives the test, or use test certificates (the plan already uses test-keyed records for the vectors). It is a scheduling constraint, not a design change.

## 7. What this survey proposes

1. **Contract generalization (cic-primitives, the next implementation PR once cleared):** a common envelope part (the members of contract section 4, the hash rule, the verifier layers), a stated value domain (section 5), per-kind schemas (`PrimitiveRelease`, `ModuleRelease`, and the policy kind), and one golden vector for each existing record that proves its verdict does not move. No change to the hash mechanism.
2. **No change in Forms**, because no general structural capability was proven missing. Forms is used where it already fits: the admission front door (G4), list identity (G6), and a read-side normalized view (G7).
3. **D4 as an extension of the trust policy of Forms section 7** (G9), not a new document family: add module scope and capability ceilings; keep the anchors, signers, countersigners, both-required and time rows as they are. The Relay decides; Forms resolves only what is data.
4. **Registry:** a schema for `capability_manifest` (G10) before any ceiling can be validated.
5. **Relay work package** (still HOLD): the CertPattern matcher (G11), the record reader on admitted bytes, the policy digest bound to the grant from the authenticated policy bytes (G12).
6. **Owner decisions to raise now:** T3 (time evidence for the present chain before 2026-12-31 13:22 UTC), and whether the Relay imports Forms.

## 8. Not verified

No Forms or engine test was run. That `compile_schema` accepts a record schema was not tried. Whether `PolicyResolution` can be applied to a policy document with the existing atoms was not tried; it was read as designed for object fields. The `v0.2.0` record and the `blobs` layer on the real source commit were not run. No search outside the four repositories was made for a `capability_manifest` schema. The count of `capability_manifest` occurrences in the Relay and the Oracle module was not re-measured here. The Forms trust plan is dated 2026-11-02; only the two certificate dates were re-measured.
