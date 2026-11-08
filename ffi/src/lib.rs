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
const CIC_FFI_ABI_VERSION: u32 = 3;

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
    use std::sync::{Arc, OnceLock};

    use base64::Engine;
    use connections::vault::port::{Countersigner, InMemoryCountersigner};
    use connections::vault::{AuthScope, Cn, Connection, SecretRef};
    use executor::{ExecOutcome, ExecRequest, Method};
    use http_executor::{
        AuditDecision, ChainedEntry, EgressPolicy, HashChainedAuditSink, HttpExecutor, Limits,
    };
    use module::{seal, ModuleIdentity};
    use secrecy::SecretString;
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
        /// Optional caller correlation id, echoed into the audit record so the
        /// Go relay can bind the actuation to its own `ProofTrace`.
        trace_id: Option<String>,
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
            trace_id: doc
                .get("trace_id")
                .and_then(Value::as_str)
                .filter(|s| !s.is_empty())
                .map(str::to_owned),
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
    /// One actuation's outcome plus its tamper-evident audit entry (the
    /// `record_hash`-chained [`ChainedEntry`] the Go relay folds into its
    /// `ProofTrace`).
    struct Actuated {
        outcome: ExecOutcome,
        audit: ChainedEntry,
    }

    fn execute_flow(input: &FlowInput) -> Result<(Actuated, Vec<String>), FlowError> {
        let subject = Cn::new(input.subject.clone());

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
        let connection = Connection::request(subject, input.scope.clone())
            .countersign(cert)
            .initialize(sealed);
        let credential = identity.open(connection.token()).map_err(err_string)?;

        // 5. Actuate over HTTP, applying the opened credential.
        let actuated = actuate(input, &credential)?;
        Ok((actuated, authorized))
    }

    /// Actuates `input` over HTTP with the opened `credential`. Shared by the mock
    /// and live profiles — they differ only in *how* the credential is obtained.
    ///
    /// A [`HashChainedAuditSink`] is attached so the actuation emits a
    /// tamper-evident, non-secret audit entry; the head [`ChainedEntry`] is
    /// returned alongside the outcome for the Go relay to fold into its
    /// `ProofTrace`.
    fn actuate(input: &FlowInput, credential: &SecretString) -> Result<Actuated, FlowError> {
        let policy = if input.insecure {
            EgressPolicy::insecure_allowing(input.allow_hosts.clone())
        } else {
            EgressPolicy::allowing(input.allow_hosts.clone())
        };
        let sink = Arc::new(HashChainedAuditSink::new());
        let executor = HttpExecutor::with_limits(policy, Limits::default())
            .map_err(err_string)?
            .with_audit_sink(sink.clone());
        let mut req =
            ExecRequest::new(input.method, input.target.clone()).with_body(input.body.clone());
        if let Some(trace_id) = &input.trace_id {
            req = req.with_trace_id(trace_id.clone());
        }
        for (name, value) in &input.headers {
            req = req.with_header(name.clone(), value.clone());
        }
        let result = runtime().block_on(executor.execute(&req, credential));
        // The executor records an audit entry on BOTH the allow and deny paths, so
        // the entry is present whether the actuation succeeded or was refused
        // (policy deny, SSRF block, limit) — a refused actuation is auditable.
        let audit = sink.entries().into_iter().next();
        match result {
            Ok(outcome) => {
                let audit = audit.ok_or_else(|| FlowError::msg("audit sink recorded no entry"))?;
                Ok(Actuated { outcome, audit })
            }
            Err(e) => Err(FlowError {
                message: err_string(e),
                audit: audit.map(Box::new),
            }),
        }
    }

    /// Renders any flow error as its (non-secret) Display string.
    fn err_string(e: impl std::fmt::Display) -> String {
        e.to_string()
    }

    /// A flow failure: a non-secret message plus, when the failure happened
    /// *during the actuation* (a policy deny, SSRF block, limit, transport
    /// error), the tamper-evident audit entry the executor still recorded — so a
    /// refused actuation is auditable, not silent. Failures *before* the actuation
    /// (countersign/seal/open) carry no audit.
    struct FlowError {
        message: String,
        // Boxed: a `ChainedEntry` is large, and keeping it inline would bloat the
        // `Err` variant of every flow `Result` (clippy `result_large_err`).
        audit: Option<Box<ChainedEntry>>,
    }

    impl FlowError {
        /// A failure with no associated audit entry (pre-actuation, or a config
        /// error). The message is non-secret.
        fn msg(e: impl std::fmt::Display) -> Self {
            Self {
                message: e.to_string(),
                audit: None,
            }
        }
    }

    /// So the many `.map_err(err_string)?` sites (and other `String` errors)
    /// propagate into a `FlowError` unchanged, without an audit entry.
    impl From<String> for FlowError {
        fn from(message: String) -> Self {
            Self {
                message,
                audit: None,
            }
        }
    }

    /// Dispatches to the requested execution profile. `mock` (default) runs the
    /// in-process mock trust backends; `live` (only when built with the
    /// `live-vault` feature) drives the real Vault chain.
    fn run_profile(
        profile: &str,
        input: &FlowInput,
        doc: &Value,
    ) -> Result<(Actuated, Vec<String>), FlowError> {
        match profile {
            "mock" => execute_flow(input),
            "live" => run_live(input, doc),
            other => Err(FlowError::msg(format!("unknown profile: {other}"))),
        }
    }

    /// Live profile — real Vault chain (see the `live` module).
    #[cfg(feature = "live-vault")]
    fn run_live(input: &FlowInput, doc: &Value) -> Result<(Actuated, Vec<String>), FlowError> {
        live::execute_flow(input, doc)
    }

    /// Live profile is not compiled into this staticlib.
    #[cfg(not(feature = "live-vault"))]
    fn run_live(_input: &FlowInput, _doc: &Value) -> Result<(Actuated, Vec<String>), FlowError> {
        Err(FlowError::msg(
            "live profile not compiled in (build cic-ffi with --features live-vault)",
        ))
    }

    /// Projects a [`ChainedEntry`] into the non-secret JSON the Go relay parses:
    /// the audit facts, the `seq`, and the tamper-evident `record_hash`. Never the
    /// credential, `Authorization`, a token, or any body contents.
    fn audit_json(entry: &ChainedEntry) -> Value {
        let record = &entry.record;
        let decision = match &record.decision {
            AuditDecision::Allowed => "allow".to_owned(),
            AuditDecision::Denied(reason) => format!("deny:{reason}"),
        };
        json!({
            "trace_id": record.trace_id,
            "method": record.method.to_string(),
            "host": record.host,
            "decision": decision,
            "status": record.status,
            // Durations in ms fit u64 for any realistic actuation; saturate rather
            // than cast (u128 has no serde_json number repr without extra features).
            "duration_ms": u64::try_from(record.duration_ms).unwrap_or(u64::MAX),
            "request_body_sha256": record.request_body_sha256,
            "response_body_sha256": record.response_body_sha256,
            "seq": entry.seq,
            "record_hash": entry.record_hash,
        })
    }

    /// The airlock core for `cic_ffi_run_flow`. Returns `None` only on malformed
    /// input; a flow that runs but fails (policy/HTTP) yields `{"ok":false,...}`.
    pub fn run_flow(input: &[u8]) -> Option<Vec<u8>> {
        let doc: Value = serde_json::from_slice(input).ok()?;
        let parsed = parse(&doc)?;
        let profile = doc
            .get("profile")
            .and_then(Value::as_str)
            .unwrap_or("mock")
            .to_owned();
        let result = match run_profile(&profile, &parsed, &doc) {
            Ok((actuated, scope)) => json!({
                "ok": true,
                "status": actuated.outcome.status,
                "body_base64": b64().encode(&actuated.outcome.body),
                "scope": scope,
                "audit": audit_json(&actuated.audit),
            }),
            // A refused actuation still carries its (deny) audit entry; a failure
            // before the actuation carries none.
            Err(FlowError { message, audit }) => match audit {
                Some(entry) => {
                    json!({ "ok": false, "error": message, "audit": audit_json(&entry) })
                }
                None => json!({ "ok": false, "error": message }),
            },
        };
        serde_json::to_vec(&result).ok()
    }

    /// The live profile: the real Vault chain (countersign → mTLS cert-auth login
    /// → `read_sealed` → open) through `vault-adapter`/`module`, then the shared HTTP
    /// actuation. Compiled only with the `live-vault` feature (which drags in a
    /// TLS stack, so the staticlib is no longer pure-Rust). The secret is sealed to
    /// the module key and seeded into KV here — the request-time A-version wrap
    /// (M41.2) is a deferred edge.
    #[cfg(feature = "live-vault")]
    mod live {
        use super::{actuate, err_string, runtime, Actuated, FlowError, FlowInput};
        use base64::Engine;
        use connections::vault::{Cn, SecretRef};
        use module::{seal, ModuleIdentity};
        use p256::ecdsa::SigningKey;
        use p256::elliptic_curve::Generate;
        use p256::pkcs8::{EncodePrivateKey, LineEnding};
        use serde_json::{json, Value};
        use vault_adapter::{AllowlistPolicy, VaultPkiCountersigner, VaultSecretSource};
        use vault_client_rs::VaultClient;

        /// Vault connection + mount coordinates, supplied by the caller in the
        /// request's `vault` object.
        struct VaultConfig {
            addr: String,
            token: String,
            ca_pem: Vec<u8>,
            pki_mount: String,
            pki_role: String,
            cert_mount: String,
            cert_role: String,
            kv_mount: String,
            kv_path: String,
        }

        /// Parses the `vault` object; `None` (→ a clean error) if any field is missing
        /// or `cacert_pem_base64` is undecodable.
        fn parse_vault(doc: &Value) -> Option<VaultConfig> {
            let v = doc.get("vault")?.as_object()?;
            let s = |k: &str| v.get(k).and_then(Value::as_str).map(str::to_owned);
            Some(VaultConfig {
                addr: s("addr")?,
                token: s("token")?,
                ca_pem: super::b64().decode(s("cacert_pem_base64")?).ok()?,
                pki_mount: s("pki_mount")?,
                pki_role: s("pki_role")?,
                cert_mount: s("cert_mount")?,
                cert_role: s("cert_role")?,
                kv_mount: s("kv_mount")?,
                kv_path: s("kv_path")?,
            })
        }

        /// Wraps DER in a PEM CERTIFICATE block (for the mTLS client identity).
        fn der_to_cert_pem(der: &[u8]) -> String {
            let encoded = super::b64().encode(der);
            let mut out = String::from("-----BEGIN CERTIFICATE-----\n");
            for line in encoded.as_bytes().chunks(64) {
                out.push_str(std::str::from_utf8(line).expect("base64 is ascii"));
                out.push('\n');
            }
            out.push_str("-----END CERTIFICATE-----\n");
            out
        }

        /// An admin (token-auth) client verifying the server CA.
        fn admin(cfg: &VaultConfig) -> Result<VaultClient, String> {
            VaultClient::builder()
                .address(&cfg.addr)
                .token_str(&cfg.token)
                .ca_cert_pem(cfg.ca_pem.clone())
                .build()
                .map_err(err_string)
        }

        /// Runs the live Vault chain and actuates. The module holds a per-request
        /// raw key (its CSR signer, mTLS identity, and sealing recipient).
        pub fn execute_flow(
            input: &FlowInput,
            doc: &Value,
        ) -> Result<(Actuated, Vec<String>), FlowError> {
            let cfg = parse_vault(doc)
                .ok_or_else(|| "live profile requires a `vault` config".to_owned())?;

            // Module key: CSR signer + mTLS client identity + sealing recipient.
            let signing = SigningKey::generate();
            let key_pem = signing.to_pkcs8_pem(LineEnding::LF).map_err(err_string)?;
            let identity = ModuleIdentity::from_pkcs8_pem(&key_pem).map_err(err_string)?;
            let subject = Cn::new(input.subject.clone());

            // 1. Live countersign: module CSR → Vault PKI sign-verbatim.
            let request = identity
                .certificate_request(subject, input.scope.clone())
                .map_err(err_string)?;
            let countersigner = VaultPkiCountersigner::new(
                AllowlistPolicy::new(input.scope.clone()),
                admin(&cfg)?,
                cfg.pki_mount.clone(),
                cfg.pki_role.clone(),
            );
            let cert = runtime()
                .block_on(countersigner.countersign(&request))
                .map_err(err_string)?;
            let authorized: Vec<String> =
                cert.scope().cns().map(|c| c.as_str().to_owned()).collect();
            let cert_pem = der_to_cert_pem(cert.der());

            // 2. Seed KV: seal the secret to the module key + admin write (stands in
            //    for the deferred request-time Vault-side wrap).
            let secret_ref = SecretRef::new(&cfg.kv_mount, &cfg.kv_path);
            let sealed = seal(
                &identity.public_key_der().map_err(err_string)?,
                secret_ref.clone(),
                input.secret.as_bytes(),
            )
            .map_err(err_string)?;
            let sealed_b64 = super::b64().encode(sealed.ciphertext());
            runtime()
                .block_on(
                    admin(&cfg)?
                        .kv2(&cfg.kv_mount)
                        .write(&cfg.kv_path, &json!({ "sealed": sealed_b64 })),
                )
                .map_err(err_string)?;

            // 3. mTLS cert-auth login → scoped token → read_sealed → open.
            let source = VaultSecretSource::connect_mtls(
                &cfg.addr,
                &cfg.ca_pem,
                cert_pem.as_bytes(),
                key_pem.as_bytes(),
            )
            .map_err(err_string)?;
            runtime()
                .block_on(source.login(&cfg.cert_mount, Some(&cfg.cert_role)))
                .map_err(err_string)?;
            let read_back = runtime()
                .block_on(source.read_sealed(&secret_ref))
                .map_err(err_string)?;
            let credential = identity.open(&read_back).map_err(err_string)?;

            // 4. Actuate over HTTP with the opened credential (shared path).
            let actuated = actuate(input, &credential)?;
            Ok((actuated, authorized))
        }
    }

    #[cfg(test)]
    mod tests {
        use super::{
            audit_json, parse_allow_hosts, parse_headers, parse_method, parse_scope, run_flow,
        };
        use base64::Engine;
        use executor::Method;
        use http_executor::{AuditDecision, AuditRecord, ChainedEntry};
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
        fn unknown_profile_is_a_structured_error() {
            let input = serde_json::json!({
                "subject": "m", "secret": "x", "target": "http://127.0.0.1:1/x",
                "profile": "bogus",
            });
            let out = run_flow(input.to_string().as_bytes()).expect("result");
            let v: Value = serde_json::from_slice(&out).expect("json");
            assert_eq!(v["ok"], false);
            assert!(v["error"]
                .as_str()
                .expect("error")
                .contains("unknown profile"));
        }

        #[cfg(not(feature = "live-vault"))]
        #[test]
        fn live_profile_without_the_feature_is_a_structured_error() {
            let input = serde_json::json!({
                "subject": "m", "secret": "x", "target": "http://127.0.0.1:1/x",
                "profile": "live",
            });
            let out = run_flow(input.to_string().as_bytes()).expect("result");
            let v: Value = serde_json::from_slice(&out).expect("json");
            assert_eq!(v["ok"], false);
            assert!(v["error"]
                .as_str()
                .expect("error")
                .contains("live profile not compiled in"));
        }

        #[cfg(feature = "live-vault")]
        #[test]
        fn live_profile_without_a_vault_config_is_a_structured_error() {
            let input = serde_json::json!({
                "subject": "m", "secret": "x", "target": "http://127.0.0.1:1/x",
                "profile": "live",
            });
            let out = run_flow(input.to_string().as_bytes()).expect("result");
            let v: Value = serde_json::from_slice(&out).expect("json");
            assert_eq!(v["ok"], false);
            assert!(v["error"].as_str().expect("error").contains("vault"));
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
                "trace_id": "trace-abc-123",
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

            // The actuation carries its non-secret audit record across the seam:
            // the caller's trace_id round-trips and the tamper-evident record_hash
            // binds it, for the Go relay to fold into its ProofTrace.
            let audit = &v["audit"];
            assert_eq!(audit["trace_id"], "trace-abc-123");
            assert_eq!(audit["method"], "GET");
            assert_eq!(audit["host"], "127.0.0.1");
            assert_eq!(audit["decision"], "allow");
            assert_eq!(audit["status"], 200);
            assert_eq!(audit["seq"], 0);
            assert!(
                audit["record_hash"].as_str().is_some_and(|h| h.len() == 64),
                "record_hash must be a 64-hex-char SHA-256 digest"
            );
            // The audit carries only digests, never the credential/body contents.
            assert!(audit["response_body_sha256"].is_string());
        }

        #[test]
        fn audit_json_projects_facts_and_renders_a_denied_decision() {
            // A denied actuation's record still projects to non-secret JSON (the
            // deny-path emission is a follow-up; the projection is exercised here).
            let entry = ChainedEntry {
                seq: 3,
                prev_hash: "prev".to_owned(),
                record: AuditRecord {
                    trace_id: Some("t-9".to_owned()),
                    method: Method::Post,
                    host: "example.test".to_owned(),
                    decision: AuditDecision::Denied("host not allowed".to_owned()),
                    status: None,
                    duration_ms: 12,
                    request_body_sha256: "req".to_owned(),
                    response_body_sha256: None,
                },
                record_hash: "hash".to_owned(),
                signature: None,
            };
            let v = audit_json(&entry);
            assert_eq!(v["trace_id"], "t-9");
            assert_eq!(v["method"], "POST");
            assert_eq!(v["host"], "example.test");
            assert_eq!(v["decision"], "deny:host not allowed");
            assert!(v["status"].is_null());
            assert_eq!(v["duration_ms"], 12);
            assert!(v["response_body_sha256"].is_null());
            assert_eq!(v["seq"], 3);
            assert_eq!(v["record_hash"], "hash");
        }

        #[test]
        fn denied_actuation_is_audited_not_silent() {
            // A well-formed flow whose target host is not allowlisted: the policy
            // refuses it before any dial. The refusal must NOT be silent — it
            // carries a tamper-evident deny audit, the most audit-worthy case.
            let input = serde_json::json!({
                "subject": "relay.worker",
                "scope": ["db.read"],
                "secret": "hvs.TOPSECRET",
                "target": "https://not-allowed.example/x",
                "allow_hosts": ["127.0.0.1"],
                "trace_id": "deny-trace-1",
            })
            .to_string();

            let out = run_flow(input.as_bytes()).expect("produces a result");
            let v: Value = serde_json::from_slice(&out).expect("json");
            assert_eq!(v["ok"], false);
            // The error message is the executor's non-secret Display, not empty.
            assert!(v["error"]
                .as_str()
                .is_some_and(|e| e.contains("actuation denied")));

            let audit = &v["audit"];
            assert!(
                audit.is_object(),
                "a refused actuation must carry a deny audit"
            );
            assert_eq!(audit["trace_id"], "deny-trace-1");
            assert_eq!(audit["host"], "not-allowed.example");
            assert!(
                audit["decision"]
                    .as_str()
                    .is_some_and(|d| d.starts_with("deny:")),
                "decision must be a deny, got {:?}",
                audit["decision"]
            );
            assert!(audit["record_hash"].as_str().is_some_and(|h| h.len() == 64));
            // The secret never crosses back, even on the deny path.
            assert!(!String::from_utf8_lossy(&out).contains("hvs.TOPSECRET"));
        }

        #[test]
        fn failure_without_actuation_has_no_audit() {
            // A failure that never reached the actuation (here: an unknown profile)
            // carries no audit — there was nothing to audit.
            let input = serde_json::json!({
                "subject": "m", "scope": ["db.read"], "secret": "x",
                "target": "https://h/x", "profile": "bogus",
            })
            .to_string();
            let out = run_flow(input.as_bytes()).expect("produces a result");
            let v: Value = serde_json::from_slice(&out).expect("json");
            assert_eq!(v["ok"], false);
            assert!(v["error"]
                .as_str()
                .is_some_and(|e| e.contains("unknown profile")));
            assert!(
                v["audit"].is_null(),
                "a failure before any actuation must carry no audit"
            );
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
