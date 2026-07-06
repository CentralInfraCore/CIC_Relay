# CIC Relay — feature-009: Signed, hash-chained audit log (observability output)

## Status

**concept** — the model is fully specified; the output format and pipeline are
not yet built. Its preconditions are existing **implemented** components plus one
**scaffold** dependency (the relay signing-key custody, shared with feature-008).

## Objective

Define the **output** of the relay's observability layer: a multi-level, typed,
JSON audit log where every record is **tamper-evident** and **attributable**.

> **Every log record is a link in two hash chains (its *type* and its *topic*),
> and the whole record is signed by the relay's key — so any reorder, edit, drop,
> or forgery is detectable, and every record is provably the relay's.**

This is the ProofTrace ethos applied to observability: *trust is not assumed — it
is a verifiable state*. The pipeline is generic; **meaning (which types, which
topics) belongs to the emitters** (WASM modules and the Rust libs), not to the
log infrastructure. *Core owns meaning; the pipeline owns the mechanism.*

## The record

A record is a canonical-JSON object. Fields are grouped by **who fills them**:

```jsonc
{
  // --- emitter-supplied (meaning) ---
  "type":  "countersign",        // emitter-defined log kind (chain axis #1)
  "topic": "conn:abc123",        // emitter-defined subject/stream (chain axis #2)
  "level": "info",               // trace | debug | info | warn | error
  "payload": { /* ≥1 field, redaction-safe */ },

  // --- relay-stamped (integrity; an emitter may NOT set these) ---
  "ts": "2026-07-06T12:00:00Z",  // RFC3339 UTC, relay clock
  "prev_type_hash":  "…",        // record_hash of the previous record of this type
  "prev_topic_hash": "…",        // record_hash of the previous record of this topic
  "record_hash": "…",            // see projection below
  "signature": "…"               // relay signature over record_hash
}
```

The full object carries **well over 3 fields**; `type`, `topic`, and `level` plus
the integrity set are always present.

## Two hash chains — type and topic

A record links **backward on two independent chains**, so integrity is verifiable
along either axis:

```
type chain  ("countersign"):   … ── Rₙ₋₁ ──prev_type_hash──▶ Rₙ ──▶ Rₙ₊₁ …
topic chain ("conn:abc123"):   … ── Rₖ₋₁ ──prev_topic_hash─▶ Rₙ ──▶ Rₖ₊₁ …
```

A single record `Rₙ` sits on both chains at once: its `record_hash` becomes the
`prev_type_hash` of the next record of its **type** *and* the `prev_topic_hash` of
the next record of its **topic**. The first record of a type/topic has an empty
predecessor (genesis for that scope).

Reuses `obs.TypeChainHash(name, …)` — already domain-separated by name, so the
type axis and the topic axis are the *same primitive* with a different scope name.

## RecordHash — the self-excluding projection

`record_hash = SHA-256( domain-tag ‖ canonicaljson( projection(record) ) )`

where **`projection` omits exactly `record_hash` and `signature`** (a value cannot
hash itself, and the signature is computed *after* the hash). Everything else —
including `type`, `topic`, both `prev_*_hash` fields, `level`, `ts`, `payload` —
**is** in the hash.

- `type` and `topic` **must** be signed content, or a record could be relabelled
  onto another chain.
- This is the repo's existing discipline: `metaForChecksum` already excludes
  `Checksum`/`Signature`/`Valid` (see `CLAUDE.md`). The projection **must be
  explicit**, not merely "hash before filling": a **verifier** receives a fully
  populated record and must deterministically zero `record_hash`/`signature`
  before recomputing — today's `obs.RecordHash(v any)` hashes the whole value, so
  it needs a `hashableProjection` wrapper.

## Signature — the relay's key, the relay's cert

`signature = Sign_relay( record_hash )`. Signing `record_hash` transitively signs
the whole record (the hash commits to everything but itself and the signature).

- **Sign** with the relay's key via `core/nexus/crypto` (Vault) — the same
  mechanism `core/nexus/recorder` already uses to sign audit commits.
- **Verify** against the relay's **cert**, chained to CICRoot.
- **Custody**: the relay signing key lives in Vault — this is the *same custody
  question* as feature-008 (`p71`), and shares its resolution.

## The pipeline — a serialized "conveyor belt"

The infrastructure is generic: it chains + signs whatever typed/topic-tagged
record flows through, without knowing the types or topics in advance.

```
emitters ──▶  ingest  ──▶  [ per-record, atomic ]  ──▶  sink (pkg/obs)
(modules,               read type-head + topic-head
 libs)                → stamp ts, prev_*_hash
                      → compute record_hash (projection)
                      → sign(record_hash)
                      → advance type-head AND topic-head
```

The **only** non-trivial part is **serialization/atomicity**: the
`read heads → hash → sign → advance heads` step must be atomic per record so the
chains stay linear. Concurrent records of the same type/topic must be totally
ordered (an effectively single-writer, per-scope append log). The head registries
are **dynamic** — a type/topic key is created on first sight.

## Ingest points

- **WASM modules** → a host-provided log ABI in `core/cabinet` (the module hands
  `{type, topic, level, payload}`; the host forwards to the pipeline).
- **Rust libs** → the **FFI seam's Go side** (feature-008 `pkg/cicffi` / the P3
  runtime binding); the Go wrapper emits into the same pipeline.

Both funnel into one belt; the belt chains and signs.

## Trust boundaries

1. **The relay stamps the integrity fields, never the emitter.** A (possibly
   untrusted) WASM module supplies `{type, topic, level, payload}` only. The relay
   owns `ts`, `prev_*_hash`, `record_hash`, `signature`.
2. **Redaction is the emitter's contract.** The belt signs what it is given; it
   cannot un-leak a secret. The module (the key holder) must keep `payload`
   redaction-safe — consistent with `ffi-boundary.md` rule 6 and the port layer's
   "no sensitive logging" posture.

## Verification model

Given a record `R` and the relay cert:

1. Recompute `record_hash` via `projection(R)` (zeroing `record_hash`/`signature`)
   → must equal `R.record_hash`.
2. Verify `R.signature` over `R.record_hash` against the relay cert (→ CICRoot).
3. Chain continuity: `R.prev_type_hash` == `record_hash` of the preceding record
   of `R.type`; likewise `R.prev_topic_hash` for `R.topic`.

Genesis records carry empty predecessors. **Head-truncation** (dropping the newest
records) is undetectable from the chain alone; periodic anchoring of a head into
the ProofTrace chain (`IntegrityEnvelope.ChainAnchor`, already present) is the
defense — an open decision below.

## Reuse map (already implemented)

| Component | Role here |
|---|---|
| `pkg/obs` (`LogRecord`, levels, `logrus_sink`, sinks, `IntegrityEnvelope`) | typed multi-level records + the sink framework |
| `pkg/obs.RecordHash` / `TypeChainHash` | canonical hashing + the per-scope chain primitive (generalizes to topic) |
| `pkg/canonicaljson` | deterministic serialization for the hash |
| `core/nexus/crypto` (Vault) | the signing mechanism (as `core/nexus/recorder` uses it) |
| ProofTrace (`ComputeChainHashV1`, `metaForChecksum`) | the domain-prefixed hash + self-excluding-projection pattern to mirror |

## New work (what feature-009 adds)

| Item | Status |
|---|---|
| `IntegrityEnvelope` gains `PrevTypeHash`, `PrevTopicHash`, `Signature` | concept |
| `hashableProjection` (explicit `record_hash`/`signature` exclusion) | concept |
| Per-(type, topic) chain-head registry + atomic append (the belt) | concept |
| Vault signing wiring in the obs path (reuse `core/nexus/crypto`) | concept |
| Ingest ABIs: WASM host log-ABI (`core/cabinet`) + FFI-seam Go emit | concept (waits on feature-008 P3) |
| `trace`/`debug` as first-class gated levels | concept |

## Open edges (to resolve before implementation)

1. **Topic key shape** — emitters define topics; is a record on exactly one topic,
   or may it carry several (→ multiple `prev_topic_hash`)? Single-topic assumed.
2. **Signing granularity** — per record (strongest, costlier) vs. per batch/segment
   with a signed head. Per-record assumed; batch is an optimization.
3. **Truncation defense** — cadence of anchoring a head into ProofTrace
   (`ChainAnchor`) and/or an external notarization.
4. **Key custody** — inherited from feature-008 `p71` (relay signing key in Vault).
5. **Level gating** — is `debug`/`trace` signed+chained like the rest, or a
   separate unsigned fast path? Default: everything is signed+chained.
