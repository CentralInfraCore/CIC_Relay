//! Ports (traits) the core needs from the outside world for the secret flow,
//! plus in-memory reference implementations and their contracts.
//!
//! Port-adapter (feature-008): the core declares *what* it needs; a driver
//! (e.g. `vault-client-rs` via a future adapter) implements *how*. Nothing here
//! performs cryptography or network I/O — the async Vault boundary is the
//! adapter's concern, so this layer stays synchronous, deterministic and
//! dependency-free. The three ports mirror the three runtime bridges of the
//! flow:
//!
//! - [`Countersigner`] — the Relay's content-dependent authorization + notary.
//! - [`TokenProvider`] — Vault cert-auth exchanging a Cert for a sealed token.
//! - [`SecretBackend`] — the at-rest store of pubkey-sealed envelopes.
//!
//! [`establish`] composes the first two into the typestate [`Connection`] flow.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;

use super::{AuthScope, Cn, Connection, CounterSignedCert, Initialized, SealedSecret, SecretRef};

/// Error returned by a [`SecretBackend`]. Carries no secret material, so it is
/// always safe to log.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum BackendError {
    /// No sealed secret exists at the referenced location.
    NotFound(SecretRef),
    /// The backend is unavailable; `message` must not contain any secret value.
    Unavailable(String),
}

impl fmt::Display for BackendError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::NotFound(r) => write!(f, "no sealed secret at {r}"),
            Self::Unavailable(message) => write!(f, "secret backend unavailable: {message}"),
        }
    }
}

impl std::error::Error for BackendError {}

/// Error returned by a [`Countersigner`] — the Relay's policy/notary role.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CountersignError {
    /// The requested scope is not permitted by policy. `reason` may name the
    /// offending CN (public, content-encoding) but never a secret value.
    Denied(String),
}

impl fmt::Display for CountersignError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Denied(reason) => write!(f, "countersignature denied: {reason}"),
        }
    }
}

impl std::error::Error for CountersignError {}

/// Error returned by a [`TokenProvider`] — Vault cert-auth.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum TokenError {
    /// The presented certificate's chain/countersignature is not trusted.
    Untrusted,
    /// The provider failed for a non-secret reason.
    Unavailable(String),
}

impl fmt::Display for TokenError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Untrusted => f.write_str("certificate not trusted"),
            Self::Unavailable(message) => write!(f, "token provider unavailable: {message}"),
        }
    }
}

impl std::error::Error for TokenError {}

/// A request to the Relay to countersign a module-generated Cert.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CertRequest {
    /// The subject the connection is requested for.
    pub subject: Cn,
    /// The authorization scope encoded in the Cert's CN set.
    pub scope: AuthScope,
    /// The DER-encoded certificate-signing request (public material).
    pub csr_der: Vec<u8>,
}

impl CertRequest {
    /// Builds a request for `subject` asking authorization `scope`, carrying the
    /// DER-encoded signing request `csr_der`.
    #[must_use]
    pub fn new(subject: Cn, scope: AuthScope, csr_der: Vec<u8>) -> Self {
        Self {
            subject,
            scope,
            csr_der,
        }
    }
}

/// The Relay port: content-dependent authorization plus countersignature.
///
/// Contract: countersigning succeeds only for a policy-permitted scope; a denial
/// is a typed [`CountersignError::Denied`] whose reason carries no secret.
pub trait Countersigner {
    /// Countersigns `request` if policy permits its scope.
    ///
    /// # Errors
    /// [`CountersignError::Denied`] if the requested scope is not permitted.
    fn countersign(&self, request: &CertRequest) -> Result<CounterSignedCert, CountersignError>;
}

/// The Vault cert-auth port: exchange a countersigned Cert for a sealed token.
///
/// Contract: a trusted certificate yields a [`SealedSecret`] token (never
/// plaintext); an untrusted one yields [`TokenError::Untrusted`].
pub trait TokenProvider {
    /// Issues a sealed token for `cert` if its chain is trusted.
    ///
    /// # Errors
    /// [`TokenError::Untrusted`] if the certificate is not trusted;
    /// [`TokenError::Unavailable`] on backend failure.
    fn issue_token(&self, cert: &CounterSignedCert) -> Result<SealedSecret, TokenError>;
}

/// A read port over the at-rest store of pubkey-sealed envelopes.
///
/// Contract: a missing reference yields [`BackendError::NotFound`] (never a
/// panic); returned values are always [`SealedSecret`] (ciphertext).
pub trait SecretBackend {
    /// Reads the sealed envelope at `secret_ref`.
    ///
    /// # Errors
    /// [`BackendError::NotFound`] if absent; [`BackendError::Unavailable`] on
    /// backend failure.
    fn read_sealed(&self, secret_ref: &SecretRef) -> Result<SealedSecret, BackendError>;
}

/// In-memory [`Countersigner`] that permits a request iff every requested CN is
/// in its `allowed` scope. A minimal model of content-dependent authorization.
#[derive(Debug, Clone, Default)]
pub struct InMemoryCountersigner {
    allowed: AuthScope,
}

impl InMemoryCountersigner {
    /// Creates a countersigner that permits exactly the CNs in `allowed`.
    #[must_use]
    pub fn new(allowed: AuthScope) -> Self {
        Self { allowed }
    }
}

impl Countersigner for InMemoryCountersigner {
    fn countersign(&self, request: &CertRequest) -> Result<CounterSignedCert, CountersignError> {
        if let Some(cn) = request.scope.cns().find(|cn| !self.allowed.contains(cn)) {
            return Err(CountersignError::Denied(format!("cn not permitted: {cn}")));
        }
        Ok(CounterSignedCert::new(
            request.csr_der.clone(),
            request.scope.clone(),
        ))
    }
}

/// In-memory [`TokenProvider`] that trusts certificates by their DER bytes and
/// issues a fixed sealed `token`. A minimal model of Vault cert-auth.
#[derive(Debug, Clone)]
pub struct InMemoryTokenProvider {
    trusted: BTreeSet<Vec<u8>>,
    token: SealedSecret,
}

impl InMemoryTokenProvider {
    /// Creates a provider that issues `token` to trusted certificates. No
    /// certificate is trusted until [`trust`](Self::trust) is called.
    #[must_use]
    pub fn new(token: SealedSecret) -> Self {
        Self {
            trusted: BTreeSet::new(),
            token,
        }
    }

    /// Marks a certificate (by its DER bytes) as trusted.
    pub fn trust(&mut self, cert_der: impl Into<Vec<u8>>) {
        self.trusted.insert(cert_der.into());
    }
}

impl TokenProvider for InMemoryTokenProvider {
    fn issue_token(&self, cert: &CounterSignedCert) -> Result<SealedSecret, TokenError> {
        if self.trusted.contains(cert.der()) {
            Ok(self.token.clone())
        } else {
            Err(TokenError::Untrusted)
        }
    }
}

/// In-memory [`SecretBackend`]: a map from [`SecretRef`] to its sealed envelope.
#[derive(Debug, Clone, Default)]
pub struct InMemorySecretBackend {
    sealed: BTreeMap<SecretRef, SealedSecret>,
}

impl InMemorySecretBackend {
    /// Creates an empty backend.
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Stores `sealed` keyed by its own [`SecretRef`], returning any previous
    /// value at that reference.
    pub fn insert(&mut self, sealed: SealedSecret) -> Option<SealedSecret> {
        self.sealed.insert(sealed.secret_ref().clone(), sealed)
    }
}

impl SecretBackend for InMemorySecretBackend {
    fn read_sealed(&self, secret_ref: &SecretRef) -> Result<SealedSecret, BackendError> {
        self.sealed
            .get(secret_ref)
            .cloned()
            .ok_or_else(|| BackendError::NotFound(secret_ref.clone()))
    }
}

/// Error from [`establish`], distinguishing which stage of the flow failed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum EstablishError {
    /// The Relay declined to countersign the request.
    Countersign(CountersignError),
    /// Vault declined to issue a token for the countersigned Cert.
    Token(TokenError),
}

impl fmt::Display for EstablishError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Countersign(e) => write!(f, "countersign stage failed: {e}"),
            Self::Token(e) => write!(f, "token stage failed: {e}"),
        }
    }
}

impl std::error::Error for EstablishError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Countersign(e) => Some(e),
            Self::Token(e) => Some(e),
        }
    }
}

/// Drives the flow across the two outward ports: countersign the request with the
/// Relay, exchange the resulting Cert for a sealed token at Vault, and return an
/// initialized [`Connection`]. Models steps 3–7 of feature-008.
///
/// # Errors
/// [`EstablishError::Countersign`] if the Relay denies the scope;
/// [`EstablishError::Token`] if Vault rejects the countersigned Cert.
pub fn establish(
    relay: &impl Countersigner,
    vault: &impl TokenProvider,
    request: &CertRequest,
) -> Result<Connection<Initialized>, EstablishError> {
    let cert = relay
        .countersign(request)
        .map_err(EstablishError::Countersign)?;
    let token = vault.issue_token(&cert).map_err(EstablishError::Token)?;
    Ok(
        Connection::request(request.subject.clone(), request.scope.clone())
            .countersign(cert)
            .initialize(token),
    )
}

#[cfg(test)]
mod tests {
    use super::{
        establish, BackendError, CertRequest, CountersignError, Countersigner, EstablishError,
        InMemoryCountersigner, InMemorySecretBackend, InMemoryTokenProvider, SecretBackend,
        TokenError, TokenProvider,
    };
    use crate::vault::{AuthScope, Cn, CounterSignedCert, SealedSecret, SecretRef};

    fn scope(cns: &[&str]) -> AuthScope {
        AuthScope::from_cns(cns.iter().map(|c| Cn::new(*c)))
    }

    fn sealed(path: &str) -> SealedSecret {
        SealedSecret::new(SecretRef::new("secret", path), b"SECRETVALUE".to_vec())
    }

    #[test]
    fn secret_backend_contract_resolves_present_and_reports_missing() {
        let mut backend = InMemorySecretBackend::new();
        let present = SecretRef::new("secret", "db");
        assert!(backend.insert(sealed("db")).is_none());

        let got = backend
            .read_sealed(&present)
            .expect("present ref must resolve");
        assert_eq!(got.secret_ref(), &present);
        assert!(!format!("{got:?}").contains("SECRETVALUE")); // stays sealed

        let absent = SecretRef::new("secret", "missing");
        assert_eq!(
            backend.read_sealed(&absent),
            Err(BackendError::NotFound(absent))
        );
    }

    #[test]
    fn countersigner_permits_allowed_scope_and_denies_the_rest() {
        let relay = InMemoryCountersigner::new(scope(&["db.read", "db.write"]));

        let ok = CertRequest::new(Cn::new("worker"), scope(&["db.read"]), vec![1, 2, 3]);
        let cert = relay
            .countersign(&ok)
            .expect("permitted scope countersigns");
        assert!(cert.scope().contains(&Cn::new("db.read")));
        assert_eq!(cert.der(), &[1, 2, 3]);

        let bad = CertRequest::new(Cn::new("worker"), scope(&["db.read", "root"]), vec![]);
        assert_eq!(
            relay.countersign(&bad).unwrap_err(),
            CountersignError::Denied("cn not permitted: root".into())
        );
    }

    #[test]
    fn token_provider_trusts_only_known_certs() {
        let token = sealed("token");
        let mut vault = InMemoryTokenProvider::new(token.clone());
        let cert = CounterSignedCert::new(vec![0xaa], scope(&["db.read"]));

        assert_eq!(vault.issue_token(&cert), Err(TokenError::Untrusted));

        vault.trust(vec![0xaa]);
        let issued = vault.issue_token(&cert).expect("trusted cert issues token");
        assert_eq!(issued, token);
        assert!(!format!("{issued:?}").contains("SECRETVALUE")); // token stays sealed
    }

    #[test]
    fn establish_drives_the_full_flow() {
        let relay = InMemoryCountersigner::new(scope(&["db.read"]));
        let mut vault = InMemoryTokenProvider::new(sealed("token"));
        vault.trust(vec![7]); // trust the cert the relay will mint (csr_der = [7])

        let req = CertRequest::new(Cn::new("worker"), scope(&["db.read"]), vec![7]);
        let conn = establish(&relay, &vault, &req).expect("flow establishes");

        assert_eq!(conn.subject(), &Cn::new("worker"));
        assert_eq!(
            conn.token().secret_ref(),
            &SecretRef::new("secret", "token")
        );
        assert!(conn.scope().contains(&Cn::new("db.read")));
    }

    #[test]
    fn establish_propagates_countersign_denial() {
        let relay = InMemoryCountersigner::new(scope(&["db.read"]));
        let vault = InMemoryTokenProvider::new(sealed("token"));
        let req = CertRequest::new(Cn::new("worker"), scope(&["root"]), vec![7]);

        let err = establish(&relay, &vault, &req).unwrap_err();
        assert!(matches!(
            err,
            EstablishError::Countersign(CountersignError::Denied(_))
        ));
    }

    #[test]
    fn establish_propagates_untrusted_token() {
        let relay = InMemoryCountersigner::new(scope(&["db.read"]));
        let vault = InMemoryTokenProvider::new(sealed("token")); // trusts nothing
        let req = CertRequest::new(Cn::new("worker"), scope(&["db.read"]), vec![7]);

        assert_eq!(
            establish(&relay, &vault, &req).unwrap_err(),
            EstablishError::Token(TokenError::Untrusted)
        );
    }

    #[test]
    fn errors_display_and_chain_without_leaking_secrets() {
        let nf = BackendError::NotFound(SecretRef::new("secret", "x"));
        assert!(nf.to_string().contains("secret/x"));
        assert!(BackendError::Unavailable("down".into())
            .to_string()
            .contains("down"));

        assert!(CountersignError::Denied("nope".into())
            .to_string()
            .contains("nope"));

        assert!(!TokenError::Untrusted.to_string().is_empty());
        assert!(TokenError::Unavailable("busy".into())
            .to_string()
            .contains("busy"));

        let ec = EstablishError::Countersign(CountersignError::Denied("d".into()));
        assert!(ec.to_string().contains("countersign"));
        assert!(std::error::Error::source(&ec).is_some());

        let et = EstablishError::Token(TokenError::Untrusted);
        assert!(et.to_string().contains("token"));
        assert!(std::error::Error::source(&et).is_some());
    }
}
