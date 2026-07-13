# Vault Transit audit-signer (feature-009)

Status: **implemented** (`vault-adapter`, `transit` module). The production
key-custody counterpart of the in-process audit-chain signer (p91): the ECDSA
signing key that authenticates the audit chain lives in Vault's Transit engine
and **never leaves it**. This is the same custody model the countersign CA key
already uses (`pki/sign-verbatim`), applied to the audit log.

## What it is

- `TransitBackend` — the narrow seam over the `vault-client-rs` driver:
  `transit_sign` and `transit_verify`, each scoped by an explicit `mount` and key
  `name`. The real `VaultClient` implements it via `self.transit(mount)`; tests
  implement it with an in-memory fake and against `wiremock`.
- `VaultTransitSigner<B = VaultClient>` — holds the backend + Transit `mount` +
  key. `sign(&[u8]) -> VaultSignature` asks Vault to sign; `verify(&[u8],
  &VaultSignature) -> bool` asks Vault to verify. It never holds private-key
  material. `connect(addr, token, mount, key)` is the plain-transport
  constructor; a custom-CA / mTLS client is built by the caller and wrapped with
  `new`.
- `VaultSignature` — a newtype over the engine's `vault:v1:<base64>` wire form.
  Public, non-secret material (safe to store and log). `from_wire` reconstructs a
  stored signature so it can be re-verified later.
- `SignError` — `Denied` (Vault policy refused) or `Backend(VaultError)`. Carries
  no secret material.

## Design decision — head anchor, not per-entry sync signing

`http-executor`'s `HashChainedAuditSink` signs each entry **synchronously** with a
`RecordSigner` (an in-process ECDSA key). A Vault Transit call is **async**
network I/O; driving it from that sync `record()` path would mean blocking on a
runtime from inside a runtime — a footgun that panics under `tokio`.

Because the chain is hash-linked (`record_hash_n` transitively binds every
earlier entry), signing the **head** `record_hash` with a Vault-held key anchors
the whole prefix in a single call — the standard tamper-evident-log notarization
shape. So `VaultTransitSigner` signs raw `record_hash` bytes (the exact
`ChainedEntry.record_hash.as_bytes()` domain the in-process signer signs, so a
Vault anchor and an in-process signature bind the same value). The in-process
`LocalEcdsaSigner` stays for the pure/embedded path; this is its production
key-custody counterpart, not a replacement.

## Two coexisting signature models

| Model | Where | Key custody | Granularity | Sync/async |
|---|---|---|---|---|
| `LocalEcdsaSigner` (p91) | `http-executor` | in-process key | per-entry | sync |
| `VaultTransitSigner` (p92) | `vault-adapter` | key in Vault Transit | head anchor | async |

## Testing & mutation

The real `impl TransitBackend for VaultClient` is **not** feature-gated (unlike
`connect_mtls`), so its `Result<String>` / `Result<bool>` returns would leave
mutation survivors (`Ok(String::new())`, `Ok(true)`, `Ok(false)`). Rather than
exclude them in `.cargo/mutants.toml` (which the policy forbids for
non-cfg'd-out code), `tests/transit_mock.rs` drives the **real** driver path
against a `wiremock` server (`vault-client-rs-test::test_client`) and asserts the
concrete sign/verify results — killing those mutants with genuine coverage.

`tests/transit_sign.rs` (behind the `live-vault` feature) proves the full path
against a real (throwaway) OpenBao Transit mount: **sign → verify →
tamper-detect → stored-signature round-trip**. Run via `make test-vault-live`,
which enables a `cic-transit` mount with an `ecdsa-p256` `cic-audit` key. Opt-in,
not a CI gate.

Gates: clippy `-D` (`--all-features`), coverage (`transit.rs` 100% lines), deny
(zero new deps), `cargo-mutants` 0 missed.

## Still open

- A **scoped token** (not root) carrying only the Transit sign/verify capability
  for the signer, mirroring the cert-auth scoping of the read path.
- Per-type/topic **dual** hash chains (feature-009 spec, p80).
- Wiring the anchor into the Go-side `ProofTrace` / persistence.
