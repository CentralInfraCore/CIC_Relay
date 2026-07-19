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
   verify module provenance/entitlement (CICSourceCA + CICLicenseCA)  ← pki_verify.go (wired)
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

## Trust anchors (corrected)

Earlier drafts said R3b is "blocked on a CA-chain bootstrap" and that
`pki_verify.go` is scaffold. **Both are wrong** (verified against the code and the
`EEC/` CA hierarchy):

- The CA hierarchy **exists** (`EEC/`: Root CA, `CICPlatformCA`, `CICDeveloperCA`,
  `CICSourceCA`, `CICLicenseCA`) and `core/cabinet/pki_verify.go` is **wired and
  active** — `cmd/relay/bootstrap.go:79`,`:508` call `VerifyCertificateChain`
  against the pool `bootstrapPKI` loads (`main.go:684`). In `CIC_TRUST_MODE=dev`
  (default) the embedded **CICDeveloperCA** is the anchor and a module with a bad
  chain is rejected at load.
- **But the embedded dev CA and the module-sidecar `.pem` are *not* the planned
  module-trust for `cic-flow`.** By design the anchors are two separate axes,
  each delivered by its own runtime path (not `go:embed`, not the sidecar `.pem`):
  - **CICSourceCA** — build/source provenance; arrives **alongside the relay
    binary**, loaded at startup.
  - **CICLicenseCA** — entitlement; **extracted from the license** at startup.
- So the module release is verified on **two** axes: `cicSign`/provenance against
  **CICSourceCA**, and entitlement against **CICLicenseCA**. The certs exist in
  `EEC/` but are **not embedded** — deliberately, because they come via those
  runtime channels.

**R3b's real precondition is therefore the CICSourceCA (binary-adjacent) +
CICLicenseCA (from-license) runtime provisioning path — a distinct planned piece —
not a CA-chain bootstrap.** `pki_verify.go` itself is ready to consume those pools.

## What the relay does at load — target sequence

| # | Step | Status |
|---|---|---|
| 1 | Load wasm + its `project.yaml` (`abi`, `capability_manifest`, `metadata`) | concept |
| 2 | **buildHash integrity**: `sha256(wasm) == metadata.buildHash` | buildable now |
| 3 | **provenance/entitlement verification**: module verified against **CICSourceCA** (build/source) + **CICLicenseCA** (license) | `pki_verify.go` **wired** (dev uses embedded CICDeveloperCA); waits on the **SourceCA/LicenseCA runtime provisioning path** |
| 4 | **`abi.imports ⊆ capability_manifest`**: `cic-flow.sign` requires a `secrets` entry with `ops:[sign]`; `cic-flow.actuate` requires `egress`. Reject at load on mismatch | buildable now |
| 5 | **Bind per-module `cicFlowDeps`** from the manifest: `egress` → egress allowlist; `secrets oci/* ops:[sign]` → `SignOCI` bound to the module's OCI key, sign-only | buildable now |
| 6 | **Enforce** every host call; a call outside the manifest → typed `permission` error, denied | buildable now (ProofTrace fold = R4) |

## Fail-closed posture

Step 3 gates the capability: a module is trusted only once its provenance
(CICSourceCA) and entitlement (CICLicenseCA) verify. Until those anchors are
provisioned via their runtime path, a module is **not fully trusted**, so
`cic-flow` stays **inert** (the current default `cicFlowDeps{}`) — deny, not
allow, matching the existing bootstrap audit `outcome: "skipped", reason: "trust
ecosystem not bootstrapped"`. R3 builds the machinery; step 3 flips it on. This is
fail-closed: no verified provenance/entitlement → no capability.

## Split

- **R3a — buildable now:** manifest parse + `abi.imports ⊆ manifest` + buildHash
  integrity + per-module `cicFlowDeps` binding + call-time enforcement.
  Unit-testable in isolation; fail-closed until R3b. Reuses R1 (`cicFlowDeps`,
  egress policy) and R2 (`SignOCI`).
- **R3b — waits on the SourceCA/LicenseCA runtime provisioning path** (a distinct
  planned piece, not a CA bootstrap): deliver CICSourceCA (binary-adjacent) +
  CICLicenseCA (from-license) into the running relay, then verify the module's
  provenance + entitlement through the already-wired `pki_verify.go`, and gate the
  R3a binding on it.

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

## Implementation status

**R3a — landed and live-wired** (`cmd/relay/cic_flow_manifest.go` +
`cic_flow_registry.go`, new code tested 100%):

- the `capabilityManifest` / `moduleProject` types; `egressPolicy` (host
  suffix-glob + method + path + port, empty = deny-all); `bindCICFlowDeps`;
  `checkImportsWithinManifest` (`abi.imports ⊆ manifest`); `verifyBuildHash`.
- **`cicFlowRegistry`** (moduleID → deps): the shared `cic-flow` host module
  resolves the *calling* guest's deps by `mod.Name()` (guests instantiate
  `WithName(moduleID)`); an unregistered module is denied — fail-closed.
- **live wiring**: `loadModule` binds a module's capability via a package-level
  hook, called **only in the cert-verified branch** — an untrusted module never
  gets one. `main` wires the hook to the registry. `resolve` is nil for now:
  egress binds per-module, the signer stays inert until the OCI-key path lands.

**The trust gate is a placeholder**: today it is the existing dev cert-chain verify
against the embedded CICDeveloperCA (`bootstrap.go`). **R3b replaces it** with
CICSourceCA (provenance) + CICLicenseCA (entitlement) via their runtime path.

**R3b provenance — landed** (`cmd/relay/pki_companion.go`): CICSourceCA is
loaded from a **detached companion** (`CIC_SOURCE_CA_BUNDLE`, beside the binary —
never embedded, so signing it after the build never perturbs the binary hash), and
`verifyCICFlowProvenance` gates the cic-flow binding on the module's cert chaining
to it (EKU CodeSigning + CRL, via the already-wired `pki_verify.go`). No SourceCA /
no chain → the module loads but gets no capability (fail-closed, audited "skipped").
CICLicenseCA is loaded the same detached way (`CIC_LICENSE_CA_BUNDLE`, from the
license).

**R3b entitlement — pending** the per-module license-artifact format: the
CICLicenseCA anchor is loaded, but the entitlement *check* (verify a module's
license attestation against it) wires in once that artifact is specified.

## Reuses / touches

- `cmd/relay/cic_flow_host_funcs.go` — `cicFlowDeps`, `hostAllowed` (→ glob).
- `core/nexus/crypto` — `SignOCI` (the per-module signer).
- `core/cabinet/pki_verify.go` — **wired/active** (`VerifyCertificateChain`, called
  from `bootstrap.go:79`/`:508`); R3b feeds it the SourceCA/LicenseCA pools.
- `cmd/relay/pki_bootstrap.go` + `cmd/relay/embedded/pki/` — the dev trust anchor
  (embedded CICDeveloperCA); `EEC/` holds the full CA hierarchy incl. CICSourceCA /
  CICLicenseCA (not embedded — delivered at runtime by design).
- the existing bootstrap-audit "skipped / trust ecosystem not bootstrapped" posture.
