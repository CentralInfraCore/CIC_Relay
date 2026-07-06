# CIC Relay — feature-008: Rust ↔ Go FFI boundary principles

## Status

**principle** — this document fixes the rules the FFI bridge must obey *before*
any boundary code is written. The bridge itself (the `ffi` shim crate + the Go
cgo consumer) is **scaffold** until those crates land; this document is the
contract they are reviewed against.

Companion of [`spec.md`](./spec.md): the trust-flow logic
(`connections` / `vault-adapter` / `module`) is pure-safe Rust; this document
governs **how that logic is called from the Go runtime** (`cmd/relay` +
`core/cabinet`).

## Objective

The Rust trust-flow crates become a **library linked into the Go relay** (cgo /
static linking). The integration is deliberate and its coupling cost (weakening
of process/language separation) is accepted and **managed outside this document**.
What this document guarantees is narrower and non-negotiable:

> **`unsafe` lives only in the boundary seam. Every byte that crosses is
> validated and *owned* before any business logic touches it. The trust-flow
> core stays machine-proven memory-safe (`unsafe_code = "forbid"`).**

The danger surface is not spread across the codebase — it is concentrated into
one small, auditable crate whose entire job is marshalling.

## The core principle — the unsafe airlock

Every `extern "C"` export is a three-phase airlock. `unsafe` appears only in the
first and last phase, and only for pointer/length handling — never for logic.

```
 ┌─ INGRESS (unsafe) ──────────────────────────────────────────────┐
 │  raw *const u8 + len from Go                                     │
 │    → null-check, len-check                                       │
 │    → COPY into an owned Vec<u8>/String (slice::from_raw_parts)   │
 │    → validate content (UTF-8, size caps, schema)                 │
 └──────────────────────────────┬──────────────────────────────────┘
                                 ▼   owned, validated value
 ┌─ CORE (safe) ───────────────────────────────────────────────────┐
 │  connections / vault-adapter / module  (unsafe_code = "forbid")  │
 │  no raw pointers, no borrows into Go memory — only owned values  │
 └──────────────────────────────┬──────────────────────────────────┘
                                 ▼   owned result
 ┌─ EGRESS (unsafe) ───────────────────────────────────────────────┐
 │  owned result → boxed slice → raw ptr + len handed back to Go    │
 │  (paired Rust-side free export releases it)                      │
 └─────────────────────────────────────────────────────────────────┘
```

The `unsafe` blocks are the **only** unsafe in the whole workspace, they are a
handful of lines each, and they contain **no** decision logic.

## The boundary rules (enforceable, reviewable)

1. **`unsafe` only at the seam.** No `unsafe` outside the `ffi` shim crate. The
   business crates keep `unsafe_code = "forbid"`. A reviewer greps for `unsafe`
   and expects hits **only** in `ffi/`.

2. **No borrow across the boundary.** Never hold a reference (`&T`) into
   Go-owned memory past the ingress `unsafe` block. The Go garbage collector may
   move or free it. Always **copy into owned Rust** (`slice::from_raw_parts(..).to_vec()`)
   before returning from the unsafe block. This is the technical form of
   *"validate the contents, then move them into safe code."*

3. **Validate at the seam.** Length, null, UTF-8, and size caps are checked at
   ingress. Malformed input from Go is rejected **here**, with an error code —
   never deep inside business logic, and never by `panic`. The core receives
   only well-formed, owned values.

4. **Single ownership / paired free.** Memory allocated by Rust and returned to
   Go is released by a **Rust-side `*_free` export** — Go never frees Rust
   memory, Rust never frees Go memory. Use `Box<[u8]>` (`into_boxed_slice` →
   `Box::into_raw`) so capacity equals length and the free is unambiguous.

5. **No panic across `extern "C"`.** Unwinding a panic across the C ABI is UB.
   Two layers of defence:
   - **Discipline (the real guarantee):** exports must not `panic` on
     attacker-controlled input — no `.unwrap()`/`.expect()`/indexing on external
     data; validation returns an error code instead.
   - **Backstop:** wrap the safe-core call in `std::panic::catch_unwind`.
     **Note:** the workspace `release` profile sets `panic = "abort"`
     (`Cargo.toml`), so in release a panic aborts the process and `catch_unwind`
     is a no-op — the discipline above is what actually protects the boundary;
     `catch_unwind` only recovers in dev/test builds (which unwind).

6. **Redaction survives only up to the seam.** The core keeps secrets in
   redacted `SecretString`. Once bytes cross into Go they are plain Go memory
   (GC-managed, not zeroized) — that is the accepted coupling cost, handled
   outside this document. The rule here: the `ffi` crate must not *log*, *copy
   into long-lived buffers*, or *widen* the exposure beyond the single return
   value the caller asked for.

7. **Determinism is preserved.** The boundary introduces no nondeterminism
   (no time, no RNG in marshalling, stable byte order). Anything entering the
   ProofTrace hash chain must be byte-identical whether produced via Rust tests
   or via the Go call path.

## Crate topology

```
ffi/  ── the airlock (the ONLY crate allowed unsafe)
   ├─ does NOT inherit `[lints] workspace = true`
   ├─ own lint posture: unsafe_code = "allow",
   │                    clippy::undocumented_unsafe_blocks = "deny"  ← every unsafe needs // SAFETY:
   ├─ crate-type = ["staticlib"]   ← C-linkable for cgo
   ├─ only extern "C" marshalling fns (the 3-phase airlock) + *_free
   └─ depends on ↓
connections / vault-adapter / module   ← unsafe_code = "forbid" — pure safe Rust, unchanged
```

The Go consumer (cgo package under `core/nexus/` or `pkg/`) declares the C ABI,
calls the exports, and owns the Go-side wrappers. It links the `ffi` staticlib.

## Repo-specific consequences (must be handled during implementation)

| Constraint (source) | Consequence |
|---|---|
| `unsafe_code = "forbid"` at `[workspace.lints.rust]` | `ffi/` cannot use `[lints] workspace = true`; it declares its own lint block (mirroring the clippy denies, allowing unsafe). |
| `panic = "abort"` in `[profile.release]` | Release aborts on panic; `catch_unwind` recovers only in dev/test. Rule 5 discipline is the real guarantee. |
| `CGO_ENABLED=0` in the release build (`Makefile:584`) | cgo requires `CGO_ENABLED=1` + a C toolchain. The pure-static Go build changes; build-provenance (reproducibility, cross-compile) impact must be tracked. |
| `rust-builder` / `go-builder` are separate containers | The link step needs the Rust staticlib artifact available to the Go build; the Makefile must produce the staticlib first, then build Go with `CGO_LDFLAGS` pointing at it. |
| `deny.toml` supply-chain gate | The `ffi` crate adds no external deps for the pattern itself (std only); any that appear go through cargo-deny like every crate. |

## Review checklist (for every boundary PR)

- [ ] `grep -rn unsafe` shows hits **only** under `ffi/`.
- [ ] Every `unsafe` block has a `// SAFETY:` comment (enforced by clippy).
- [ ] No `&`-borrow of Go memory escapes the ingress block; inputs are `.to_vec()`-copied.
- [ ] No `.unwrap()`/`.expect()`/raw indexing on Go-supplied data.
- [ ] Every allocating export has a paired `*_free` export; ownership documented.
- [ ] Safe-core call wrapped in `catch_unwind` (backstop).
- [ ] No secret logged or copied beyond the single return value.
- [ ] Business crates still `unsafe_code = "forbid"` — no change to their lint posture.

## Related

- [`spec.md`](./spec.md) — the trust-flow model this bridge serves.
- `Cargo.toml` (workspace) — `unsafe_code = "forbid"`, `panic = "abort"`.
- Bridge map in `CLAUDE.md` — this document turns the *"the model exists but the
  implementation bridge breaks here"* edge into a defined, auditable seam.
