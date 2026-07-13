# FFI audit bridge — actuation audit → Go ProofTrace (feature-009)

Status: **implemented** (`cic-ffi` run_flow, `pkg/cicffi`, `core/cabinet`). The
first span of the bridge from the Rust-side audit chain to the running Go relay:
an actuation's non-secret audit record now **crosses the FFI seam** and folds
into the Go relay's `ProofTrace` vocabulary.

## The span

```
http-executor actuation
  └─ HashChainedAuditSink → ChainedEntry (record + record_hash)   [Rust]
      └─ run_flow emits it as the outcome's `audit` object        [FFI, ABI v3]
          └─ ParseRunFlowResult → RunFlowResult.Audit             [Go, pkg/cicffi]
              └─ ProofTraceStepFromActuation → ProofTraceStep     [Go, core/cabinet]
                  └─ ComputeChainHashV1                           [existing ProofTrace]
```

## Rust side (`cic-ffi`)

`run_flow` attaches a `HashChainedAuditSink` to the `HttpExecutor` in the shared
`actuate` path (so both the mock and live profiles get it), captures the head
`ChainedEntry`, and adds an `audit` object to the success outcome:

```json
{ "ok": true, "status": 200, "body_base64": "...", "scope": ["db.read"],
  "audit": { "trace_id": "...", "method": "GET", "host": "127.0.0.1",
             "decision": "allow", "status": 200, "duration_ms": 7,
             "request_body_sha256": "...", "response_body_sha256": "...",
             "seq": 0, "record_hash": "..." } }
```

The request may carry a `trace_id`; it is applied via `ExecRequest::with_trace_id`
and echoed into the audit so the Go relay can bind the actuation to its own
`ProofTrace`. The `audit` object carries **only digests and facts** — never the
credential, the `Authorization` header, a Vault token, or any body contents. The
outcome shape changed, so the C-ABI version is bumped **v2 → v3** (Go
`ExpectedABIVersion` kept in sync, `CheckABI` guards a mismatch).

## Go side

- `pkg/cicffi/result.go` (no build tag — plain Go, testable without the
  staticlib): `ActuationAudit`, `RunFlowResult`, `ParseRunFlowResult`.
- `core/cabinet.ProofTraceStepFromActuation(traceID, method, host,
  requestBodySHA256, responseBodySHA256)` maps the actuation audit into a
  `ProofTraceStep` (body digests → input/output hashes; name = trace id, or
  `"<method> <host>"` when none). An actuation now folds into the **same**
  hash-chained `ProofTrace` as native and WASM steps.

## Tested

- Rust: `run_flow` happy path asserts the `audit` object (trace_id round-trip,
  `record_hash` 64-hex, decision/method/host/status); a unit test covers the
  `deny:<reason>` projection. cic-ffi coverage 98.6%, mutation 0 missed.
- Go (non-cgo): `ParseRunFlowResult` success/failure/garbage;
  `ProofTraceStepFromActuation` mapping + chain-hash fold.
- Go (cgo, `make test-ffi`): the live Go→Rust→Go path parses the audit and maps
  it into a `ProofTraceStep` + `ComputeChainHashV1`, and re-asserts the secret
  never crosses back.

## Still open (deliberate)

- **Audit on the deny path**: today the `audit` object is emitted only on the
  success path (`ok:true`); a denied/failed actuation still returns
  `{"ok":false,"error":...}` without audit. Threading the audit through the error
  path is the next micro-slice.
- **Persistence**: wiring the folded `ProofTraceStep` / the `record_hash` anchor
  into the `core/nexus/recorder` (Git-audit + Vault-signed commit) so the chain
  is durable — the recorder span of the bridge.
- **Relay-driven actuation**: making the relay's `Setx` invoke the `http.request`
  capability through this seam (rather than the standalone PoC), so real
  workflows produce these steps.
