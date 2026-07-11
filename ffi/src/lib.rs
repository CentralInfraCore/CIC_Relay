//! # cic-ffi — the unsafe airlock
//!
//! The single crate in the workspace permitted to use `unsafe`. It is the C-ABI
//! seam that links the safe Rust trust-flow crates (`connections`, `module`,
//! `executor`, `http-executor`) into the Go relay. (`http-executor` is linked
//! **without** its TLS feature, so the staticlib stays pure-Rust and the Go cgo
//! build needs no C library.)
//!
//! Every export routes through one shared airlock ([`marshal`]) that fixes the
//! three phases from `features/feature-008/ffi-boundary.md`:
//!
//! 1. **INGRESS** (`unsafe`): copy the caller's bytes into an *owned* Rust value.
//!    No borrow into caller memory escapes this phase.
//! 2. **CORE** (safe): a plain `fn(&[u8]) -> Option<Vec<u8>>` — no raw pointers,
//!    no `unsafe`, panic-contained via [`std::panic::catch_unwind`]. `None`
//!    means "rejected" (validation failure), never a crash.
//! 3. **EGRESS** (`unsafe`): hand an owned buffer back to the caller as
//!    `ptr` + `len`; the caller releases it via [`cic_ffi_free`].
//!
//! `unsafe` therefore lives only in [`marshal`] and [`cic_ffi_free`], and only
//! for pointer/length handling — never business logic. The trust-flow crates
//! stay `unsafe_code = "forbid"`.

use std::panic::{catch_unwind, UnwindSafe};
use std::ptr;
use std::slice;

/// Maximum input accepted at the seam (1 MiB). Control-plane requests are tiny;
/// anything larger is rejected on ingress rather than copied (ffi-boundary.md
/// rule 3 — validate/size-cap at the seam).
const MAX_INPUT_LEN: usize = 1024 * 1024;

/// C-ABI version of this staticlib. Bump on any breaking change to an export's
/// signature or semantics; the Go side checks it against its own expectation to
/// catch a stale or mismatched library link. v2 adds `cic_ffi_run_flow`.
const CIC_FFI_ABI_VERSION: u32 = 2;

/// The safe core. Contains no `unsafe` and no raw pointers — every function here
/// takes owned/borrowed-owned bytes and returns owned bytes, so it is ordinary,
/// machine-checked safe Rust dispatching into the trust-flow crates.
mod logic {
    use connections::vault::{AuthScope, Cn, Connection};
    use serde_json::{json, Value};

    /// Identity echo — the linkability smoke test for the Rust ↔ Go bridge.
    // Always `Some`, but the `Option` return is the uniform core contract the
    // airlock ([`super::marshal`]) dispatches; keeping it matches every other
    // core function's signature.
    #[allow(clippy::unnecessary_wraps)]
    pub fn echo(input: &[u8]) -> Option<Vec<u8>> {
        Some(input.to_vec())
    }

    /// Build a Vault Connection request (trust-flow step 1) from a JSON
    /// `{"subject": "...", "scope": ["cn", ...]}` document and return its
    /// **canonical** form: the scope is deduplicated and sorted by
    /// [`AuthScope`]'s `BTreeSet`, so the output is reproducible (a `ProofTrace`
    /// prerequisite). Returns `None` — a clean rejection, never a panic — on
    /// malformed JSON, a missing/non-string subject, or an empty subject.
    pub fn connection_request(input: &[u8]) -> Option<Vec<u8>> {
        let doc: Value = serde_json::from_slice(input).ok()?;

        let subject = doc.get("subject")?.as_str()?.trim();
        if subject.is_empty() {
            return None;
        }

        let cns: Vec<Cn> = doc
            .get("scope")
            .and_then(Value::as_array)
            .map(|arr| {
                arr.iter()
                    .filter_map(Value::as_str)
                    .filter(|s| !s.is_empty())
                    .map(Cn::new)
                    .collect()
            })
            .unwrap_or_default();

        let conn = Connection::request(Cn::new(subject), AuthScope::from_cns(cns));

        let canonical = json!({
            "subject": conn.subject().as_str(),
            "scope": conn.scope().cns().map(Cn::as_str).collect::<Vec<_>>(),
        });
        serde_json::to_vec(&canonical).ok()
    }
}

/// End-to-end trust-flow `PoC`, run entirely inside Rust and driven from Go: mint a
/// module identity + CSR, mock-countersign it, seal a (mock Vault) secret to the
/// module's public key, open it *inside* the module, and actuate over HTTP with
/// the opened credential. Mock trust backends (no live Vault); a real HTTP call.
///
/// The point the seam demonstrates: Go hands in the request, Rust runs the whole
/// pipeline, and only the HTTP **outcome** crosses back — the secret is sealed in
/// transit and opened only inside the module, never leaving as data.
mod flow {
    use std::collections::BTreeMap;
    use std::sync::OnceLock;

    use base64::Engine;
    use connections::vault::port::{Countersigner, InMemoryCountersigner};
    use connections::vault::{AuthScope, Cn, Connection, SecretRef};
    use executor::{ExecOutcome, ExecRequest, Method};
    use http_executor::{EgressPolicy, HttpExecutor, Limits};
    use module::{seal, ModuleIdentity};
    use serde_json::{json, Value};
    use tokio::runtime::Runtime;

    /// Standard base64 for the response body carried in the JSON result.
    fn b64() -> base64::engine::general_purpose::GeneralPurpose {
        base64::engine::general_purpose::STANDARD
    }

    /// A shared multi-thread runtime that drives the async actuation to completion
    /// from inside the synchronous airlock. Built once; the seam is called from a
    /// Go (non-tokio) thread, so `block_on` never nests inside a runtime.
    fn runtime() -> &'static Runtime {
        static RT: OnceLock<Runtime> = OnceLock::new();
        RT.get_or_init(|| {
            tokio::runtime::Builder::new_multi_thread()
                .worker_threads(1)
                .enable_all()
                .build()
                .expect("build tokio runtime")
        })
    }

    /// The parsed, validated request. Bundling the fields keeps `execute_flow`
    /// from taking a long argument list (and models the request as one value).
    struct FlowInput {
        subject: String,
        scope: AuthScope,
        secret: String,
        method: Method,
        target: String,
        headers: BTreeMap<String, String>,
        body: Vec<u8>,
        allow_hosts: Vec<String>,
        insecure: bool,
    }

    /// Parses the request JSON. Returns `None` (a clean rejection) on malformed
    /// JSON, a missing/empty required field, or an undecodable `body_base64`.
    fn parse(doc: &Value) -> Option<FlowInput> {
        let subject = doc.get("subject")?.as_str()?.trim().to_owned();
        if subject.is_empty() {
            return None;
        }
        let secret = doc.get("secret")?.as_str()?.to_owned();
        let target = doc.get("target")?.as_str()?.to_owned();
        if target.is_empty() {
            return None;
        }
        let body = match doc.get("body_base64").and_then(Value::as_str) {
            None | Some("") => Vec::new(),
            Some(encoded) => b64().decode(encoded).ok()?,
        };
        Some(FlowInput {
            subject,
            scope: parse_scope(doc),
            secret,
            method: parse_method(doc.get("method").and_then(Value::as_str)),
            target,
            headers: parse_headers(doc),
            body,
            allow_hosts: parse_allow_hosts(doc),
            insecure: doc
                .get("insecure")
                .and_then(Value::as_bool)
                .unwrap_or(false),
        })
    }

    fn parse_scope(doc: &Value) -> AuthScope {
        let cns = doc
            .get("scope")
            .and_then(Value::as_array)
            .map(|arr| {
                arr.iter()
                    .filter_map(Value::as_str)
                    .filter(|s| !s.is_empty())
                    .map(Cn::new)
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default();
        AuthScope::from_cns(cns)
    }

    fn parse_method(raw: Option<&str>) -> Method {
        match raw.map(str::to_ascii_uppercase).as_deref() {
            Some("POST") => Method::Post,
            Some("PUT") => Method::Put,
            Some("DELETE") => Method::Delete,
            _ => Method::Get,
        }
    }

    fn parse_headers(doc: &Value) -> BTreeMap<String, String> {
        doc.get("headers")
            .and_then(Value::as_object)
            .map(|obj| {
                obj.iter()
                    .filter_map(|(k, v)| v.as_str().map(|s| (k.clone(), s.to_owned())))
                    .collect()
            })
            .unwrap_or_default()
    }

    fn parse_allow_hosts(doc: &Value) -> Vec<String> {
        doc.get("allow_hosts")
            .and_then(Value::as_array)
            .map(|arr| {
                arr.iter()
                    .filter_map(Value::as_str)
                    .map(str::to_owned)
                    .collect()
            })
            .unwrap_or_default()
    }

    /// Runs the full pipeline, returning the actuation outcome and the scope the
    /// (mock) Relay countersigned. Every failure is a non-secret `String`.
    fn execute_flow(input: FlowInput) -> Result<(ExecOutcome, Vec<String>), String> {
        let subject = Cn::new(input.subject);

        // 1. Module mints a per-request identity + CSR; the private key stays in.
        let (identity, request) =
            ModuleIdentity::generate(subject.clone(), input.scope.clone()).map_err(err_string)?;

        // 2. The Relay countersigns (mock policy: permit the requested scope).
        let cert = InMemoryCountersigner::new(input.scope.clone())
            .countersign(&request)
            .map_err(err_string)?;
        let authorized: Vec<String> = cert.scope().cns().map(|c| c.as_str().to_owned()).collect();

        // 3. Seal the (mock Vault) secret to the module's public key.
        let pubkey = identity.public_key_der().map_err(err_string)?;
        let sealed = seal(
            &pubkey,
            SecretRef::new("secret", "poc"),
            input.secret.as_bytes(),
        )
        .map_err(err_string)?;

        // 4. Advance the connection typestate and open the token *inside* the
        //    module — the plaintext exists only here, never as data outside.
        let connection = Connection::request(subject, input.scope)
            .countersign(cert)
            .initialize(sealed);
        let credential = identity.open(connection.token()).map_err(err_string)?;

        // 5. Actuate over HTTP, applying the opened credential.
        let policy = if input.insecure {
            EgressPolicy::insecure_allowing(input.allow_hosts)
        } else {
            EgressPolicy::allowing(input.allow_hosts)
        };
        let executor = HttpExecutor::with_limits(policy, Limits::default()).map_err(err_string)?;
        let mut req = ExecRequest::new(input.method, input.target).with_body(input.body);
        for (name, value) in input.headers {
            req = req.with_header(name, value);
        }
        let outcome = runtime()
            .block_on(executor.execute(&req, &credential))
            .map_err(err_string)?;
        Ok((outcome, authorized))
    }

    /// Renders any flow error as its (non-secret) Display string.
    fn err_string(e: impl std::fmt::Display) -> String {
        e.to_string()
    }

    /// The airlock core for `cic_ffi_run_flow`. Returns `None` only on malformed
    /// input; a flow that runs but fails (policy/HTTP) yields `{"ok":false,...}`.
    pub fn run_flow(input: &[u8]) -> Option<Vec<u8>> {
        let doc: Value = serde_json::from_slice(input).ok()?;
        let parsed = parse(&doc)?;
        let result = match execute_flow(parsed) {
            Ok((outcome, scope)) => json!({
                "ok": true,
                "status": outcome.status,
                "body_base64": b64().encode(&outcome.body),
                "scope": scope,
            }),
            Err(message) => json!({ "ok": false, "error": message }),
        };
        serde_json::to_vec(&result).ok()
    }

    #[cfg(test)]
    mod tests {
        use super::{parse_allow_hosts, parse_headers, parse_method, parse_scope, run_flow};
        use base64::Engine;
        use executor::Method;
        use serde_json::Value;
        use std::io::{Read, Write};
        use std::net::TcpListener;

        /// Binds a loopback listener and answers exactly one HTTP request with a
        /// fixed raw response. Returns the port. (A tiny real HTTP server, so the
        /// happy path exercises the actual reqwest transport.)
        fn spawn_http_once(raw_response: &'static str) -> u16 {
            let listener = TcpListener::bind("127.0.0.1:0").expect("bind loopback");
            let port = listener.local_addr().expect("addr").port();
            std::thread::spawn(move || {
                if let Ok((mut stream, _)) = listener.accept() {
                    let mut buf = [0u8; 2048];
                    let _ = stream.read(&mut buf); // drain the request line + headers
                    let _ = stream.write_all(raw_response.as_bytes());
                }
            });
            port
        }

        #[test]
        fn parse_helpers_extract_request_fields() {
            let doc = serde_json::json!({
                "headers": { "x-a": "1", "x-b": "2" },
                "scope": ["b", "a", "b"],
                "allow_hosts": ["h1", "h2"],
            });
            let headers = parse_headers(&doc);
            assert_eq!(headers.get("x-a").map(String::as_str), Some("1"));
            assert_eq!(headers.get("x-b").map(String::as_str), Some("2"));
            assert!(parse_headers(&serde_json::json!({})).is_empty());

            let scope: Vec<String> = parse_scope(&doc)
                .cns()
                .map(|c| c.as_str().to_owned())
                .collect();
            assert_eq!(scope, ["a", "b"]); // deduped + sorted

            assert_eq!(parse_allow_hosts(&doc), ["h1".to_owned(), "h2".to_owned()]);
            assert!(parse_allow_hosts(&serde_json::json!({})).is_empty());
        }

        #[test]
        fn parse_method_maps_verbs_and_defaults_to_get() {
            assert_eq!(parse_method(Some("post")), Method::Post);
            assert_eq!(parse_method(Some("PUT")), Method::Put);
            assert_eq!(parse_method(Some("delete")), Method::Delete);
            assert_eq!(parse_method(Some("get")), Method::Get);
            assert_eq!(parse_method(None), Method::Get);
            assert_eq!(parse_method(Some("weird")), Method::Get);
        }

        #[test]
        fn happy_path_runs_the_whole_chain_and_returns_the_outcome() {
            let port = spawn_http_once("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\npong");
            let input = serde_json::json!({
                "subject": "relay.worker",
                "scope": ["db.read"],
                "secret": "hvs.TOPSECRET",
                "target": format!("http://127.0.0.1:{port}/x"),
                "allow_hosts": ["127.0.0.1"],
                "insecure": true,
            })
            .to_string();

            let out = run_flow(input.as_bytes()).expect("produces a result");
            let v: Value = serde_json::from_slice(&out).expect("json");
            assert_eq!(v["ok"], true);
            assert_eq!(v["status"], 200);
            assert_eq!(
                v["body_base64"],
                base64::engine::general_purpose::STANDARD.encode("pong")
            );
            assert_eq!(v["scope"], serde_json::json!(["db.read"]));
            // The secret is sealed/opened internally and never crosses back out.
            assert!(!String::from_utf8_lossy(&out).contains("hvs.TOPSECRET"));
        }

        #[test]
        fn malformed_json_is_rejected() {
            assert!(run_flow(b"not json at all").is_none());
        }

        #[test]
        fn missing_required_field_is_rejected() {
            let input = serde_json::json!({ "secret": "x", "target": "http://127.0.0.1/x" });
            assert!(run_flow(input.to_string().as_bytes()).is_none());
        }

        #[test]
        fn bad_body_base64_is_rejected() {
            let input = serde_json::json!({
                "subject": "m", "secret": "x",
                "target": "http://127.0.0.1:1/x", "body_base64": "!!! not base64 !!!",
            });
            assert!(run_flow(input.to_string().as_bytes()).is_none());
        }

        #[test]
        fn denied_host_is_a_structured_error_not_a_rejection() {
            let input = serde_json::json!({
                "subject": "m", "secret": "x",
                "target": "https://evil.example/x", "allow_hosts": ["ok.example"],
            });
            let out = run_flow(input.to_string().as_bytes()).expect("produces a result");
            let v: Value = serde_json::from_slice(&out).expect("json");
            assert_eq!(v["ok"], false);
            assert!(v["error"].as_str().expect("error").contains("denied"));
        }

        #[test]
        fn unreachable_target_fails_after_the_crypto_chain_without_leaking() {
            // The whole generate → countersign → seal → open chain runs; only the
            // HTTP call fails (nothing on port 1). Proves the credential opened.
            let input = serde_json::json!({
                "subject": "m", "scope": ["db.read"], "secret": "hvs.NEEDLE",
                "target": "http://127.0.0.1:1/x", "allow_hosts": ["127.0.0.1"], "insecure": true,
            });
            let out = run_flow(input.to_string().as_bytes()).expect("produces a result");
            let v: Value = serde_json::from_slice(&out).expect("json");
            assert_eq!(v["ok"], false);
            assert!(!v["error"].as_str().expect("error").is_empty());
            assert!(!String::from_utf8_lossy(&out).contains("hvs.NEEDLE"));
        }
    }
}

/// The shared airlock. INGRESS (copy + own) → CORE (safe, panic-contained) →
/// EGRESS (owned buffer to the caller). `core` is a safe function over owned
/// bytes; `None` from it (or a panic, or empty output) yields a null return.
///
/// # Safety
///
/// `ptr` must point to `len` valid bytes (or be null with `len == 0`), and
/// `out_len` must be a valid, writable `*mut usize` (or null).
unsafe fn marshal(
    ptr: *const u8,
    len: usize,
    out_len: *mut usize,
    core: impl FnOnce(&[u8]) -> Option<Vec<u8>> + UnwindSafe,
) -> *mut u8 {
    if out_len.is_null() {
        return ptr::null_mut();
    }
    // SAFETY: `out_len` checked non-null just above; single write.
    *out_len = 0;

    // Reject oversized input at the seam, before allocating/copying it.
    if len > MAX_INPUT_LEN {
        return ptr::null_mut();
    }

    // ---- INGRESS: copy + own immediately; no borrow escapes. ----
    let owned: Vec<u8> = if ptr.is_null() || len == 0 {
        Vec::new()
    } else {
        // SAFETY: the caller guarantees `ptr` points to `len` valid bytes. We
        // copy straight into an owned `Vec` and never retain the raw pointer.
        slice::from_raw_parts(ptr, len).to_vec()
    };

    // ---- CORE: safe, panic-contained. `Err` (panic) or `None` (rejection). ----
    // The backstop recovers only in unwinding (dev/test) builds; the release
    // profile is `panic = "abort"`, so a panic aborts before reaching here.
    let Ok(Some(result)) = catch_unwind(move || core(&owned)) else {
        return ptr::null_mut();
    };
    if result.is_empty() {
        return ptr::null_mut();
    }

    // ---- EGRESS: hand an owned buffer to the caller. ----
    let boxed = result.into_boxed_slice();
    let produced = boxed.len();
    let raw = Box::into_raw(boxed).cast::<u8>();
    // SAFETY: `out_len` checked non-null at the top; single write.
    *out_len = produced;
    raw
}

/// Release a buffer previously returned by a `cic_ffi_*` allocating export.
///
/// A null pointer or a zero length is a no-op, so it is always safe to call
/// even when an export returned "nothing".
///
/// # Safety
///
/// `ptr` and `len` must be exactly the pair returned by a `cic_ffi_*` export,
/// and this function must be called at most once for that pair.
#[no_mangle]
pub unsafe extern "C" fn cic_ffi_free(ptr: *mut u8, len: usize) {
    if ptr.is_null() || len == 0 {
        return;
    }
    // SAFETY: by the contract above, `ptr`/`len` came from `into_boxed_slice`
    // in [`marshal`] (so capacity == len) and is released at most once.
    // Rebuilding the `Box<[u8]>` lets Rust drop it with the correct layout.
    let slice = ptr::slice_from_raw_parts_mut(ptr, len);
    drop(Box::from_raw(slice));
}

/// Echo `len` bytes from `ptr` back to the caller — the airlock linkability
/// smoke test. Result must be released with [`cic_ffi_free`].
///
/// # Safety
///
/// See [`marshal`].
#[no_mangle]
pub unsafe extern "C" fn cic_ffi_ping(ptr: *const u8, len: usize, out_len: *mut usize) -> *mut u8 {
    marshal(ptr, len, out_len, logic::echo)
}

/// Build a Vault Connection request and return its canonical JSON form — the
/// first real trust-flow function bound through the seam.
///
/// Input: a JSON `{"subject": "...", "scope": [...]}` document (`ptr`/`len`).
/// Output: canonical JSON with a deduplicated, sorted scope, allocated for the
/// caller to release with [`cic_ffi_free`]. Returns null (and `*out_len == 0`)
/// on malformed input, an empty subject, a null `out_len`, or a panic.
///
/// # Safety
///
/// See [`marshal`].
#[no_mangle]
pub unsafe extern "C" fn cic_ffi_connection_request(
    ptr: *const u8,
    len: usize,
    out_len: *mut usize,
) -> *mut u8 {
    marshal(ptr, len, out_len, logic::connection_request)
}

/// Run the end-to-end trust-flow `PoC` and return its JSON result.
///
/// Input: a JSON request (`ptr`/`len`) — `subject`, `scope[]`, `secret`,
/// `target`, `allow_hosts[]`, optional `method`/`headers`/`body_base64`/
/// `insecure`. Output: JSON `{"ok":true,"status":..,"body_base64":..,"scope":[..]}`
/// on a completed actuation, or `{"ok":false,"error":".."}` if the flow ran but
/// failed (policy/HTTP). Returns null (and `*out_len == 0`) only on malformed
/// input, a null `out_len`, or a panic. Release the result with [`cic_ffi_free`].
///
/// # Safety
///
/// See [`marshal`].
#[no_mangle]
pub unsafe extern "C" fn cic_ffi_run_flow(
    ptr: *const u8,
    len: usize,
    out_len: *mut usize,
) -> *mut u8 {
    marshal(ptr, len, out_len, flow::run_flow)
}

/// Returns the C-ABI version of this staticlib. The Go side checks it against
/// its own expectation at startup to catch a stale or mismatched link. No
/// pointers cross here, so it needs no airlock.
#[no_mangle]
pub extern "C" fn cic_ffi_abi_version() -> u32 {
    CIC_FFI_ABI_VERSION
}

#[cfg(test)]
mod tests {
    use super::{
        cic_ffi_abi_version, cic_ffi_connection_request, cic_ffi_free, cic_ffi_ping,
        cic_ffi_run_flow,
    };
    use serde_json::{json, Value};
    use std::ptr;
    use std::slice;

    #[test]
    fn ping_round_trips_bytes() {
        let input = b"hello-cic";
        let mut out_len: usize = 0;
        // SAFETY: `input` is a valid slice; `out_len` is a valid local.
        let out = unsafe { cic_ffi_ping(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(!out.is_null());
        assert_eq!(out_len, input.len());
        // SAFETY: `out`/`out_len` are the pair just returned by the export.
        let got = unsafe { slice::from_raw_parts(out, out_len) };
        assert_eq!(got, input);
        // SAFETY: releasing the exact pair returned above, exactly once.
        unsafe { cic_ffi_free(out, out_len) };
    }

    #[test]
    fn ping_empty_input_returns_null() {
        let mut out_len: usize = 7;
        // SAFETY: null input with zero length is an explicitly supported call.
        let out = unsafe { cic_ffi_ping(ptr::null(), 0, &raw mut out_len) };
        assert!(out.is_null());
        assert_eq!(out_len, 0);
    }

    #[test]
    fn ping_null_out_len_returns_null() {
        let input = b"x";
        // SAFETY: `input` is valid; a null `out_len` is an explicitly handled case.
        let out = unsafe { cic_ffi_ping(input.as_ptr(), input.len(), ptr::null_mut()) };
        assert!(out.is_null());
    }

    #[test]
    fn free_null_is_noop() {
        // SAFETY: null/zero is a documented no-op.
        unsafe { cic_ffi_free(ptr::null_mut(), 0) };
    }

    #[test]
    fn connection_request_canonicalizes_scope() {
        let input = br#"{"subject":"mod-x","scope":["cn-b","cn-a","cn-b"]}"#;
        let mut out_len: usize = 0;
        // SAFETY: `input` is a valid slice; `out_len` is a valid local.
        let out =
            unsafe { cic_ffi_connection_request(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(!out.is_null());
        // SAFETY: `out`/`out_len` are the pair just returned by the export.
        let got = unsafe { slice::from_raw_parts(out, out_len) }.to_vec();
        // SAFETY: releasing the exact pair returned above, exactly once.
        unsafe { cic_ffi_free(out, out_len) };

        let v: Value = serde_json::from_slice(&got).expect("valid JSON out");
        assert_eq!(v["subject"], "mod-x");
        // deduped ("cn-b" once) + sorted (BTreeSet) regardless of input order
        assert_eq!(v["scope"], json!(["cn-a", "cn-b"]));
    }

    #[test]
    fn connection_request_rejects_empty_subject() {
        let input = br#"{"subject":"","scope":[]}"#;
        let mut out_len: usize = 9;
        // SAFETY: `input` is valid; `out_len` is a valid local.
        let out =
            unsafe { cic_ffi_connection_request(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(out.is_null());
        assert_eq!(out_len, 0);
    }

    #[test]
    fn connection_request_rejects_malformed_json() {
        let input = b"not json at all";
        let mut out_len: usize = 9;
        // SAFETY: `input` is valid; `out_len` is a valid local.
        let out =
            unsafe { cic_ffi_connection_request(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(out.is_null());
    }

    #[test]
    fn ping_rejects_oversized_input() {
        let big = vec![b'x'; super::MAX_INPUT_LEN + 1];
        let mut out_len: usize = 9;
        // SAFETY: `big` is a valid slice; `out_len` is a valid local.
        let out = unsafe { cic_ffi_ping(big.as_ptr(), big.len(), &raw mut out_len) };
        assert!(out.is_null());
        assert_eq!(out_len, 0);
    }

    #[test]
    fn ping_accepts_input_exactly_at_the_cap() {
        // Hard-codes the documented 1 MiB cap (rather than `super::MAX_INPUT_LEN`)
        // so this test still pins the real boundary if the constant's definition
        // is ever miscomputed.
        let at_cap = vec![b'y'; 1024 * 1024];
        let mut out_len: usize = 0;
        // SAFETY: `at_cap` is a valid slice; `out_len` is a valid local.
        let out = unsafe { cic_ffi_ping(at_cap.as_ptr(), at_cap.len(), &raw mut out_len) };
        assert!(!out.is_null());
        assert_eq!(out_len, at_cap.len());
        // SAFETY: releasing the exact pair returned above, exactly once.
        unsafe { cic_ffi_free(out, out_len) };
    }

    #[test]
    fn run_flow_export_returns_structured_error_through_the_seam() {
        // Denied host needs no network: the flow runs and fails in policy, so the
        // seam returns a non-null JSON result (not a rejection).
        let input = br#"{"subject":"m","secret":"s","target":"https://evil.example/","allow_hosts":["ok.example"]}"#;
        let mut out_len: usize = 0;
        // SAFETY: `input` is a valid slice; `out_len` is a valid local.
        let out = unsafe { cic_ffi_run_flow(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(!out.is_null());
        // SAFETY: `out`/`out_len` are the pair just returned by the export.
        let got = unsafe { slice::from_raw_parts(out, out_len) }.to_vec();
        // SAFETY: releasing the exact pair returned above, exactly once.
        unsafe { cic_ffi_free(out, out_len) };

        let v: Value = serde_json::from_slice(&got).expect("valid JSON out");
        assert_eq!(v["ok"], false);
        assert!(v["error"]
            .as_str()
            .expect("error string")
            .contains("denied"));
    }

    #[test]
    fn run_flow_export_rejects_malformed_input() {
        let input = b"not json";
        let mut out_len: usize = 9;
        // SAFETY: `input` is valid; `out_len` is a valid local.
        let out = unsafe { cic_ffi_run_flow(input.as_ptr(), input.len(), &raw mut out_len) };
        assert!(out.is_null());
        assert_eq!(out_len, 0);
    }

    #[test]
    fn abi_version_matches_the_constant() {
        assert_eq!(cic_ffi_abi_version(), super::CIC_FFI_ABI_VERSION);
    }
}
