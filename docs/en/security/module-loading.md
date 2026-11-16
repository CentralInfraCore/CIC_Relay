# Module loading: signature rules

A WASM module reaches the relay as a triple: `<name>.yaml` (the descriptor), `<name>.wasm` and `<name>.pem` (the signer's **certificate**; it is not a signature over the module). This document says what the relay does with each combination, and which part of it an operator can change. The loader has two entry points (the modules embedded in the binary and the modules installed on disk); both go through the same function, so the same rule applies to both.

## 1. What is checked, and what is not

Two different things are called "signed" in everyday speech; the loader does only the first.

| Check | Done at load | What it proves |
|---|---|---|
| **Certificate chain** of the `.pem`: trusted root, EKU CodeSigning, validity, CRL | **yes** | the module ships with a certificate issued by a trusted CA for code signing |
| WASM digest and size against the `.yaml` descriptor | **yes** | the `.wasm` is the one the descriptor names |
| **Cryptographic signature over the module** (WASM + descriptor/manifest) made with the certificate's key | **no** | would prove that the holder of the certificate approved exactly this artifact and manifest |

So the loader does not yet bind the certificate to the artifact: nothing at load time shows that the certificate holder signed this descriptor, this WASM or this `capability_manifest`. That binding is the open work item [#162](https://github.com/CentralInfraCore/CIC_Relay/issues/162), and it is **not** a guarantee this change provides. This change makes the *certificate* requirement fail-closed; it does not add artifact authentication. Where this document or the README says "signed", read "carries a certificate whose chain verifies".

## 2. The rule

The decision depends on two things only: the trust mode and what the `.pem` file is.

| Case | `prod` | `dev` (default) | `dev` + `CIC_ALLOW_UNSIGNED_MODULES=1` |
|---|---|---|---|
| no `.pem` file (unsigned) | **refused** | **refused** | allowed, **no capability** |
| `.pem` present, certificate chain valid (EKU CodeSigning, CRL, freshness) | allowed | allowed | allowed |
| `.pem` present, not a PEM certificate | **refused** | **refused** | **refused** |
| `.pem` present, certificate cannot be parsed | **refused** | **refused** | **refused** |
| `.pem` present, chain does not verify (untrusted, expired, revoked, wrong usage) | **refused** | **refused** | **refused** |
| `.pem` present, no trust store loaded to verify it | **refused** | **refused** | **refused** |
| `.pem` exists but cannot be read | **refused** | **refused** | **refused** |

`CIC_ALLOW_UNSIGNED_MODULES=1` in `prod` is ignored with a warning: the `prod` column does not change.

The opt-in decides one thing: whether a module that has **no signature file at all** may load. It never makes a present but invalid signature acceptable. A certificate that is there must verify; a malformed, unverifiable or unreadable one is a failure, not "unsigned". (The check is on the certificate chain; see section 1 for what it does not cover.)

## 3. The opt-in

| Variable | Values | Effect |
|---|---|---|
| `CIC_ALLOW_UNSIGNED_MODULES` | unset, empty or `0`: off. `1`: on (`dev` only). Anything else (`true`, `yes`, ` 1`, ...) is a **start-up error** | Admits modules that have no signature file. Logged as a warning at start-up and once per admitted module |

A typo can not open the exception: only the exact value `1` does, and every other value stops the start-up.

An unsigned module that is admitted by the opt-in:

- **gets no capability.** It is not bound to the `git` or `cic-flow` registry, so every call from it to those host modules is denied (see [git-host-functions.md](git-host-functions.md));
- still has its WASM digest checked against the descriptor;
- is recorded in the audit log with `outcome=allowed-unsigned`.

## 4. Operator notes

- The relay's default is fail-closed in both modes. If a developer used to run unsigned modules in `dev`, they must either sign them with the developer CA or set the opt-in explicitly in their own environment.
- Do not set `CIC_ALLOW_UNSIGNED_MODULES` in a shared, long-lived or production-adjacent environment. In `prod` it has no effect, but setting it there only hides which environment is which.
- A module whose certificate is now refused (expired, revoked, untrusted CA) is not rescued by the opt-in. Obtain a valid certificate for it.
- A valid certificate does not authenticate the module content (section 1, #162). Do not treat "loaded in prod" as "the certificate holder approved this exact artifact and manifest".

## 5. What the host surface is

Signed or not, a loaded module can only import the host modules the relay registers: `git`, `cic-flow` and WASI. A test (`TestHostSurface_MatchesThePolicyTable`) enumerates them from the production wiring and compares every function with a reviewed table. It fails when a function appears that the table does not classify, when a classified one disappears, and when a host module is registered in a way the enumeration cannot see. A new host function therefore needs a deliberate, reviewed edit of that table.

## 6. Behaviour changes

Before this change an unsigned module, a module whose signature file was not a PEM certificate, and a signed module loaded while no trust store was present all **loaded** (without a capability). Now they are refused, in `prod` and, by default, in `dev`. This is the second half of the fix for [GHSA-5489-jhgc-m947](https://github.com/CentralInfraCore/CIC_Relay/security/advisories/GHSA-5489-jhgc-m947): the capability binding was already fail-closed, the loading itself now is too.
