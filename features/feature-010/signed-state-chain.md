# Signed state chain — verifiable, tamper-evident recorder (feature-010)

Status: **implemented** (`core/nexus/crypto`, `core/nexus/recorder`). The first
relay-side slice of the §8 demo (Terraform → CIC → Rollback): a state recorder
whose commits form a **Vault-signed, tamper-evident chain** that can be
**verified** end-to-end — the key never leaves Vault (CIC-native model, not
git-native `git verify-commit`).

## What it adds

- **`crypto.CryptoService.Verify(ctx, data, signature) (bool, error)`** — the
  counterpart of `Sign`, over Vault Transit's `verify` endpoint. The signing key
  never leaves Vault; verification asks Vault whether a signature is valid.
- **Parent binding** — `RecordState` now writes the **parent commit hash** into
  `meta.json`, so the signed manifest (expected + actual + meta) covers the
  chain position. Changing any earlier commit breaks the git parent linkage that
  every later signature attests to.
- **`GitStateRecorder.VerifyChain(ctx)`** — walks the recorded state commits
  oldest-to-newest and, for each: recomputes the manifest exactly as `RecordState`
  did, asks Vault to **verify the signature** (content authentic and unaltered),
  and checks the **parent linkage** (signed parent == actual git parent). Any
  content tampering or chain break is detected. The initial `.gitignore` commit
  (unsigned) is skipped.

## The verification model (CIC-native)

```
RecordState:  manifest = {expected.json, actual.json, meta.json(+parent)}
              signature.meta = VaultTransit.Sign(sha256(manifest))   # key stays in Vault

VerifyChain:  for each state commit (oldest→newest):
                recompute sha256(manifest)  →  VaultTransit.Verify(hash, signature)
                assert meta.parent == git-parent(commit)
              → "signed, chain intact" | error at the first bad commit
```

The signing key stays in Vault (CIC principle: *no key on disk*). This is not the
git-native `git verify-commit` (which needs a GPG/SSH signer); the trust is the
Vault signature over the manifest plus the parent-linked chain.

## Tested

- `crypto`: `Verify` valid / invalid / vault-error / missing-field (httptest Vault
  mock). Coverage 93.9%.
- `recorder`: a 3-commit chain verifies; content tampering, a broken parent link,
  an invalid signature, a Vault verify error, a corrupt `signature.meta`, an
  empty (no signed commits) repo, and a nil crypto service are all detected.
  Coverage 85.8%. The test mock's signature deterministically binds the data, so
  real tamper-detection is exercised.

## Live demo (`make demo`)

`make demo` runs the §8.1 + §8.5 core against a **real, throwaway OpenBao**
(`scripts/statechain-demo.sh` spins a `-dev-tls` server with a Transit
`ecdsa-p256` key; `tools/statechain-demo` drives it):

1. records three infrastructure-state transitions — each a Vault-signed,
   parent-linked commit (prints commit hash, parent, and the `vault:v1:…`
   signature);
2. `VerifyChain` → "signed state chain verified — signatures valid, chain
   intact" (the key never left Vault);
3. **tampers** the latest committed state and re-verifies → the altered commit is
   caught ("signature verification failed") — the chain is tamper-evident.

Opt-in, not a CI gate (needs the `vault`/openbao binary).

## Next (demo track)

- Drift detection + classification (`SOFT` / `RECONCILIABLE` / `HARD_DRIFT`),
  then the `intent/`-branch rollback + OIS policy gate (§8.2–§8.4).
