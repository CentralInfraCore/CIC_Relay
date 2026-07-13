# `http.request` capability — Setx-driven actuation (feature-009)

Status: **implemented** (`core/modules/httpcap`, cabinet integration). The
relay-integration span of the audit bridge: the HTTP actuation is now a **native
cabinet capability** that `Setx` drives as an ordinary workflow step — so a
capability call folds into the same hash-chained `ProofTrace` (and, via the
recorder, the same Vault-signed Git commit) as any native or WASM step, with no
special-casing.

## Shape

- `core/modules/httpcap.Actuator` — the seam. One method,
  `RunFlow(reqJSON []byte) (resJSON []byte, error)`, matching the Rust FFI
  `run_flow` document byte-for-byte. The production implementation is the cgo FFI
  (`cicffi.RunFlow`), injected at relay startup; `httpcap` and the cabinet never
  import cgo, so the default build stays pure-Go. `ActuatorFunc` adapts a plain
  function (or a test double).
- `NewRequestFunc(actuator)` — returns the native module function
  (`func(context.Context, interface{}) (interface{}, error)`). It marshals the
  step input as the `run_flow` request, actuates, and returns a non-secret
  `Result` (status, base64 body, countersigned scope, and the `Audit` record with
  its tamper-evident `record_hash`). A refused actuation (policy deny, SSRF block,
  limit, transport error) is surfaced as a step error; the credential and body
  contents never cross into the result. A nil actuator yields `ErrNoActuator`.

## Why this is the whole point

Setx already fingerprints every native step (input hash → dispatch → output
hash → `ProofTraceStep`) and chains it into the `ProofTrace`. So registering the
actuation as a native module means:

```
workflow step  http@1.0.request(input)
  → Setx hashes input → httpcap.NewRequestFunc → Actuator (FFI run_flow)
    → Setx hashes output → ProofTraceStep → ChainHash
      → ExecutionRecorder → Vault-signed Git commit   (existing machinery)
```

No bespoke audit path: a capability actuation is auditable and durable exactly
like any other step.

## Tested

- `httpcap` unit tests: success (non-secret `Result`, secret never in output),
  refused actuation → error, nil actuator → `ErrNoActuator`, actuator error /
  garbage result / unmarshalable input → error. Coverage 100%, gremlins efficacy
  100% (added to the CI mutation list).
- Cabinet integration (`core/cabinet/httpcap_integration_test.go`): a real
  `Setx` runs a `http@1.0.request(input)` workflow through a mock `Actuator`,
  asserting the credential reaches the actuator (Relay→Rust), exactly one chained
  `ProofTrace` step is produced (`module = http@1.0`), the `ChainHash` is
  reproducible, and the credential never reaches the trace.

## Deploy span (done)

The capability is now wired into the relay. `cmd/relay`:

- `newActuator()` is build-tag split: `actuator_stub.go` (`!ffi_cgo`) returns
  `nil`; `actuator_cgo.go` (`ffi_cgo`) returns an `ffiActuator` whose `RunFlow`
  **is** `cicffi.RunFlow`. So the cabinet never imports cgo, and the default relay
  is unchanged.
- `registerHTTPCapability(ctx, svc, actuator)` (called from
  `registerNativeComponents` with `newActuator()`) registers `http@1.0` with a
  `request` function when an actuator is present, and is a no-op for `nil`. A
  cgo-built relay therefore serves the live `http@1.0.request` capability
  end-to-end; the default (non-cgo) relay omits it.

Tested: default-build unit tests (`newActuator()` nil, `registerHTTPCapability`
no-op / registers / error paths — cmd/relay coverage 89.7% ≥ 80) and a cgo test
(`actuator_cgo_test.go`, run by `make test-ffi` against the linked staticlib):
the actuator is non-nil and `http@1.0` is registered with `request`. The
`registerNativeComponents` module-count assertion is adaptive (10, or 11 under
`ffi_cgo`).

## Still open (deliberate)

- **Audit on the deny path through the capability** (see the bridge doc): a
  refused capability call fails the workflow step, so `Setx` aborts without
  recording the deny `ProofTrace`.
