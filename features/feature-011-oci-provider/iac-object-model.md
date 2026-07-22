# IaC object/field model (feature-011)

Status: **concept / spec**. Distilled from `thread-object-model.txt` (2026-07-20),
which hardened the field model against Kubernetes SSA, Terraform, and Crossplane
prior art and absorbed an external architecture review. The guiding conclusion
(from the thread's own synthesis): **do not redesign the concept — specify its
boundaries and internal layers, and keep the full engine off the PoC critical
path.**

## The core (already built)

Every parameter is, at bottom, a rich descriptor; the scalar is sugar:

```
key: VALUE  ≡  key: { $value: VALUE, $cic: { … } }
```

`core/nexus/iac/field.go` implements the base — `Field{Value, Default, Mode}` with
`ExpandField` normalizing the three equivalent forms to one canonical value
(determinism). **The `mode` set (`read` / `write` / `implemented` / `visible`) is
confirmed by the thread** and is the field-level capability/status surface —
including the CIC three-status (`implemented`) at field granularity.

## The layered `$cic` (refined target)

`$value` (business data) stays strictly separate from `$cic` (metadata). `$cic` is
**not a flat bag** — it is layered:

```yaml
key:
  $value: VALUE
  $cic:
    schema:                       # WHAT is it
      field_id: cic:namespace:object:field   # STABLE identity, survives path rename
      ref: cic://schemas/…
      digest: sha256:…
    behavior:                     # HOW it works   ← field.go is this layer's core
      default: false
      mode: { read: true, write: true, implemented: true, visible: true }
      compare: { type: exact }    # semantic equality (Terraform-style)
      sensitive: { type: none }   # secret custody/placement policy
    access:                       # WHO
      acl: { inherit: true }
    collection:                   # STRUCTURE
      topology: atomic            # atomic | set | map(keys: […])
```

### Object-level indices (NOT per-field)

Kept out of the field tree, at object/envelope level:

- **`managedFields`** — SSA-like multi-writer ownership (`manager`, `operation`,
  `fields[]`); who currently owns/claims a field, conflict detection.
- **`observation`** — coverage as an explicit, signable fieldset:
  `observed` / `authoritative_absent` / (implicit) `unobserved`.
- **`proof`** — `schema_digest`, `conformance_plan_digest`, `observation_digest`,
  `object_digest`, `signature`.

## Intent ↔ State — the tension resolved

Same canonical type system for both; **intent = partially-filled**, **state =
observed + provider-extended**. Not two hand-maintained schemas, and **no
hand-written ConformanceSurface** — the conformance plan is **compiled from the
schema's behavior annotations** (mode/compare/collection), and its digest goes in
the proof so a verdict cites the rule-set that produced it. The `mode.read/write`
split is the field-level expression of the intent(config)/state(observed) sides.

## Prior art (mechanisms borrowed; the composition + trust model is CIC)

| Source | What CIC takes | Maps to |
|---|---|---|
| Terraform | attribute behavior (Required/Optional/**Computed**/Sensitive), semantic equality | `behavior.mode`, `behavior.compare` |
| K8s **SSA** managedFields | multi-writer ownership, conflict, omission, list **collection topology** (atomic/set/map) | `collection`, `managedFields` |
| Crossplane | late-initialization (provider-filled values back into spec) | provider-ID re-lift into intent |
| POSIX / NFSv4 ACL | hierarchical access precedent | `access.acl` |

None of these *together* give field-level ACL partner-projection, multi-management
secret placement, intent/state branches, a schema-object graph, provider-binding
proof, and cryptographically-proven verdicts. **The mechanisms are prior art; the
composition and trust model are CIC's.**

## Five specs to write before the general engine (the review's load-bearing gaps)

These are named-but-unspecified today; the engine will diverge exactly here:

1. **CIC Canonical Object Encoding v1** — number normalization (`1` vs `1.0` vs
   `1e0`, `-0`), map key order, set order, null vs absent, URI/path normalization,
   schema digest. Language-neutral, with **Go + Rust test vectors** (JCS / RFC 8785
   or deterministic CBOR as a base — a CIC profile, not an ad-hoc format).
2. **ACL evaluation algorithm** — pick **one** formal model (not POSIX + NFSv4 +
   RBAC mixed). v1: default-deny + owner/group/other + named allow + mask +
   inherit; **no explicit deny yet** (that is a `cic-acl/v2`).
3. **Collection topology / list-element identity** — atomic / set / map(keys);
   element identity = `field_id + collection key` (e.g.
   `cic:compute:vm:network-interfaces/{name=nic-0}`); drives merge, ownership,
   drift, deletion, and proof paths.
4. **Observation completeness** — `observed` / `authoritative-absent` /
   `unobserved`; the verdict distinguishes `CONFORMANT` / `DRIFT` /
   `OBSERVED_ABSENT` / `UNOBSERVED` / `NOT_COMPARABLE`, so it never claims
   conformance for a field the observe never saw.
5. **Schema evolution + stable field identity** — `field_id` survives a path
   rename; ownership / ACL / proof / observation key on `field_id`, not the path.

## Secret custody (from the thread's `sensitive` discussion)

`behavior.sensitive` is a **placement/custody policy, not an ACL**: `exposure`
(`local` = plaintext only in the local custody boundary / `remote` = only a
capability handle) + per-management-scope `backup` levels (transit / store /
restore) + envelope encryption (ciphertext + per-scope wrapped DEK). The general
`access.acl` says *who may reference/rotate/use*; `sensitive` says *where the
secret may live*. Keep the two separate.

## Scheduling — keep the engine off the PoC critical path

- The PoC runs on the **current** drift mechanism (`reflect.DeepEqual`), not the
  full model.
- `field.go` is the validated **foundation** (the `behavior` core). Do **not** pull
  the full Node IR / ACL engine / cross-language test vectors into the PoC.
- Next slice: **one OCI vertical** — a VM + 2 NICs + 2 subnets + provider ID +
  read/write/computed fields + one map-topology collection + observation coverage +
  a simple ACL projection. Prove the model on that.
- Then Go + Rust common test vectors; only *then* a general Node IR migration.

## How the current code maps

`core/nexus/iac/field.go` `Field{Value, Default, Mode}` is the `$cic.behavior` core
(value + default + mode). The remaining layers — `schema.field_id`,
`behavior.compare` / `sensitive`, `access.acl`, `collection.topology` — and the
object-level `managedFields` / `observation` / `proof` indices are the **deferred
build**, sequenced per the scheduling above. `ExpandField`'s scalar-sugar +
three-forms determinism is the base those layers wrap; the `$value`/`$cic`
namespace split is the next small refinement when the layers land.

## Implementation status (core/nexus/iac)

| Piece | Status |
|---|---|
| Field expansion base (`field.go`) — scalar↔descriptor sugar, `mode` | **landed**, tested |
| Structural validation (`validator.go`) — mode consistency, dependency + field-reference integrity | **landed** |
| Field references (`reference.go`) — the hidden object behind a parameter | **landed** |
| Collection topology (`collection.go`, spec #3) — atomic/set/map + element identity | **partial** (identity landed; merge/ownership use later) |
| Canonical digest (`digest.go`, spec #1) — determinism proven (form-equivalence + golden vector) | **landed** — number normalization (`number.go`): `4`/`4.0`/`4e0` collapse, `-0`→`0`, integer precision preserved; Rust peer `cic-canonical` mirrors the Go vectors byte-for-byte |
| Observation completeness (`observation.go`, spec #4) — coverage envelope + coverage-aware verdicts | **landed** |
| ACL algorithm (spec #2, `acl.go`) — cic-acl/v1: default-deny + owner/group/other + named allow + mask + inherit, no explicit deny; keys on `field_id` | **landed**, tested (POSIX most-specific-class-wins semantics) |
| Stable `field_id` (spec #5, `field_id.go`) — proof/ownership re-key on `field_id`, surviving a path rename; collection element id = field id + element key | **landed**, tested (rename-stability proof) |
| Secret custody (`sensitive.go`) — placement/custody policy (class / exposure / backup), separate from ACL; type boundary: a secret field holds only a `SecretRef`, never plaintext | **landed**, tested (invariant + object-wide enforcement) |
| `$value`+`$cic` namespace split + rich `ValueKind` / `ObjectRef` IR (spec #5 cont., `node.go`) — discriminated `Value` (8 kinds), `Node{Value, Meta}` split, type-level secret boundary, canonical digest + `FromRaw`/`Raw` interop | **landed** (the IR type + interop); migrating the `interface{}`-based layers onto it is the deferred final round |
| Intent↔state comparator (Task 1, `compare.go`) — exact / numeric; produces `ClassifyField`'s `matched`; `NOT_COMPARABLE` on kind mismatch | **landed**, tested (schema-compiled plan still deferred) |
| OCI vertical slice (`conformance.go`) — object verdict over VM + 2 NICs + 2 subnets + map collection + observation | **landed**, tested (CONFORMANT / DRIFT / NOT_COMPARABLE / UNOBSERVED / OBSERVED_ABSENT proven end-to-end) |
| Conformance plan compiled from schema behavior annotations (`schema.go`) — `mode.write` = config/state split, `behavior.compare` = comparator, `collection` = topology | **landed**, tested (compiled plan reproduces the hand-built verdict; read-only fields excluded) |
| Drift-engine rewire onto the new model (`core/nexus/drift` → `core/nexus/iac`) | **landed** — `drift.Analyze` classifies via `iac.Evaluate`; extra observed keys no longer drift, empty+empty is NONE; recorder verdicts unchanged |

Per the scheduling above, this is the base off the PoC critical path; the OCI
vertical proves it, and the general Node IR / cross-language test vectors come last.

### RepoWise symbol-graph audit (2026-07-21)

Cross-checked the table against a whole-repo RepoWise index (690 files, full symbol
graph), not file reading. Findings:

- **Landed rows confirmed** as symbols in `core/nexus/iac`: `ExpandField` /
  `FieldMode` (`field.go`), `Observation` / `CoverageState` / `ClassifyField` /
  `FieldVerdict` (`observation.go`), `Collection` / `CollectionTopology` /
  `ElementKey` (`collection.go`), `IaCValidator` (`validator.go`), `Reference`,
  `Digest`.
- **Concept rows confirmed empty repo-wide** — the annotated symbols above return
  *no* match anywhere in the codebase (not just outside `iac`). `Node` / `Value` /
  `SecretRef` exist only in the Rust `connections/` and `pkg/merkle`, i.e. **not**
  the IaC object model.
- **The surface layer** (`ConfigSurface` / `StateSurface` / `BindingSurface` /
  `ManagedEntity`) the thread calls "already built" is **not in this repo** — it
  lives in the separate `schemas.tar` / `cic-module-oracle-cloud` artifact.
- **The bridge is absent.** The existing drift engine is `core/nexus/drift`
  (`SoftDrift` / `HardDrift` / `ReconciliableDrift`, the `DeepEqual` classifier the
  scheduling above keeps on the PoC path). At audit time there were **0 graph edges
  between `core/nexus/iac` and `core/nexus/drift`** — the migration had not started.
  **(Update: closed — `drift.Analyze` now classifies through `iac.Evaluate`; the
  edge exists and the DeepEqual classifier is retired.)**

Net: the foundation third (field-mode + observation-coverage + verdict +
digest/collection/reference/validator) is landed and tested; the upper two-thirds
(conformance contract, comparator plan, rich Node/Value IR with secret+ACL
invariants, and the drift-engine rewire) is concept with no code counterpart. The
`iac` package is imported by `cmd/relay/main.go`, but the feature-011 symbols do not
yet drive a live conformance verdict.

## Reference

`thread-object-model.txt` (raw) — the field-wrapping model, the secret
custody/backup-placement policy, and the SSA / Terraform / Crossplane comparison
with the external architecture review.
