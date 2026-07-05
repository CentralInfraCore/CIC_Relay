# CIC Relay — feature-008: Capability-Based Secret Flow (Vault Connection)

## Status

**concept** — the model is fully specified but the runtime bridge is not yet live.
Its preconditions are existing **scaffold** components (see *Bridge map* below).

## Objective

Define the trust model by which a workload obtains and uses a secret/token from
Vault **without the plaintext ever existing as data outside a controlled boundary**.

The secret is not "fetched and handed around". Instead it moves as an
*object / capability* through a controlled execution pipeline, where every
non-holder component sees only references or ciphertext it cannot open. This is
the **Vault Connection** — the first concrete `Connection` type in the
`connections` crate (see [feature-006](../feature-006/spec.md)).

Guiding principle (layer ownership):

> **Core owns meaning. Vault lib owns access. Adapter owns translation. Runtime owns execution.**

## Trust flow

```
1. WASM module receives a task
       │
2.     ├─ generates a keypair + Cert
       │     └─ the PRIVATE KEY never leaves the module
       │     └─ the Cert's CN(s) encode the task content
       │
3.     ├─ requests a countersignature from the Relay  ──►  Relay
       │                                                     │
4.     │                            the Relay decides WHICH CN(s) to countersign
       │                            └─ this is the content-dependent authorization
       │                               = the policy decision point
       │◄──── countersigned Cert ─────────────────────────────┘
       │
5.     ├─ presents the countersigned Cert to the Vault lib
       │
6.     ├─ requests a connection  ──►  Vault (cert-auth: trusts the Relay chain)
       │                                    └─ CN/role → Vault policy → scoped token
       │
7.     └─ vault object init → holds token → usage
```

### The twist: secrets are stored encrypted, never as data

Every secret/token returned by Vault is **immediately encrypted with the public
key of the request** (the keypair whose private half lives only inside the WASM
module). Consequences:

- **At rest**: whatever caches/routes the material (Relay, host, cache) holds
  only **ciphertext it cannot read** — the storage layer is *cryptographically
  blind*, not merely blind by convention.
- **At use**: usage flows **through an endpoint of the WASM module** — the
  request is fed *into* the module (which decrypts internally with its private
  key and uses the secret), rather than the secret flowing *out* to a caller.
  The use continues as a **step in the pipeline**, and is therefore an
  auditable `ProofTrace` step.

The WASM module is thus simultaneously the **key holder** and the **execution
endpoint**. This is the runtime ABI form of the `with_secret(scope)` /
`execute_with_secret(request, capability)` pattern.

## Roles

| Component | Role |
|---|---|
| WASM module | key holder + execution endpoint; generates Cert, holds private key, decrypts and uses secret in-scope |
| Relay | notary / intermediate CA (countersigns) **and** policy authority (decides CN scope) — content-dependent authorization. **Cryptographically blind to secrets** — see below |
| Vault lib | pure access driver; cert-auth against the Relay chain, maps CN/role → policy → scoped token; **not CIC-aware** |
| Adapter | translates between the Vault driver and the `connections` core model |
| Host / cache | stores only pubkey-encrypted ciphertext; never sees plaintext |

## No privileged component — the Relay is not a secret broker

There is **no privileged component** in this model. The Relay is not a trusted
super-broker that can see plaintext; it is **as blind to secrets as anyone else**.

- **The Relay's own first request follows the exact same flow as a normal WASM
  module**: it generates its own keypair + Cert, gets it countersigned, and
  receives a token **encrypted to its own public key that only it can open**.
  There is no privileged/back-door path for the Relay to obtain secrets.
- The Relay's two roles are **orthogonal**: it *authorizes* (countersigns CNs)
  and *orchestrates*, but it **cannot read** the material it authorizes. There
  is **no master key and no trusted plaintext broker** anywhere in the system.
- This is what makes the boundary claim defensible: not "trust the Relay", but
  "no component — including the Relay — is a plaintext holder for material that
  is not its own".

## Why this is not "hard isolation" — and how it is handled instead

Much of this runs in-process, so this is **not** hard physical isolation.
The defensible claim is not "physically impossible to access" but:

> The system does not expose raw secret access to unauthorized components at the
> normal API, type and execution-flow level; and any material at rest is
> encrypted to the only holder that can use it.

The pubkey-envelope at rest strengthens the earlier "acceptable risk" posture:
even in-process, a component holding only the ciphertext cannot use it without
the private key that lives solely in the module.

## Bridge map — preconditions (existing scaffold)

This feature *activates* components that are intentionally scaffolded today:

| Component | Current status | Role in this feature |
|---|---|---|
| `core/cabinet/pki_verify.go` | scaffold | verifies the Relay countersignature chain (waits on CA bootstrap) |
| `core/nexus/isolation/coordinator.go` | implemented (concept-bound) | 3-step onion decrypt (Relay→Module→Host); the pubkey-encrypted token is the module-layer of this onion |
| `core/nexus/crypto/service.go` | implemented | Vault Transit wrapper — candidate mechanism for the pubkey encryption |
| `connections` crate | implemented (minimal) | home of the Vault Connection object model (this feature grows it) |
| M25 PKI Trust Bootstrap | Done | CICRootCA → Intermediate → leaf; the Relay countersignature is the Intermediate role at runtime |

## Open edges (to resolve before implementation)

1. **Relay = intermediate CA**: the Relay signs the WASM Cert with its own key
   (chained to CICRootCA), and Vault trusts that chain root. *(assumed, to confirm)*
2. **CN → Vault policy mapping** lives on the **Vault** side (cert-auth backend
   roles); the Relay only requests scope via CN, Vault decides what it grants.
   *(assumed, to confirm)*
3. **Relay bootstrap (chicken-and-egg)**: since the Relay is just another module
   that must be countersigned, **who countersigns the Relay's very first Cert?**
   Presumably closed by the CICRootCA / M25 trust bootstrap (the Relay leaf sits
   directly under the root/intermediate). *(to specify)*

## Testing posture (target)

Per the layer model, unit tests are not enough for the core/capability layer:

- **Unit**: object invariants, validation, state transitions.
- **Contract**: every backend/adapter honours the same `SecretBackend` /
  capability contract (typed NotFound, envelope-not-plaintext, redacted errors,
  no sensitive logging).
- **Mocked runtime scenario**: full `Actor → Intent → policy → SecretRef →
  capability → use → audit → proof` flow; assert plaintext is never exposed to a
  disallowed component, denied paths do not execute, audit events are emitted.
- **Adapter/integration**: real Vault (and OpenBao) cert-auth, token renewal,
  error mapping.

Gate target for the core/adapter: 90%+ coverage + mutation testing, matching the
existing Rust/Go/Python gates.

## Implementation progress

| Slice | Status | Where |
|---|---|---|
| Core object model (`Cn`, `AuthScope`, `SecretRef`, `CounterSignedCert`, `SealedSecret`, typestate `Connection<S>`) | **implemented** | `connections/src/vault/mod.rs` — clippy `-D warnings` clean, 100% coverage, cargo-deny ok |
| Port/adapter trait surface (`Countersigner`, `TokenProvider`, `SecretBackend` + typed errors, in-memory refs, `establish()`, contract tests) | **implemented** | `connections/src/vault/port.rs` — sync/deterministic/dependency-free; 16 tests, line coverage 100% |
| Live Vault backend on the real driver (`VaultSecretSource::read_sealed` via KV v2, `AdapterError` mapping) | **implemented** | `vault-adapter/` — depends on `sinkog/vault-client-rs` as a **pinned git dep** (rev `17cba8d`); async read path; gates green, coverage TOTAL 97.85% |
| Vault cert-auth → scoped token (`VaultSecretSource::login`, `IssuedToken`) | **implemented** | `vault-adapter/` — `client.auth().cert_at(mount).login(role)`; token kept as redacting `SecretString` |
| Cert generation + private-key custody (WASM side) | **concept** | — |
| Relay countersignature runtime (intermediate-CA + policy) | **concept** | port defined; runtime preconditions: `pki_verify.go`, M25 |
| Pubkey-sealing of returned secrets + pipeline use endpoint | **concept** | `isolation/coordinator.go`, `crypto/service.go` — turns `IssuedToken` into a `SealedSecret` |
| Live-path integration tests (wiremock / real Vault) for `read_sealed` + `login` | **concept** | network async shells are the only sub-90% coverage in `vault-adapter` |

The `vault-adapter` crate is the concrete **adapter** layer: it depends on the
maintained `sinkog/vault-client-rs` fork (pinned by rev in `Cargo.lock`) and owns
*translation* between that async driver and the `connections` core types. It never
decrypts — `read_sealed` reads the pubkey-sealed envelope from KV v2 and wraps it
opaquely as a `SealedSecret`. All async/dependency complexity is isolated here; the
`connections` core stays synchronous and dependency-free. `deny.toml` gained the
fork's git source (`allow-git`), the `CDLA-Permissive-2.0` license (webpki-roots),
and `allow-wildcard-paths` for the internal path/git deps.

The port layer intentionally stays **synchronous, deterministic and
dependency-free** — the core declares *what* it needs; the async Vault I/O is the
future adapter's *how*. The three ports map one-to-one onto the three runtime
bridges, so wiring each concept slice is now "implement this trait against the
real driver" rather than an open design question.

The core slice deliberately carries **no cryptography and talks to no Vault** —
it owns *meaning* only. The typestate (`Requested → Countersigned → Initialized`)
makes the flow order a compile-time guarantee, and `SealedSecret` has no plaintext
field and redacts under `Debug`/`Display`, encoding the security invariant in the
type system rather than in runtime checks.

## Relationship to other features

- **feature-006** (Cabinet / `connections`): this feature adds the first
  concrete `Connection` type — the Vault Connection — on top of the abstract
  relationship model.
- Upstream `vault-client-rs` fork: stays a **pure Vault driver** (typed errors,
  token renewal, redaction, safe secret primitives); the CIC capability/policy
  model lives here in core, reached via an adapter — never poured into the lib.
