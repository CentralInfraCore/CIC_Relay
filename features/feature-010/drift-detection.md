# Drift detection — desired ↔ actual classification (feature-010)

Status: **implemented** (`core/nexus/drift`). The classification core of the §8.2–§8.3
demo: given a **declared (desired)** state and the **observed (actual)** state,
it returns a deterministic drift verdict — the "Declarative Accountability"
primitive. This is the engine; recording the verdict into the signed state chain
(§8.2's `drift: true` commits) is the next slice.

## Classification

| Type | Meaning |
|---|---|
| `NONE` | actual matches desired exactly |
| `SOFT_DRIFT` | desired fully satisfied, but actual carries **extra** state beyond it — reconcilable by pruning |
| `RECONCILIABLE_DRIFT` | a desired key is **missing or changed** — reconcilable by re-applying desired |
| `HARD_DRIFT` | there is **no observable actual state** (the infra is gone) — cannot be derived from the last valid ProofTrace |

Precedence: an empty/nil actual is always `HARD_DRIFT`; otherwise a missing or
changed desired key makes it `RECONCILIABLE_DRIFT`; extra-only difference is
`SOFT_DRIFT`; an exact match is `NONE`.

## API

- `drift.Analyze(desired, actual map[string]interface{}) Report` — the full,
  **deterministic** result: the `Type` plus the exact `Missing` / `Changed` /
  `Extra` keys (each sorted, so the same inputs always yield the same report — a
  `ProofTrace` prerequisite).
- `drift.Classify(desired, actual)` — convenience over `Analyze` returning only
  the `Type`.

## Tested

100% coverage, gremlins efficacy 100%: exact match, extra-key soft drift,
changed-value and missing-key reconciliable drift, nil/empty hard drift, the
changed-takes-precedence-over-extra rule, and the deterministic sorted report.

## Drift recording (done)

`GitStateRecorder.RecordDrift(ctx, runID, desired, actual)` classifies the drift
(`drift.Analyze`) and commits it as a **Vault-signed, parent-linked** state entry
whose `meta.json` carries the verdict (`drift`, `drift_missing`, `drift_changed`,
`drift_extra`) — so the drift itself is an **auditable, signed event** (§8.2
"drift: true"). `expected.json` holds the desired state and `actual.json` the
observed one, so the standard `VerifyChain` verifies drift commits **unchanged**;
`RecordState` and `RecordDrift` share one `signAndCommit` and can be mixed in one
chain. Tested: NONE/RECONCILIABLE/HARD classification persisted and signed, the
verdict readable from the committed meta, and a mixed chain verifies (recorder
coverage 86.4%).

## Next (demo track)

- Surface drift in `make demo` (in-sync → drift on a manual change → HARD_DRIFT on
  delete), then the `intent/`-branch rollback + OIS gate (§8.4).
