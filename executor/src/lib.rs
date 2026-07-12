//! The **executor** — the actuating ("intervening") half of a `module/host`
//! pair in feature-008's secret flow.
//!
//! The pair owns two objects: a Vault object (holds pubkey-sealed secrets) and
//! this executor object (performs the real intervention; first round: HTTP). The
//! executor sits at the **tail of the secret-flow pipeline**: the WASM module
//! opens a `SealedSecret` (from `connections`) into plaintext, and that plaintext
//! — carried as a [`secrecy::SecretString`], the same type `module::open` yields
//! — is applied here as the actuation credential, right before the outbound call.
//!
//! This crate owns *meaning* only: the [`ExecRequest`]/[`ExecOutcome`] shapes and
//! the [`Executor`] port, plus an in-memory [`MockExecutor`] and its contract
//! tests. It performs **no network I/O** — a real HTTP transport is a separate
//! adapter crate (`http-executor`), mirroring how `vault-adapter` sits behind
//! `connections`.
//!
//! Contract note: a **reached** endpoint always yields `Ok(`[`ExecOutcome`]`)`,
//! whatever its status — interpreting a `401`/`500` is the caller's job, not the
//! transport's. An [`ExecuteError`] means only that the call was *malformed*,
//! *denied by policy*, or *never completed*.

use std::collections::BTreeMap;
use std::fmt;

use secrecy::{ExposeSecret, SecretString};

/// The request method for an actuation. The first round models HTTP verbs; the
/// set is deliberately closed so policy can reason about it exhaustively.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Method {
    /// HTTP GET.
    Get,
    /// HTTP POST.
    Post,
    /// HTTP PUT.
    Put,
    /// HTTP DELETE.
    Delete,
}

impl fmt::Display for Method {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Self::Get => "GET",
            Self::Post => "POST",
            Self::Put => "PUT",
            Self::Delete => "DELETE",
        })
    }
}

/// An actuation request: *what* to do, independent of *how* it is transported.
///
/// It carries **no secret material** — the actuation credential is supplied
/// separately to [`Executor::execute`], so an `ExecRequest` is safe to clone and
/// log. Headers use a [`BTreeMap`] so the ordering is deterministic (a
/// prerequisite for any hash or `ProofTrace` over the request).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ExecRequest {
    /// The request method.
    pub method: Method,
    /// The target of the actuation (first round: an HTTPS URL). Opaque here; a
    /// transport adapter validates and dials it.
    pub target: String,
    /// Request headers, in deterministic key order.
    pub headers: BTreeMap<String, String>,
    /// The request body (may be empty).
    pub body: Vec<u8>,
    /// An optional correlation id, carried into audit records so an actuation can
    /// be linked to the `ProofTrace` step (or wider trace) that drove it.
    pub trace_id: Option<String>,
}

impl ExecRequest {
    /// Builds a request for `method` against `target`, with empty headers/body
    /// and no trace id.
    #[must_use]
    pub fn new(method: Method, target: impl Into<String>) -> Self {
        Self {
            method,
            target: target.into(),
            headers: BTreeMap::new(),
            body: Vec::new(),
            trace_id: None,
        }
    }

    /// Sets a header, returning `self` for chaining.
    #[must_use]
    pub fn with_header(mut self, name: impl Into<String>, value: impl Into<String>) -> Self {
        self.headers.insert(name.into(), value.into());
        self
    }

    /// Sets the correlation id carried into audit records, returning `self`.
    #[must_use]
    pub fn with_trace_id(mut self, trace_id: impl Into<String>) -> Self {
        self.trace_id = Some(trace_id.into());
        self
    }

    /// Sets the request body, returning `self` for chaining.
    #[must_use]
    pub fn with_body(mut self, body: impl Into<Vec<u8>>) -> Self {
        self.body = body.into();
        self
    }
}

/// The result of an actuation that reached the endpoint, whatever its status.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ExecOutcome {
    /// The endpoint's status code.
    pub status: u16,
    /// Response headers, in deterministic key order.
    pub headers: BTreeMap<String, String>,
    /// The response body.
    pub body: Vec<u8>,
}

impl ExecOutcome {
    /// Wraps a `status` with empty headers and body.
    #[must_use]
    pub fn new(status: u16) -> Self {
        Self {
            status,
            headers: BTreeMap::new(),
            body: Vec::new(),
        }
    }
}

/// An actuation failure. Carries no secret material, so it is always safe to log.
///
/// A *reached* endpoint is **not** a failure — it is an [`ExecOutcome`] with a
/// status. These variants cover only the ways an actuation never produces a
/// (complete) outcome: malformed, denied, over a limit, or never completed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ExecuteError {
    /// The request was malformed and rejected before any call (e.g. an empty or
    /// unparseable target).
    InvalidRequest(String),
    /// The executor refused to act on policy grounds (e.g. a non-https scheme or
    /// a host outside the egress allowlist). `reason` carries no secret.
    Denied(String),
    /// A configured limit was exceeded (e.g. the response-body cap). `reason`
    /// carries no secret.
    LimitExceeded(String),
    /// The endpoint could not be reached, or its response could not be read, for
    /// a non-secret reason.
    Unavailable(String),
}

impl fmt::Display for ExecuteError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidRequest(m) => write!(f, "invalid actuation request: {m}"),
            Self::Denied(m) => write!(f, "actuation denied: {m}"),
            Self::LimitExceeded(m) => write!(f, "actuation limit exceeded: {m}"),
            Self::Unavailable(m) => write!(f, "actuation transport unavailable: {m}"),
        }
    }
}

impl std::error::Error for ExecuteError {}

/// The executor port: perform an [`ExecRequest`], applying a secret credential.
///
/// Contract: the `secret` (the plaintext a module opened from a `SealedSecret`)
/// is applied as the actuation credential and **must never appear** in the
/// returned [`ExecOutcome`] nor in an [`ExecuteError`]. A reached endpoint yields
/// `Ok(ExecOutcome)` for any status. A real implementation is a network
/// transport (`http-executor`); the in-memory [`MockExecutor`] models one without
/// I/O.
pub trait Executor {
    /// Performs `request`, applying `secret` as the actuation credential.
    ///
    /// # Errors
    /// [`ExecuteError::InvalidRequest`] if the request is malformed;
    /// [`ExecuteError::Denied`] if policy refuses it;
    /// [`ExecuteError::Unavailable`] if the endpoint is not reached.
    fn execute(
        &self,
        request: &ExecRequest,
        secret: &SecretString,
    ) -> Result<ExecOutcome, ExecuteError>;
}

/// An in-memory [`Executor`] that models an endpoint guarding a credential: it
/// **echoes** the request back (status 200, same headers and body) when the
/// supplied secret matches the one it accepts, and returns a `401` outcome
/// otherwise.
///
/// This is the executor-side analogue of `connections`' `InMemoryTokenProvider`
/// (trust-by-value): it proves the opened secret flows all the way through to the
/// actuation and is checked there, while never placing the secret in its output.
/// Note that a credential mismatch is a `401` **outcome**, not an error — exactly
/// how a live HTTP endpoint would answer.
pub struct MockExecutor {
    accepted: SecretString,
}

impl MockExecutor {
    /// Creates an executor whose endpoint accepts exactly `secret`.
    #[must_use]
    pub fn accepting(secret: SecretString) -> Self {
        Self { accepted: secret }
    }
}

impl fmt::Debug for MockExecutor {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        // Never expose the accepted credential.
        f.debug_struct("MockExecutor")
            .field("accepted", &"<redacted>")
            .finish()
    }
}

impl Executor for MockExecutor {
    fn execute(
        &self,
        request: &ExecRequest,
        secret: &SecretString,
    ) -> Result<ExecOutcome, ExecuteError> {
        if request.target.is_empty() {
            return Err(ExecuteError::InvalidRequest("empty target".to_owned()));
        }
        // Non-constant-time compare — acceptable for an in-memory test double,
        // never for a real credential check (that is the endpoint's concern).
        if secret.expose_secret() != self.accepted.expose_secret() {
            return Ok(ExecOutcome {
                status: 401,
                headers: BTreeMap::new(),
                body: b"unauthorized".to_vec(),
            });
        }
        Ok(ExecOutcome {
            status: 200,
            headers: request.headers.clone(),
            body: request.body.clone(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::{ExecOutcome, ExecRequest, ExecuteError, Executor, Method, MockExecutor};
    use secrecy::SecretString;

    fn secret(value: &str) -> SecretString {
        SecretString::from(value.to_owned())
    }

    #[test]
    fn method_displays_as_http_verb() {
        assert_eq!(Method::Get.to_string(), "GET");
        assert_eq!(Method::Post.to_string(), "POST");
        assert_eq!(Method::Put.to_string(), "PUT");
        assert_eq!(Method::Delete.to_string(), "DELETE");
    }

    #[test]
    fn exec_request_builds_with_headers_and_body() {
        let req = ExecRequest::new(Method::Post, "https://api.example/v1/things")
            .with_header("content-type", "application/json")
            .with_body(b"{}".to_vec());

        assert_eq!(req.method, Method::Post);
        assert_eq!(req.target, "https://api.example/v1/things");
        assert_eq!(
            req.headers.get("content-type").map(String::as_str),
            Some("application/json")
        );
        assert_eq!(req.body, b"{}");
        assert_eq!(req.trace_id, None); // absent unless set

        let traced =
            ExecRequest::new(Method::Get, "https://api.example/t").with_trace_id("trace-42");
        assert_eq!(traced.trace_id.as_deref(), Some("trace-42"));
    }

    #[test]
    fn exec_outcome_new_is_empty() {
        let out = ExecOutcome::new(204);
        assert_eq!(out.status, 204);
        assert!(out.headers.is_empty());
        assert!(out.body.is_empty());
    }

    #[test]
    fn mock_echoes_request_when_credential_matches() {
        let exec = MockExecutor::accepting(secret("s3cr3t"));
        let req = ExecRequest::new(Method::Get, "https://api.example/ping")
            .with_header("x-req", "1")
            .with_body(b"hello".to_vec());

        let out = exec
            .execute(&req, &secret("s3cr3t"))
            .expect("matching credential actuates");

        assert_eq!(out.status, 200);
        assert_eq!(out.headers.get("x-req").map(String::as_str), Some("1"));
        assert_eq!(out.body, b"hello");
        // The secret never rides along in the outcome.
        assert!(!format!("{out:?}").contains("s3cr3t"));
    }

    #[test]
    fn mock_returns_401_outcome_when_credential_mismatches() {
        let exec = MockExecutor::accepting(secret("right"));
        let req = ExecRequest::new(Method::Get, "https://api.example/ping");

        // A credential mismatch is a reached-endpoint outcome, not an error.
        let out = exec.execute(&req, &secret("wrong")).expect("still reaches");
        assert_eq!(out.status, 401);
        assert!(!format!("{out:?}").contains("wrong"));
    }

    #[test]
    fn mock_rejects_empty_target_before_any_call() {
        let exec = MockExecutor::accepting(secret("x"));
        let req = ExecRequest::new(Method::Get, "");

        assert!(matches!(
            exec.execute(&req, &secret("x")),
            Err(ExecuteError::InvalidRequest(_))
        ));
    }

    #[test]
    fn mock_debug_redacts_the_accepted_credential() {
        let exec = MockExecutor::accepting(secret("t0psecret"));
        let dbg = format!("{exec:?}");
        assert!(!dbg.contains("t0psecret"));
        assert!(dbg.contains("<redacted>"));
    }

    #[test]
    fn errors_display_without_leaking() {
        assert!(ExecuteError::InvalidRequest("empty target".to_owned())
            .to_string()
            .contains("empty target"));
        assert!(ExecuteError::Denied("host not allowed: x".to_owned())
            .to_string()
            .contains("denied"));
        assert!(ExecuteError::LimitExceeded("body too large".to_owned())
            .to_string()
            .contains("limit exceeded"));
        assert!(ExecuteError::Unavailable("dns".to_owned())
            .to_string()
            .contains("dns"));

        let err: &dyn std::error::Error = &ExecuteError::Unavailable("x".to_owned());
        assert!(err.source().is_none());
    }
}
