# http-executor — security hardening (slice 3)

Status: **implemented** (`http-executor` crate). Closes the four security-critical
gaps identified in review of slice 2, before the adapter can face real traffic.
Roadmap: seeds of **M43** (`http.request` broker egress policy) and **M44**
(limits/budget). The remaining M43/M44 items (method allowlist, hop-by-hop /
proxy header hygiene, Host-header validation, audit/trace, per-invocation budget)
stay open and are tracked separately.

## The four fixes

### 1. Request + connect timeout (was: unbounded hang)

`reqwest::Client` has **no default timeout**. A slow or silent endpoint could
hang the actuation forever. The client is now built with an explicit
`Limits.request_timeout` (total) and `Limits.connect_timeout`. Defaults mirror the
roadmap M44 numbers: **30 s** request, **10 s** connect. A timeout surfaces as
`ExecuteError::Unavailable` (the endpoint did not complete).

### 2. Response-body size cap (was: unbounded read → memory DoS)

`Response::bytes()` read the whole body into memory with no limit. The body is now
read **incrementally** (`Response::chunk`) and aborted the moment it exceeds
`Limits.max_response_bytes` (default **1 MiB**), returning
`ExecuteError::LimitExceeded`. A declared oversized `Content-Length` is refused up
front, before any body is read.

### 3. Proxy environment ignored (was: honored `HTTP(S)_PROXY`)

`reqwest` honors `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` by default — an
exfiltration / MITM vector the roadmap explicitly forbids ("proxy env:
ignorálva"). The client is now built with `.no_proxy()`, so ambient proxy
environment variables can never redirect an actuation.

### 4. SSRF private-range guard (was: none)

Two layers of defense against reaching internal/link-local/loopback targets:

- **IP-literal targets** are checked at policy time: if the target host is an IP
  literal in a blocked range, the request is `Denied` before any dial (unless the
  policy explicitly permits private IPs — the loopback/testing escape).
- **Hostname targets** are guarded at resolution time by a custom
  `reqwest::dns::Resolve` installed on the client (`GuardedResolver`): every
  resolved address is filtered against the blocked ranges, so a DNS name that
  resolves to a private address cannot be connected to. Because the check runs
  **inside reqwest's own connect path** (not a separate pre-resolve), there is no
  resolve-then-connect TOCTOU window for the addresses reqwest actually dials. A
  hostname that resolves only to blocked addresses fails to connect and surfaces
  as `Unavailable`.

**Blocked ranges** (default-on; overridable only by the explicit private-IP
escape used for loopback testing):

| Family | Ranges |
|---|---|
| IPv4 | loopback `127.0.0.0/8`, private `10.0.0.0/8` · `172.16.0.0/12` · `192.168.0.0/16`, link-local `169.254.0.0/16`, unspecified `0.0.0.0`, broadcast `255.255.255.255` |
| IPv6 | loopback `::1`, unspecified `::`, unique-local `fc00::/7`, link-local unicast `fe80::/10` |
| IPv4-mapped IPv6 | `::ffff:a.b.c.d` is unwrapped and checked as IPv4 (closes the `::ffff:127.0.0.1` bypass) |

## Configuration surface

- `EgressPolicy::allowing(hosts)` — https-only, private IPs blocked (production shape).
- `EgressPolicy::insecure_allowing(hosts)` — also permits `http` **and** private
  IPs; loopback/testing only, never production egress.
- `Limits { request_timeout, connect_timeout, max_response_bytes }` — `Default`
  is 30 s / 10 s / 1 MiB. `HttpExecutor::with_limits(policy, limits)` overrides;
  `HttpExecutor::new(policy)` uses the defaults.

## Error mapping

| Condition | `ExecuteError` |
|---|---|
| malformed / unparseable target | `InvalidRequest` |
| non-https (production policy), host not allowlisted, blocked **IP literal** | `Denied` |
| response exceeds `max_response_bytes` (or declared `Content-Length`) | `LimitExceeded` |
| timeout, connect failure, hostname resolves only to blocked IPs, body read error | `Unavailable` |

No variant carries the secret credential; reqwest error `Display` (which can
include the URL) is never surfaced — messages are coarse and non-secret.

## Slice 4 — request-side hardening (done)

Closes more of the M43/M44 list on the request path:

- **Method allowlist** — `EgressPolicy::with_allowed_methods` narrows the permitted
  methods; a method outside the set is `Denied` before any call. (Default: all
  four, for backward compatibility.)
- **Request-header hygiene** — hop-by-hop framing (`Connection`,
  `Transfer-Encoding`, `Keep-Alive`, `Upgrade`, `TE`, `Trailer`,
  `Proxy-Connection`), proxy-related (`Proxy-Authorization`, `X-Forwarded-For`,
  `X-Real-IP`) and caller-supplied `Authorization` headers are stripped; the
  executor's credential always wins.
- **Host-header validation** — a `Host` header that disagrees with the target is
  `Denied` (no silent redirect); a matching one is dropped (reqwest sets it).
- **Request-body cap** — `Limits.max_request_bytes` (default 1 MiB); an oversized
  body is `LimitExceeded` before any network call.

## Still open

Port policy · per-invocation request budget (multi-request) · configurable
credential placement (beyond Bearer) · `module.open` → executor pipeline wiring
and FFI exposure (done elsewhere — p85/p87).

## Slice 5 — audit/trace (done, M45 seed)

Every actuation can emit a structured, **non-secret** [`AuditRecord`] via a
caller-supplied `AuditSink` (`HttpExecutor::with_audit_sink`): `trace_id` (from
`ExecRequest::with_trace_id`), method, host, `decision` (allow/deny + reason),
status, `duration_ms`, and SHA-256 hex digests of the request/response bodies —
**never** the credential, `Authorization`, a token, or any body contents. The
record is emitted on both the allow and deny paths (`execute` wraps `execute_inner`
to time it and record). This is the M45 seed; wiring the sink into the Go-side
`ProofTrace` / the signed hash-chained audit log (feature-009) is the follow-up.
