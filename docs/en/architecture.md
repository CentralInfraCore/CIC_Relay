# System Architecture Overview

This document provides a high-level overview of the project's architecture and operational philosophy. The goal is for a new developer to understand the system's fundamental concepts within 5-10 minutes.

> This repo was originally cloned from the shared `base-repo` scaffolding
> template, and this document used to describe *that* template's own
> "produce templates for downstream repos" model instead of what CIC-Relay
> itself does. `project.yaml`/`tools/compiler.py` still carry `base-repo`'s
> original template metadata and aren't wired into this repo's CI — they're
> inherited-but-inactive scaffolding, not part of the runtime described
> below.

## What CIC-Relay Is

CIC-Relay is a **ProofTrace-based, auditable execution platform**. Every
executed workflow forms a deterministic hash chain whose integrity can be
cryptographically verified back to the CICRootCA. Trust here is not an
assumption — it's a verifiable state.

## Three Layers

```
cmd/relay        HTTP API (:8080), bootstrap, PKI asset loading
core/cabinet      Schema/Module/Workflow registry + WASM executor (wazero)
core/nexus        IaC loader, Git recorder, Vault crypto, sync, isolation, operator
```

`cabinet` and `nexus` are intentionally independent — they evolve on
different timelines. The runtime that's actually live today is
`cmd/relay` + `core/cabinet`, with `nexus/recorder` and `nexus/crypto` wired
in as optional components.

## Execution Flow

1. A client `POST`s a workflow request to `cmd/relay`'s `/set` endpoint.
2. `core/cabinet`'s `Setx()` validates the request against the registered
   schema (`validateInputSchema`), then walks the `ExecutionGraph` — a
   linear sequence of named steps.
3. Each step that names a WASM module runs through the `cicwasm` host: the
   wazero runtime compiles/instantiates the module (LRU-cached,
   singleflight-deduplicated, timeout-bounded) and executes it.
4. Every step's input/output is hashed into a per-step `ProofTrace`, chained
   into a running `chain_hash` — the deterministic, auditable record of
   what actually ran.
5. If a recorder is configured (`core/nexus/recorder`), the trace is
   Vault-signed and committed to a Git audit log. Otherwise it's recorded
   with `outcome: "skipped", reason: "trust ecosystem not bootstrapped"` —
   the bootstrap is intentionally incomplete until the PKI chain exists
   (see Status below), and the system says so rather than pretending.

## Determinism

The whole trust model depends on identical inputs producing byte-identical
hashes, so canonicalization is enforced end-to-end:

- `pkg/canonicaljson` — sorted keys, platform-independent serialization.
- WASM binaries are canonically stripped of custom sections (`wabin`)
  before hashing.
- `CanonicalizeSetPayload` dedupes and sorts request payloads before they
  ever reach a hash.

Any change that touches something that gets hashed must pass the golden
tests: `make verify`.

## Status Legend

Every component in this system is one of three things, and the difference
matters — a "scaffold" is not a bug, it's a deliberate, gated placeholder:

| Status | Meaning |
|---|---|
| **implemented** | lives in code, covered by tests, CI green |
| **scaffold** | present in code but deliberately unwired — a precondition is missing |
| **concept** | documented, but has no runtime counterpart yet |

### What's implemented today

- `core/cabinet/service.go` — `Setx()` workflow executor, schema validation, ProofTrace, recorder call
- `core/cabinet/graph.go` — `ExecutionGraph` (linear, named output keys, backward-compatible)
- `core/cabinet/cicwasm.go` — WASM ABI (`Call`/`allocate`/`deallocate`), LRU cache, singleflight, timeout
- `core/cabinet/validate.go` — deterministic checksums (kind prefix + canonical JSON + SHA-256)
- `core/cabinet/stores.go` — CAS-backed `ModuleStore`, thread-safe `SchemaStore`/`WorkflowStore`
- `core/nexus/recorder/recorder.go` — Git-backed audit trail: expected+actual JSON + Vault signature → commit
- `core/nexus/crypto/service.go` — Vault Transit engine wrapper
- `core/nexus/isolation/coordinator.go` — 3-step onion decrypt (Relay → Module → Host)
- `pkg/canonicaljson` — deterministic JSON serializer
- `pkg/obs` — structured audit-log emission (`LogRecord`, hash-chained integrity envelope) plus basic
  runtime metrics: an in-process aggregator (`obs.PrometheusSink`) exposed at `GET /metrics`

### What's scaffolded (deliberate, waiting on a precondition)

| Component | Waiting on |
|---|---|
| `pki_verify.go` wiring | CICRootCA → Intermediate CA → leaf cert bootstrap |
| `UpstreamSource` | relay federation (v2) |
| `LocalWorker` in-process isolation | 3-process isolation + Vault |

### What's concept-only (documented, no runtime yet)

- The quorum decision layer
- The CICmeta protocol bridge into `core/cabinet`

If a question touches one of these gaps, the honest answer is never "it
doesn't exist" — it's "the model exists, but the implementation bridge
breaks here."

## Where to Go Next

- `CLAUDE.md` — the canonical AI/developer context, kept current every sprint
- `context/CONTRACT.md`, `context/SYMBOLS.md`, `context/LIMITS.md` — canonical contracts
- `ai/ROADMAP.md` — milestones and current sprint status
- `ai/PROMPTMAP.yaml` — the concrete next steps
