# OCI provider WASM module — real IaC, level 2 (feature-011)

Status: **concept**. Design notes distilled from a design thread
(`thread-oci-go-sdk-to-wasm.txt`, 2026-07-17) that hardened the intent/state
model and settled the shape of a real cloud provider on CIC. No runtime yet;
this records the decisions so the eventual build starts aligned.

This is the "real IaC (level 2)" target: actuate a real cloud (Oracle Cloud /
OCI) through a **CIC provider WASM module**, not a Terraform adapter.

## The single biggest correction

**There is no Terraform in this system.** Earlier notes carried "Terraform" from
the §8 demo doc as an *illustration*; it is not the substrate. The provider is
built directly on the official **OCI Go SDK**, and the primitives are the
actuation unit, not something layered under Terraform. The layering is:

```
Rust   — custody: key, request/proof signing, hash & evidence boundary
Go     — OCI Go SDK: operations, retry, waiter, pagination, work requests
WASM   — the provider adapter logic (OCI ⇄ CIC transform), sandboxed
```

## Why the OCI Go SDK is a good substrate

Generated, statically typed, machine-extractable (`go/ast`, not runtime
reflection — keeps doc comments). From `*_client.go` + `*_request_response.go` +
`*_details.go` you can recover the full operation catalog: HTTP method, path,
path/query/header/body params, required/optional, enums, nested types, response
headers. The `Signer` and `HTTPClient` are **replaceable interfaces** → a custom
CIC signer (key in Vault/Rust) and a proof-capturing dispatcher slot in cleanly.
`Interceptor` runs after request-build, before signing → a natural pre-sign
evidence point.

Limits: no unified public OpenAPI catalog; **breaking changes can ship in minor
versions** → the SDK must be **exact-pinned** with a source hash + extracted
schema hash + a breaking-change gate before version promotion. Cross-field rules,
immutability, replacement, IAM, lifecycle transitions are only partly in the tags
(often just doc comments) — CIC must add the *semantic* contract.

## The provider ABI (stable WIT, versioned payloads)

Do **not** make Create/Update/Delete the ABI, and do **not** generate a typed WIT
per resource (every schema change would rebuild the component). Fixed, small WIT
envelope; the resource data rides inside as a hashed, versioned payload:

```
interface provider {           record schema-payload {
  describe   -> manifest         schema-id, schema-version,
  validate   -> ...              schema-hash, encoding,
  observe    -> observation      data: list<u8>   # canonical JSON / CBOR
  plan       -> execution-plan  }
  execute    -> execution-result
  poll       -> execution-result   # WIT = stable binary ABI
  invoke     -> operation-result   # CIC/schema = semantic contract
  destroy    -> execution-result   # payload = validated, hashed instance
}
```

The reconciler shape (not naive field-set):

```
config_surface → validate → observe(current) → plan(desired, observed)
  → ExecutionPlan → CIC approval/signature → execute(exact signed plan)
  → ExecutionReceipt / JobReference → poll/observe → effective + state → verify
```

Concurrency: the observed **ETag** feeds `If-Match` on update/delete (412 on
concurrent change → no silent overwrite). Async ops carry a **Work Request** id
that `poll` drives to a terminal state.

## Intent / State — two branches, object-level correspondence

This settles the earlier "technical/third branch" question. **Two branches only:
intent and state.** No third branch.

- **Intent** = the logical desired object (higher-level, human/agent-editable).
- **State** = the *same logical object's* observed, provider-bound realization
  (observer-written only). It carries more than intent: provider ids/OCIDs, ETag,
  lifecycle state, generated values, provider defaults, timestamps, evidence.

Intent and state are **not** the same-shaped document and **not** compared
field-by-field / row-by-row. They are **two representations of one logical
object**, correlated and checked **at the object level**:

```
intent.uid == state.uid          # CIC UID = stable logical identity
state.provider_id                # current realization; changes on recreate
correlate(intentObject) -> stateObject      # by CIC UID, not provider id
satisfies(stateObject, intentObject) = true|false   # object-level contract
```

Correspondence is a semantic contract, e.g. `intent.public_ip_allowed ==
!state.observed.prohibit_public_ip_on_vnic`, and logical-reference resolution
(`intent.route_table: production-private` ↔ `state.route_table_id: ocid1…`).

### `effective_config` is not a branch and not a new surface

A cloud read is neither the intent nor pure operational state, so a normalized,
CIC-shaped projection of the *applied* config is needed for comparison — but it is
**derived, read-only**, living inside the state object (or it simply *is*
`state.config` when the state already stores canonical applied values). Not a
third source of truth, not hand-editable. Better name: **`applied_config`** /
`normalized_config` (avoids reading as a third desired state). The genuinely
first-class artifact is the **verification result** binding `intent_commit ↔
state_commit`.

## Boundary — who does what (and self-attestation)

```
WASM module :  OCI API  ⇄  CIC state-object transform  (provider mapping,
               defaults, multi-resource reassembly, read normalization)
CIC host    :  object correlation + object-level compliance check
               + evidence sealing
```

The module does **not** attest its own work and holds **no custody**: secrets
arrive as a **handle** (`cic-secret://…`), never plaintext into WASM; the host
grants only scoped imports (sign request, permitted HTTP call, read allowed
secret via handle, emit evidence, monotonic time). This continues the
feature-008/009 seam (WASM sandbox reaches the world only through host
capabilities; the key stays in Vault/Rust).

This also answers the earlier open question ("does the module actuate directly, or
emit a plan the host applies?"): **plan then execute a signed plan** — the module
emits a plan, the host approves/signs, `execute` runs the *exact signed* plan;
observe/verify are host-side.

## Relay-side interface (issues #87–89)

**Security invariant — no direct guest egress.** The WASM guest has no ambient
network; wazero only exposes what the relay registers (today just `git`). All
outbound traffic goes through a relay host capability (`cic-flow.actuate` →
`http-executor` under `EgressPolicy`), never a guest-opened socket. This is
load-bearing, not convenience: the sign-then-send non-exfiltration property
depends on it — if the guest could egress directly it could `sign`, obtain the
signed credential/request, and leak it itself, bypassing the egress allowlist and
the audit. It is also the underlying reason R1 exists at all: the guest literally
cannot reach a managed service except via a relay host capability.

The host capabilities the boundary above needs do **not** exist yet. Two relay
requirements, raised from `cic-module-oracle-cloud`:

- **R1 / #87 — reachability.** The trust-flow is native-FFI only
  (`cic_ffi_run_flow`, `ffi/src/lib.rs`); the only wazero host module a guest sees
  is `git` (`cmd/relay/git_host_funcs.go`). A provider WASM module cannot drive an
  actuation. Needs a wazero host module (the `git` pattern) exposing the trust-flow
  or a capability derived from it.
- **R2 / #88 — credential model.** The current actuation applies the opened
  credential as a **Bearer** `Authorization` header and **strips** any caller-
  supplied `Authorization` (`http-executor/src/lib.rs`). That fits the
  feature-008/009 seam but **not** a cloud that authenticates per-request signed
  requests. Correction to the Bearer assumption carried in the "Boundary" seam
  above: the OCI path is **sign-then-send**, not Bearer.

**Proposed interface (#89): a `cic-flow` host module, two functions.**

```
cic-flow.sign(req)    -> { signature, key_id }    # airlock signs a caller-built
cic-flow.actuate(req) -> { status, headers, body} # canonical string; carries the
                                                   # caller's own Authorization
```

Sign-then-send splits the work so **the provider-specific canonicalization stays
in the guest** and **the key stays in the airlock**; neither half alone can
exfiltrate (`sign` sees no network, `actuate` sees no key). `actuate` **must not**
strip the caller's `Authorization` on this path (the core of R2). Relay
foundations already exist: `vault-adapter` `VaultTransitSigner` (the signer),
`http-executor` `EgressPolicy` + `Limits` (egress/limits), `HashChainedAuditSink`
(audit) — the delta is plumbing + a `sign` primitive + not stripping auth here,
not new crypto.

### On the signing algorithm — what OCI actually accepts (verified)

Earlier framing ("OCI mandates RSA-SHA256") was imprecise; here is the verified
position. Two levels have to be kept apart:

- **Protocol.** OCI uses the **draft-cavage HTTP Signatures** scheme, which is
  **algorithm-agnostic** — the `algorithm` field is derived from the key, not
  dictated by the wire format.
- **What OCI's implementation accepts.** Per OCI docs, the API signing key must be
  an **RSA key pair, PEM format, minimum 2048-bit**. **ECDSA / Ed25519 are not
  accepted for API signing** (they appear only in KMS/Vault contexts, not request
  signing). The `keyId` (`tenancy/user/fingerprint`) points at that uploaded RSA
  public key.

So for OCI the effective signature **is RSA-SHA256** — not because the protocol
mandates an algorithm, but because RSA is the only accepted API-key type. The
`"rsa-sha256"` in the module spec is therefore correct for OCI, not a guess.

Design consequence: `cic-flow.sign` stays **algorithm-parametric** (it signs with
whatever the `handle`'s key is) — a good cross-provider property, so a *future*
provider that accepts EC keys would sign ECDSA over the same interface. But the
**OCI provider's provisioned key must be RSA**; there is no EC option for OCI
today. RSA is thus quarantined to the OCI API key; CIC-internal custody stays on
P-256 (feature-008). Source: OCI "Required Keys and OCIDs"
(`docs.oracle.com/en-us/iaas/Content/API/Concepts/apisigningkey.htm`).

**Relay wiring gap (found while proving the profile).** OCI's `rsa-sha256` is
RSASSA-**PKCS#1 v1.5**. Vault Transit's RSA default is **PSS**, and
`core/nexus/crypto.VaultCryptoService.Sign` sends no `signature_algorithm`. This is
now handled by **`VaultCryptoService.SignOCI`** (`core/nexus/crypto`): it pins
`signature_algorithm=pkcs1v15` / `hash_algorithm=sha2-256` and returns the raw
base64 signature the OCI `Authorization` header needs (Vault's `vault:v<N>:` prefix
stripped); the key still never leaves Vault. The profile itself (canonical header
set + order, PKCS#1 v1.5, tamper-binding) is proven end-to-end by
`tools/oci-sign-demo` (`make demo-oci-sign`): a fake OCI endpoint verifies the
signature as OCI would, then rejects a tampered body. **Remaining R2/R3 step**:
bind `SignOCI` (with the module's OCI RSA key) into `cic-flow.sign` per-module from
the signed capability manifest — the signer exists; only the per-module wiring is
left.

### Decision (recorded) — a separate, pure-Go `cic-flow` host module

**Where `run_flow` lives.** `run_flow` (`cic_ffi_run_flow`) is in the **`cic-ffi`
crate** (`ffi/src/lib.rs`) — the single `unsafe` C-ABI seam that links the safe
trust-flow crates into Go. It does not implement custody; it *composes* the
reusable crates (`module` = identity + seal, `connections` = vault typestate,
`http-executor` = egress + audit, `vault-adapter` = live Vault chain) under one
baked-in credential model: **Bearer**, with the raw credential opened inside the
Rust airlock and never crossing into Go.

**Decision.** `cic-flow` is a **separate wazero host module**, not an extension of
`run_flow`, and — decided here — it is **pure Go** (the `git` precedent), no cgo on
this path:

- **Mechanism is forced.** A WASM guest can only reach a host module (R1), so
  `cic-flow` is registered on the wazero runtime like `git` (`NewHostModuleBuilder`),
  never via the FFI entry.
- **`sign`** reuses the **Go** Vault Transit signer (`core/nexus/crypto`
  `CryptoService.Sign`) — the OCI API key is a Transit key; Go asks Vault to sign
  and never sees the key.
- **`actuate`** is **pure-Go egress-policed HTTP** carrying the caller's own
  `Authorization` (never strips it); the allowlist is the host's, not the request's.
- **ABI**: the `git` convention `(req_ptr,req_len,out_ptr,out_len) -> i32`, JSON in
  linear memory — **not** the module's proposed packed-i64. Consistency wins; the
  module defers to "match `git`".

**Why pure Go (correcting an earlier note in this doc).** An earlier draft assumed
`cic-flow` had to cross the `cic-ffi` seam to reuse the Rust custody. It does not:
`run_flow` needs the Rust airlock only because its Bearer model **opens a raw
secret** that must never touch Go. Sign-then-send **opens nothing secret** — the
signature is the authenticator and is public, the key stays in Vault via Transit —
so there is no airlock requirement, and the capability is naturally pure Go, like
`git`. `run_flow` stays as-is (the Bearer FFI path); `cic-flow` is the new,
decomposed, guest-facing, pure-Go surface, reusing `core/nexus/crypto` for `sign`.

**Implementation status.** First slice — the `cic-flow` host module (`sign` +
`actuate`, git-pattern ABI, egress-policed) with unit tests — **landed**
(`cmd/relay/cic_flow_host_funcs.go`), registered alongside `git` but **inert by
default** (empty egress denies all `actuate`; nil signer makes `sign` a clean
error) until R3 binds a per-module signer + egress from the signed manifest. R4
(fold host-call audit into the ProofTrace on this path) is the paired follow-up.

Module-side specs (read-only from here): `relay-requirements.md` (R1–R4),
`specs/relay-sign-send-interface.md`, `specs/host-functions.md`,
`specs/capability-manifest.md`.

**R3 plan** — binding the per-module signer + egress from the *signed* capability
manifest, and where `cicSign` verification activates `pki_verify.go`: see
[`r3-plan.md`](r3-plan.md).

## ProofTrace binding

Every execution seals: desired-config hash → plan hash → request/response body
hashes → applied/effective hash → operational state, plus provider evidence
(`opc-request-id`, `etag`, `work-request-id`, module hash, schema hash). This is
the physical "what actually happened" bound to the logical intent — the PoSE-style
intent↔realization anchor, at object granularity.

## First PoC scope

Network primitives, where Create/Update/Read align well and a mechanical
AST field-intersection + converter generator is realistic:

```
VCN · Subnet · RouteTable · SecurityList · NSG (+ rules)
Internet/NAT/Service Gateway · VNIC · Private IP
```

Enough to show: operation registry + request/response schema + CIC contract +
executor adapter + ProofTrace + SDK-version schema diff — all generated from the
pinned Go SDK, with the semantics/policy added by CIC.

## Open border questions (from the thread, still to settle)

1. **1 intent → N provider objects**: a high-level `internet_access.outbound`
   becomes NAT gateway + route rule + NSG rule. Are those separate state objects
   with their own intent, or internal realization detail of one object?
2. **Where the object-correspondence contract lives**: in the YANG/CIC schema, or
   in the mapping/verification contract the OCI WASM module ships?
3. **Delete**: does the state object vanish, go `terminating`, or persist as a
   **tombstone** with the final provider id + evidence?
4. **State: snapshot or history?** Only the latest state in the branch (git
   history + ProofTrace give the time series), or observation history in the
   object too?

(The earlier open question "which key types does OCI API-signing accept?" is now
**resolved** — RSA-only, PEM, ≥2048-bit; see "On the signing algorithm" above.)

## Reference

Full design thread: `thread-oci-go-sdk-to-wasm.txt` (raw). OCI YANG: no official
public control-plane YANG exists, but a CIC-specific YANG is generatable from the
Go SDK (with `cic:create-only` / `immutable` / `requires-replacement` /
`provider-computed` extensions) — deferred; the WIT envelope + object contract is
the near-term path.
