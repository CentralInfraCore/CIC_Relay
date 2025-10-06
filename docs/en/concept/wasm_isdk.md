# WASM iSDK — Host & Guest Concept (v1)

## 0) Goal and Scope

**Goal:** Provide a simple, deterministic, developer‑friendly guest‑side SDK (iSDK) for WASM modules and a minimal host framework that can run **core** and **wasm** steps primarily in **synchronous** mode inside a YAML‑based, directed‑tree (tree‑as‑graph) workflow.

**Scope:** Concept and contract‑level description only; no implementation details.

---

## 1) Context: YAML Directed Tree + Workflow

* The source is a **YAML‑formatted directed tree**. Each node defines: `type` (`core|wasm|group`), `action`, `io` (in/out), optional `on_error`, and `children`.
* A **workflow** walks the tree in a **deterministic order** (e.g., pre‑order).
* A **dispatcher (core function)** decides at each node: local execution (core/wasm call) **or** spawning a sub‑workflow for the child(ren).
* **No implicit Context**: execution is governed strictly by the YAML description and **schema validation**.

---

## 2) Roles and Boundaries

* **iSDK (guest/wasm‑side SDK):** a library linked into the WASM module for module authors. It hides memory/ABI concerns and exposes a unified export.
* **Host framework (minimal executor/dispatcher layer):** turns dispatcher invocations into WASM calls; loads the module, performs the synchronous call, enforces limits, and validates schemas **around** the call.

> YAML only states **what** to run and **which IO bindings** to use. The **how** (ABI, memory management, pooling, timeouts) belongs to the host framework and/or policy.

---

## 3) Principles

* **Deterministic execution:** same YAML → same traversal and result.
* **Uniform call model:** from the dispatcher’s view, both `core` and `wasm` steps are `input → output` transformations.
* **Schema‑driven IO:** pre‑ and post‑validation against declared schemas.
* **Primarily synchronous:** v1 focuses on a stable, simple sync path; async/parallel is a later extension.
* **Minimal surface:** a single stable guest export; JSON payloads in v1.

---

## 4) Host‑Side Concept (minimal framework)

**Responsibilities:**

* **Module resolution & call:** invoke the export indicated by the `type: wasm` node’s `action`.
* **Schema validation (pre/post):** ensure `io.in` matches *accepts* schema; ensure `io.out` matches *produces* schema.
* **Limits:** enforce runtime and input/output size limits (sync path).
* **Error policy application:** default `fail‑fast`; honor `on_error: continue` when specified.
* **Minimal metrics:** call duration, sizes, status.

**Out of scope for v1:** instance pool, async callbacks, WASI access (these appear on the roadmap).

---

## 5) iSDK (guest/wasm) Concept

**Goal:** give module authors a clean, simple surface; iSDK owns the WASM complexity.

**“Big Object” (Facade) — `cicwasm` inside the guest:**

* Inside the WASM module a single **`cicwasm` object** performs *all* WASM‑specific tasks (memory, (de)serialization, internal init) and **executes `Call`** with the appropriate `op`.
* Public methods (all build on `Call`, hiding op‑strings from module authors):

  * `.Init(authCtx, cfg) -> (data_json, error_json)`   ≙ `Call("init", auth, cfg)`
  * `.Process(authCtx, in) -> (data_json, error_json)` ≙ `Call("process", auth, in)`
  * `.Get(authCtx, q) -> (data_json, error_json)`      ≙ `Call("get", auth, q)`
  * `.Notify(authCtx, evt) -> (data_json, error_json)` ≙ `Call("notify", auth, evt)`
* **Dedicated return convention:** **every** method returns **only** `(data_json, error_json)`; there are **no side channels**.
* **Internal init within WASM:** calling `cicwasm.Init(...)` is available **inside** the module and on first call performs module‑level initialization via `Call("init", ...)`.

**Deterministic behavior:**

* WASI and external I/O are **disabled**; processing uses only the provided `data_json`.
* Optional state may exist inside the module, initialized by `cicwasm`.

---

### 5/A) iSDK API Contract (v1)

**Operations (domain‑level):**

* `init` — creation/configuration (optional, module bring‑up).
* `process` — processing / set / create (the main operational call).
* `get` — fetch current state/output (idempotent, no side effects).
* `notify` — notify the host; optional stub in v1, basis for async in v2.

**Uniform signature (exposed via the guest export, provided by iSDK):**

```
Call(op: "init"|"process"|"get"|"notify",
     auth_context_json: JSON,   // optional; may be {} in v1
     data_json: JSON)
 -> (data_json: JSON, error_json: JSON|null)
```

* `auth_context_json` is a host‑prepared auth/scope hint; real authorization occurs on the host.
* `data_json` is the domain payload tied to the *accepts/produces* schemas (v1: JSON/UTF‑8).

**Errors (machine‑readable):**

```json
{
  "code": "INPUT|RUNTIME|INTERNAL|RESOURCE|TIMEOUT",
  "message": "short human‑readable message",
  "details": { "...": "optional machine data" }
}
```

*`code` is fixed; `details` is optional for machine data.*

**Semantics:**

* v1: **synchronous** call (deterministic, no external I/O, WASI off).
* `notify` is an optional stub in v1 (no external I/O); in v2 it becomes the guest→host async return path (`job_id`, return path).
* Roadmap: async (job_id, return path) while keeping the API **unchanged**.
* **Dedicated returns:** every call returns **exactly** `(data_json, error_json)`.

---

## 6) Contract & Schema (short)

* **YAML node required fields** (`type=wasm|core`): `id`, `action`, `io.in`, `io.out`.
* **Schema references:** YAML nodes reference *accepts/produces* by name/URI; the host validates **pre/post**.
* **`type=group`:** has no `action`/`io`; it delegates to its `children` only.
* **Validation:** structural (tree, sibling‑unique `id`) and IO schema validation on the host.
* **Error handling:** per‑node `on_error: fail|continue`.

---

## 7) Execution Semantics (sync, v1)

1. Dispatcher performs a **pre‑order** traversal of the tree.
2. `type=group` → spawn sub‑workflow(s) for children; no local execution.
3. `type=core|wasm` → local execution: `io.in` → (call) → `io.out`.
4. On error, the node’s `on_error` decides: stop (`fail`) or continue (`continue`).

---

## 8) DevSec Minimums (v1)

* **Import allowlist** from the host; WASI **off**.
* **Limits:** time (e.g., ≤ 500 ms) and input/output sizes (e.g., ≤ 2 MB) for the sync path.
* **Deterministic run:** no external side effects.
* **Audit/metrics (minimal):** who called what, duration, sizes, status.

---

## 9) DoD (Definition of Done) — v1

* [ ] Single stable guest export `Call` with dedicated `(data_json, error_json)` returns.
* [ ] Guest‑side `cicwasm` Big Object with `Init/Process/Get/Notify` methods (all map to `Call`).
* [ ] Host minimal framework: load, call, pre/post schema validation, limits, error policy.
* [ ] YAML schema skeleton: node types, required fields, IO bindings, error policy.
* [ ] Deterministic tree traversal documented (e.g., pre‑order).
* [ ] Minimal audit/metrics captured.

---

## 10) Roadmap (v1 → v1.1/v2)

* **v1.1:** optional CBOR payloads; extended error codes; richer metrics; **host‑side Big‑Object client** mirroring `Init/Process/Get/Notify` (internally calling `Call` with an opcode).
* **v2:** host instance pool, async (`job_id`), parallelization of independent children; guest `async_return` support in iSDK; optional fluent API.

---

## 11) Glossary

* **iSDK:** guest/wasm‑side SDK for module authors.
* **Host framework:** minimal runtime layer used by the executor/dispatcher.
* **Dispatcher:** core function that drives execution according to the YAML tree.
* **Node types:** `core` (host function), `wasm` (module), `group` (delegation only).
* **IO binding:** `io.in` → input key; `io.out` → output key.
