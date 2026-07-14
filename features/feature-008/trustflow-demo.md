# Live FFI trust-flow demo (`make demo-trustflow`)

Status: **implemented** (`tools/trustflow-demo`, `scripts/trustflow-demo.sh`).
The visual, one-command demonstration of the feature-008/009 trust-flow — the
one that actually exercises the **Rust** side (unlike the pure-Go
`make demo`/state-chain demo). The Go relay hands one request to the Rust airlock
(`cic-ffi`) and the entire trust-flow runs **inside Rust** against a real
(throwaway) Vault; only the non-secret outcome crosses back.

## What it runs (all inside Rust)

```
Go relay → cic-ffi (Rust) → live Vault:
  ModuleIdentity::generate  — per-request P-256 key + CSR (the key never leaves the module)
  Vault PKI countersign     — sign-verbatim; the CA key stays in Vault
  mTLS cert-auth login       — the countersigned cert in the TLS handshake → a scoped token
  read_sealed (KV)          — the pubkey-sealed secret envelope
  module.open (ECIES)       — the plaintext exists ONLY inside the module
  HTTP actuation            — the opened credential applied as a Bearer token
← only {status, body_base64, scope, audit} returns; the secret never crosses back
```

Sample output (against a throwaway OpenBao):

```
[*] outcome crossed back to Go: status=200 body="pong" scope=[db.read]
    non-secret audit: decision=allow method=GET host=127.0.0.1 record_hash=875e4ab41f23..
[*] the endpoint received the opened credential as: "Bearer hvs.LIVE-DEMO-SECRET"
    → sealed in Vault → read_sealed → opened INSIDE the module → applied as Bearer.
[ok] the secret NEVER crossed back through the FFI seam — only the outcome did.
```

## How it is built

- `scripts/trustflow-demo.sh` spins a throwaway `-dev-tls` OpenBao, provisions the
  PKI (self-contained dev root) + cert-auth + KV mounts, and runs the demo. It
  mirrors `scripts/ffi-live-e2e.sh` (the live FFI test harness), swapping the test
  for the visual command.
- `tools/trustflow-demo` is cgo (`//go:build ffi_cgo`), calling `cicffi.RunFlow`
  with `profile: "live"`; a `!ffi_cgo` stub keeps the default `go build ./...`
  working. The `make demo-trustflow` target builds the live-vault `cic-ffi`
  staticlib (rust-builder), then runs the demo in the go-builder with the `vault`
  binary mounted. Opt-in, not a CI gate; `tools/trustflow-demo` is on the coverage
  skip list.

## Relationship to `make demo`

- `make demo` — the **IaC trust chain** (feature-010): signed, verifiable,
  tamper-evident state + drift + OIS rollback. **Pure Go**, no Rust.
- `make demo-trustflow` — the **capability / secret trust-flow** (feature-008/009):
  per-request module identity, ECIES sealing, mTLS cert-auth, Vault countersign,
  the FFI bridge. **Exercises the Rust modules end-to-end.**

Together they cover the two halves of the CIC thesis.
