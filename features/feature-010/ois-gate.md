# OIS gate — intent authorization (feature-010, §8.4)

Status: **implemented** (`core/nexus/ois`). The authorization primitive of the
demo's closing act (§8.4): before a state-changing intent — notably a **rollback**
to an earlier, verified state — is applied, it must be authorized by policy. This
is the "OpenIntentSign" seam, mirroring the countersign pattern (a
content-dependent authorization decision) for declarative intents.

## Model

- `Intent{ Actor, Action, TargetRef, Reason }` — a declared, non-secret request:
  who wants to do what (`rollback` / `apply`), targeting which verified
  `CommitRef`, and why.
- `Policy interface { Authorize(Intent) error }` — the seam. `nil` authorizes; a
  `*Denied` (with a non-secret reason) refuses. Real deployments plug richer rules
  (signed intents, RBAC, quorum) into the same interface.
- `AllowlistPolicy` — the trivial policy: an intent is authorized iff its `Actor`
  is allowlisted for its `Action`. A missing actor or target ref is always
  refused. Built with `NewAllowlistPolicy().Allow(action, actors...)`.

## Where it fits

```
select a verified CommitRef (VerifyChain-passing)
  → build a rollback Intent (actor, target, reason)
    → ois.Policy.Authorize(intent)        ← this slice: the gate
      → if authorized: record the restored desired state as a signed commit
        → (real infra) Terraform apply     ← out of scope (infra)
```

The gate decides; the applier knows nothing about the policy — the Relay's
"smartness" lives here, exactly like the countersign policy.

## Tested

100% coverage, gremlins efficacy 100% (full mutator coverage): authorized actor,
wrong-actor / empty-actor / empty-ref / unknown-action refusals, the multi-actor
allowlist, and the `Denied` error. Added to the CI mutation list.

## Next (demo track — the closing act)

- Rollback orchestration: extract a target verified commit's declared state, run
  the OIS gate, and — if authorized — record the restored desired as a new signed
  `intent`/state event, then surface it in `make demo` (§8.4).
