# R3 plan — capability-manifest binding & enforcement (feature-011)

Status: **plan**. R3 (#89 bindings / `relay-requirements.md` R3) makes the inert
`cic-flow` host module (R1) **safely active**: it binds a per-module signer +
egress from the module's *signed* capability manifest, and rejects a module whose
imports exceed its declaration. This plans the relay-side work and splits it into
what is buildable now vs what is blocked on the CA-chain bootstrap.

## The trust chain (grounded in the module's `project.yaml` + release tooling)

A module release carries its own provenance; the relay verifies it at load. Fields
are the actual `project.yaml` `metadata` keys (confirmed against
`cic-module-oracle-cloud` `tools/{compiler,finalize_release,verify_release}.py`):

```
author: buildHash = sha256(module/module.wasm)            # compiler.py
        sign  = author signature over checksum, RE-SIGNED to cover buildHash
                ( _resign_with_build_hash )                # createdBy.certificate
                                                           # createdBy.issuer_certificate (CIC Root CA issuer)
   ↓ submit
CIC:    verify checksum == buildHash, review manifest      # finalize_release.py
        cicSign = CIC counter-signature                    # cicSignedCA.certificate (CIC Root CA)
   ↓ deploy
RELAY (load, this plan):
   verify cicSign against the CIC Root CA        ← pki_verify.go (SCAFFOLD)
   verify buildHash == sha256(loaded wasm)
   verify abi.imports ⊆ capability_manifest
   bind per-module cicFlowDeps from capability_manifest
   enforce every host call; deny → ProofTrace
```

The commitments are `abi` (`exports` / `operations` / `imports: cic-flow:[sign,
actuate]`) + `capability_manifest` (`egress`, `secrets`). The module's own
`verify_release.py` does checks 1–4 (schema, buildHash integrity, abi.exports vs
wasm, MANIFEST.sha256) but **explicitly performs no cryptographic signature
verification** (check 5 is status-only) — that crypto step is *the relay's R3 job*.

## What the relay does at load — target sequence

| # | Step | Status |
|---|---|---|
| 1 | Load wasm + its `project.yaml` (`abi`, `capability_manifest`, `metadata`) | concept |
| 2 | **buildHash integrity**: `sha256(wasm) == metadata.buildHash` | buildable now |
| 3 | **cicSign verification**: `metadata.cicSign` valid against the CIC Root CA (`cicSignedCA`) | **scaffold** — `pki_verify.go`, blocked on CA bootstrap |
| 4 | **`abi.imports ⊆ capability_manifest`**: `cic-flow.sign` requires a `secrets` entry with `ops:[sign]`; `cic-flow.actuate` requires `egress`. Reject at load on mismatch | buildable now |
| 5 | **Bind per-module `cicFlowDeps`** from the manifest: `egress` → egress allowlist; `secrets oci/* ops:[sign]` → `SignOCI` bound to the module's OCI key, sign-only | buildable now |
| 6 | **Enforce** every host call; a call outside the manifest → typed `permission` error, denied | buildable now (ProofTrace fold = R4) |

## Fail-closed posture (respecting the scaffold)

Step 3 (cicSign) is blocked on the `CICRootCA → Intermediate → leaf` bootstrap —
the same precondition that keeps `pki_verify.go` scaffold and produces the
existing bootstrap audit `outcome: "skipped", reason: "trust ecosystem not
bootstrapped"`. Until it lands, a module is **not cryptographically trusted**, so
`cic-flow` stays **inert** (the current default `cicFlowDeps{}`) — deny, not
allow. R3 builds the machinery; the cicSign check flips it on. This is fail-closed:
no verified provenance → no capability.

## Split

- **R3a — buildable now (no CA bootstrap):** manifest parse + `abi.imports ⊆
  manifest` + buildHash integrity + per-module `cicFlowDeps` binding + call-time
  enforcement. Unit-testable in isolation; fail-closed until R3b. Reuses R1
  (`cicFlowDeps`, `hostAllowed`) and R2 (`SignOCI`).
- **R3b — blocked on CA bootstrap:** activate `pki_verify.go` to verify `cicSign`
  against the CIC Root CA. This is the same activation the scaffold map already
  tracks; R3 is a first concrete consumer of it.

## Concrete relay artifacts (R3a)

- a `capabilityManifest` type + loader: `egress` (host glob + methods) and
  `secrets` (handle + ops), read from the module's `project.yaml`.
- **egress matching** — extend `hostAllowed` (today exact, case-insensitive) to a
  host suffix glob (`*.oraclecloud.com` matches `iaas.…oraclecloud.com`) plus
  per-entry **method / path / port** checks. Bounded: no bare `*`, no infix
  wildcards (mirrors the module review rule "egress: `*` is rejected").
- `bindCICFlowDeps(manifest, keyResolver) cicFlowDeps` — builds the egress policy
  (host / method / path / port) and the sign-only signer (`SignOCI` over the key
  the `keyResolver` maps the handle to — a **relay-provisioned Vault key**, not
  shipped with the module).
- `abi.imports ⊆ manifest` check → typed load-time rejection.
- buildHash check: `sha256(loaded wasm) == metadata.buildHash`.
- wiring: `main.go`'s inert `cicFlowDeps{}` becomes per-module binding, **gated on
  step 3** (cicSign verified) — so production stays fail-closed until R3b.

## Decisions (settled)

1. **Egress match surface** — host suffix-glob (`*.oraclecloud.com`) **plus
   methods, paths, and ports**. The full `egress` entry is enforced, not just
   host+methods; default port 443 when omitted. Still bounded: no bare `*` host,
   no infix wildcards.
2. **Manifest source** — the **signed `project.yaml` only**. No extra layer: the
   guest's `describe()` is *not* a second source of truth to reconcile against.
   The `capability_manifest` block in the verified `project.yaml` is authoritative.
3. **Key lifecycle** — the OCI cloud-access key is **relay-managed, not shipped
   with the module**. The relay separately obtains the OCI API key and stores it in
   Vault; the manifest's `oci/*` handle is a *logical* reference the `keyResolver`
   maps to that relay-provisioned Vault key. The module never carries or names key
   material — only the handle. Provisioning the OCI key into Vault is a distinct
   operator flow (separate task); R3a's `keyResolver` assumes the key is already in
   Vault.
4. **No dev/insecure bypass** — R3a is **fail-closed, period**. No new
   dev/insecure opt-in is added; the existing dev/insecure escape hatches are to be
   **phased out** over time. Unverified provenance → no capability.

## Reuses / touches

- `cmd/relay/cic_flow_host_funcs.go` — `cicFlowDeps`, `hostAllowed` (→ glob).
- `core/nexus/crypto` — `SignOCI` (the per-module signer).
- `core/cabinet/pki_verify.go` — **scaffold**, the cicSign verifier (R3b).
- the existing bootstrap-audit "skipped / trust ecosystem not bootstrapped" posture.
