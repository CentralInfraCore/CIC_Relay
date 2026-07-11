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

#### Module identity lifecycle — per-request, single-use

The module keypair is **the key of the request**, not a persistent identity. Per
the OIS-lib model, *the client mints a fresh keypair per request, and the response
key is always a single-use, session-level ephemeral key.* One keypair deliberately
serves **both** roles — the signing identity (its public key carries the
authorization CNs in the CSR) **and** the ECDH sealing recipient — because the
public key **is** the capability, binding authorization and encryption in one
short-lived object.

**Why single-use matters (and why the code enforces it):** reusing one keypair
across requests would make the ephemeral key effectively long-lived, at which
point the shared signing/ECDH key reintroduces cross-algorithm-key-reuse and
forward-secrecy concerns. The single-use lifecycle is what keeps the dual role
sound. `ModuleIdentity::open()` therefore **consumes `self`** — opening ends the
identity's life and zeroizes the key — so single-use is a type-system invariant,
not a convention. The mechanism (rather than two keys) is the deliberate design;
splitting the keypair would break the pubkey-as-capability model.

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
| M25 PKI Trust Bootstrap | Done (public anchor only) | CICRootCA → Intermediate → leaf; the Relay countersignature is the Intermediate role at runtime. **Caveat**: only the public verify anchor is wired — the intermediate *signing key* custody is not (see *Countersign runtime blocker* below) |
| Rust ↔ Go FFI bridge | principle fixed, code scaffold | the trust-flow crates link into the Go relay as a library; the boundary rules are fixed in [`ffi-boundary.md`](./ffi-boundary.md) (unsafe airlock, safe core) |

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

## Countersign runtime blocker — confirmed (the critical path for the Relay slice)

The **Relay countersignature runtime** slice (still `concept`) cannot start until
the Relay holds an intermediate *signing key* at runtime. This blocker is
**confirmed against the code**, not assumed:

- **Verify side is live, signing side is absent.** `core/cabinet/pki_verify.go`
  (`VerifyCertificateChain` + CRL + SPKI pinning) is fully implemented and already
  wired into module loading (`cmd/relay/bootstrap.go`, `ExtKeyUsageCodeSigning`).
  There is **no countersign / `x509.CreateCertificate`-as-intermediate** code path
  anywhere, and **no intermediate private-key custody** in the runtime.
- **The runtime embeds only the public anchor.** `//go:embed embedded` in
  `cmd/relay/embedded.go` bundles `cmd/relay/embedded/` — which contains **only
  public `.pem` certs** (`rootCA2026.pem`, `CICDeveloperCA2026.pem`,
  `EmbeddedTestRootCA.pem`); no private key.
- **The private keys in the repo are not the runtime's.** The repo-root
  `embedded/pki/` holds real `intermediate_ca.key` / `root_ca.key`, but it is an
  offline openssl generation scratch dir (`.csr` / `.srl` artifacts), it is **not**
  the embedded FS the runtime bundles, and no Go code loads it as a signing key.
  Its own README states: *"DEVELOPMENT ONLY … Production PKI material is injected
  at runtime from Vault or a separate secure store."*

**Consequence**: today the Relay can only *verify* (public trust store), never
*countersign* (needs the private intermediate key). Wiring intermediate-key custody
is therefore the **first task** of the Relay countersignature slice — everything
downstream (policy decision point, signing op, WASM ABI endpoint, verify round-trip)
depends on it.

### Decision — intermediate signing key custody lives in Vault (PKI engine)

Resolving *Open edge #1*'s custody question: the intermediate signing key is
**injected at runtime from Vault, not embedded as an asset**, and the countersign
uses the **Vault PKI secrets engine** (`pki/sign-verbatim`), not Transit.

The two feature-008 crypto operations are distinct: **countersigning** (the Relay
as intermediate CA signing the module Cert) is a CA operation the PKI engine is
built for — it takes the module's CSR and returns a cert chained to the CA root,
so the intermediate key never leaves Vault; **pubkey-sealing** of the returned
token (step 7) is the separate Transit/pubkey step. (An earlier note pinned the
countersign to Transit *sign*; that only signs raw bytes and would force manual
X.509 assembly, so it is corrected to the PKI engine here.) Embedded `.key` files
remain **dev/test only**, per the `embedded/pki/README.md` warning.

### The Relay's intelligence lives in the policy, not the signer

The countersign runtime splits into two orthogonal parts, so the Relay's
*content-dependent authorization* (trust-flow step 4 — deciding **which** CNs to
countersign) is a first-class, injectable decision point rather than baked-in
logic:

- **`CountersignPolicy`** (the decision — where the Relay is "smart"): given a
  `CertRequest`, returns the authorized `AuthScope` or a `Denied` reason. The
  trivial `AllowlistPolicy` is just the simplest impl; richer content-dependent
  rules (and later quorum) plug into the same seam.
- **`PkiBackend`** (the mechanism — a dumb signer): `pki/sign-verbatim` over the
  driver. It knows nothing about policy.

`VaultPkiCountersigner` wires them: authorize, then — only for what policy
permitted — have Vault countersign, yielding a `CounterSignedCert` carrying the
**authorized** scope. Implemented in `vault-adapter/src/countersign.rs`, tested
against the driver-level `MockPki`; this is the live counterpart of the
`connections` `Countersigner` port.

#### Scope contract — authorization must equal signed content

`sign-verbatim` signs the request's CSR **unchanged**, so the issued cert's
identity (its SANs) is exactly the request's scope, and **Vault cert-auth
authorizes on the cert, not on the `CounterSignedCert.scope` metadata**. A policy
that returns a strict *subset* of the requested scope would therefore emit a cert
whose SANs **exceed** the authorized scope — the metadata would narrow, the
cryptographic reality would not. This is a security invariant, not a nuance.

Contract, enforced in code: `authorized == request.scope` or the countersign
**fails closed** (`CountersignError::ScopeNarrowingUnsupported`). `AllowlistPolicy`
is all-or-nothing, so it satisfies this today. Any future *narrowing* policy must
first make the mechanism reflect the decision in the **signed bytes** — rebuild
the CSR/cert to the authorized subset, or verify the returned cert's SANs against
`authorized` — before the guard can be relaxed.

### Driver-level mock foundation (in place — the custody scaffolding)

So the custody + countersign logic can be built against Vault's `transit`/`pki`
operations **without a live Vault and without pre-committing the provisioning
path**, a driver-level mock of the Vault client now exists:

- Upstream in the `sinkog/vault-client-rs-test` crate (`feat/driver-trait-mocks`,
  rev `73dab2c`): hand-written trait mocks (`MockKv2`, `MockCertAuth`) implementing
  the driver's operation traits — reusable, owned, programmable doubles (the
  operation traits are not object-safe, so `mockall` is out and the seam is generic).
- In `vault-adapter/`: a narrow `VaultBackend` port (`read_field` + `cert_login`)
  over the driver; `VaultSecretSource<B>` is generic over it (default `VaultClient`).
  `tests/driver_mock.rs` exercises the adapter's translation logic against the mocks
  with no `WireMock`/HTTP; the p70 `WireMock` tests remain as the HTTP layer.

The `MockTransit`/`MockPki` doubles follow the same template and land with the
countersign slice that first exercises `TransitOperations::sign` / `PkiOperations`.
Tracked as **p72** (Done) in `ai/ROADMAP.md`; unblocks the p71 custody work.

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
| Cert generation + private-key custody (WASM side) | **implemented** | `module/` — `ModuleIdentity::generate(subject, scope)` → P-256 keypair + PKCS#10 CSR (subject CN + scope CNs as SANs) → `CertRequest`; private key held (redacted `Debug`), never emitted. Pure-Rust (`p256`+`x509-cert`), permissive-only. E2e round-trip test: module CSR → `VaultPkiCountersigner` |
| Relay countersignature runtime (intermediate-CA + policy) | **concept** | port defined; **blocked** on intermediate signing-key custody (Vault) — see *Countersign runtime blocker*. Verify side (`pki_verify.go`) already live; signing side absent |
| Live-path integration tests (wiremock) for `read_sealed` + `login` | **implemented** | `vault-adapter/tests/live_mock.rs` — mocked Vault HTTP; `vault-adapter` now 100% line coverage |
| Pubkey-sealing of returned secrets + module open endpoint | **implemented** | `module/src/sealing.rs` — `seal(pubkey_der, ref, plaintext)` → `SealedSecret` and `ModuleIdentity::open()` → plaintext. **ECIES over P-256** (ephemeral ECDH + HKDF-SHA256 + AES-256-GCM), *not* Vault Transit (which can't encrypt to an external module key). Full-flow e2e: CSR → countersign → seal → `Connection::initialize` → module opens |

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
