//! Live Vault-PKI-backed countersigner — the runtime counterpart of the
//! `connections` [`Countersigner`](connections::vault::port::Countersigner) port.
//!
//! This is the Relay's role at runtime, split into its two orthogonal parts:
//!
//! - **The decision** ([`CountersignPolicy`]) — the Relay's *content-dependent
//!   authorization*: given a [`CertRequest`], decide which CNs to countersign, or
//!   refuse. This is where the Relay is "smart"; the signer knows nothing about it.
//! - **The mechanism** ([`PkiBackend`]) — a dumb CA-signing seam over Vault's PKI
//!   engine (`pki/sign-verbatim`). The intermediate CA key lives in Vault and never
//!   enters the Relay's process.
//!
//! [`VaultPkiCountersigner`] wires the two: it authorizes via the policy, then —
//! only for what the policy permitted — has Vault countersign the module's CSR,
//! yielding a [`CounterSignedCert`] chained to the CA root. It never holds the
//! signing key and never sees a secret.

use std::fmt;
use std::future::Future;

use base64::Engine;
use connections::vault::port::CertRequest;
use connections::vault::{AuthScope, CounterSignedCert};
use vault_client_rs::{PkiOperations, PkiSignedCert, VaultClient, VaultError};

/// PEM label for a PKCS#10 certificate-signing request.
const CSR_LABEL: &str = "CERTIFICATE REQUEST";

/// A refusal to countersign. The `reason` may name the offending CN (public,
/// content-encoding material) but never a secret value.
#[derive(Debug, Clone)]
pub struct Denied(String);

impl Denied {
    /// Creates a denial with `reason`.
    #[must_use]
    pub fn new(reason: impl Into<String>) -> Self {
        Self(reason.into())
    }

    /// The public denial reason.
    #[must_use]
    pub fn reason(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for Denied {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

/// The Relay's content-dependent authorization — the policy decision point.
///
/// Given a [`CertRequest`] (subject + requested scope + CSR), decide **which CNs
/// to countersign** and return that authorized [`AuthScope`], or [`Denied`] to
/// refuse. Implementations range from a trivial [`AllowlistPolicy`] to richer,
/// content-dependent rules; the countersigner treats them uniformly.
pub trait CountersignPolicy: Send + Sync {
    /// Authorizes `request`, returning the scope the Relay will countersign.
    ///
    /// # Errors
    /// [`Denied`] if the request is not permitted by policy.
    fn authorize(&self, request: &CertRequest) -> Result<AuthScope, Denied>;
}

/// The trivial policy: permit **iff every requested CN is in a fixed allowlist**,
/// authorizing the full requested scope. The simplest possible decision — a real
/// Relay plugs a richer [`CountersignPolicy`] into the same seam.
pub struct AllowlistPolicy {
    allowed: AuthScope,
}

impl AllowlistPolicy {
    /// Permits exactly the CNs in `allowed`.
    #[must_use]
    pub fn new(allowed: AuthScope) -> Self {
        Self { allowed }
    }
}

impl CountersignPolicy for AllowlistPolicy {
    fn authorize(&self, request: &CertRequest) -> Result<AuthScope, Denied> {
        if let Some(cn) = request.scope.cns().find(|cn| !self.allowed.contains(cn)) {
            return Err(Denied::new(format!("cn not permitted: {cn}")));
        }
        Ok(request.scope.clone())
    }
}

/// The CA-signing mechanism the countersigner needs, as a seam over the driver.
///
/// The real [`VaultClient`] implements it via the PKI engine's `sign-verbatim`;
/// tests implement it with the driver-level `MockPki`. It signs the CSR as-is
/// (the module chose the subject/CNs); the policy — not this seam — decides
/// whether that request is allowed.
pub trait PkiBackend: Send + Sync {
    /// Countersigns the PEM-encoded `csr` under the PKI `role` at `mount`.
    fn sign_verbatim(
        &self,
        mount: &str,
        role: &str,
        csr: &str,
    ) -> impl Future<Output = Result<PkiSignedCert, VaultError>> + Send;
}

impl PkiBackend for VaultClient {
    async fn sign_verbatim(
        &self,
        mount: &str,
        role: &str,
        csr: &str,
    ) -> Result<PkiSignedCert, VaultError> {
        self.pki(mount).sign_verbatim(role, csr).await
    }
}

/// A countersign failure. Carries no secret material, so it is always safe to log.
#[derive(Debug)]
pub enum CountersignError {
    /// The policy refused the request.
    Denied(Denied),
    /// The Vault PKI backend failed to sign.
    Backend(VaultError),
    /// The certificate Vault returned could not be decoded to DER.
    CertEncoding(String),
    /// The policy authorized a strict *subset* of the requested scope, but
    /// `sign-verbatim` signs the request's CSR unchanged — so the issued cert's
    /// identity would exceed the authorized scope. Refused until the mechanism
    /// rebuilds/verifies the signed content against the authorization.
    ScopeNarrowingUnsupported,
}

impl fmt::Display for CountersignError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Denied(d) => write!(f, "countersignature denied: {d}"),
            Self::Backend(e) => write!(f, "vault pki backend error: {e}"),
            Self::CertEncoding(m) => write!(f, "countersigned certificate not decodable: {m}"),
            Self::ScopeNarrowingUnsupported => write!(
                f,
                "scope narrowing unsupported: policy authorized a subset but sign-verbatim signs the CSR unchanged"
            ),
        }
    }
}

impl std::error::Error for CountersignError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Backend(e) => Some(e),
            _ => None,
        }
    }
}

/// The Relay's runtime countersigner: a [`CountersignPolicy`] (decision) plus a
/// [`PkiBackend`] (mechanism), targeting a PKI `mount`/`role`.
///
/// It authorizes the request, then — only if permitted — has Vault countersign
/// the CSR, returning a [`CounterSignedCert`] carrying the authorized scope. The
/// signing key stays in Vault; this type never holds it.
pub struct VaultPkiCountersigner<Pol, B = VaultClient> {
    policy: Pol,
    backend: B,
    mount: String,
    role: String,
}

impl<Pol: CountersignPolicy> VaultPkiCountersigner<Pol, VaultClient> {
    /// Connects to Vault at `address` with `token`, countersigning via the PKI
    /// engine at `mount` under `role`, authorized by `policy`.
    ///
    /// # Errors
    /// [`VaultError`] if the address or client configuration is invalid.
    pub fn connect(
        policy: Pol,
        address: &str,
        token: &str,
        mount: impl Into<String>,
        role: impl Into<String>,
    ) -> Result<Self, VaultError> {
        Ok(Self::new(
            policy,
            VaultClient::new(address, token)?,
            mount,
            role,
        ))
    }
}

impl<Pol: CountersignPolicy, B: PkiBackend> VaultPkiCountersigner<Pol, B> {
    /// Wires a `policy` and a [`PkiBackend`] targeting `mount`/`role`.
    pub fn new(policy: Pol, backend: B, mount: impl Into<String>, role: impl Into<String>) -> Self {
        Self {
            policy,
            backend,
            mount: mount.into(),
            role: role.into(),
        }
    }

    /// Countersigns `request` if policy permits, via Vault PKI `sign-verbatim`.
    ///
    /// # Errors
    /// [`CountersignError::Denied`] if the policy refuses;
    /// [`CountersignError::ScopeNarrowingUnsupported`] if the policy authorizes a
    /// strict subset of the requested scope (see below); [`CountersignError::Backend`]
    /// on a Vault failure; [`CountersignError::CertEncoding`] if the returned
    /// certificate is not decodable.
    pub async fn countersign(
        &self,
        request: &CertRequest,
    ) -> Result<CounterSignedCert, CountersignError> {
        let authorized = self
            .policy
            .authorize(request)
            .map_err(CountersignError::Denied)?;

        // `sign-verbatim` signs `request.csr_der` unchanged, so the issued cert's
        // identity is exactly the request's (its SANs == request.scope). This
        // countersigner can therefore only honor FULL-scope authorization: a
        // strict subset would yield a cert whose SANs exceed `authorized`, and
        // Vault cert-auth authorizes on the cert — not on this metadata. Fail
        // closed. See features/feature-008/spec.md (countersign scope contract).
        if authorized != request.scope {
            return Err(CountersignError::ScopeNarrowingUnsupported);
        }

        let csr_pem = der_to_pem(&request.csr_der, CSR_LABEL);
        let signed = self
            .backend
            .sign_verbatim(&self.mount, &self.role, &csr_pem)
            .await
            .map_err(CountersignError::Backend)?;

        let cert_der = pem_to_der(&signed.certificate).map_err(CountersignError::CertEncoding)?;
        Ok(CounterSignedCert::new(cert_der, authorized))
    }
}

/// Wraps DER bytes in a PEM block with `label`, at 64 base64 chars per line.
fn der_to_pem(der: &[u8], label: &str) -> String {
    let b64 = base64::engine::general_purpose::STANDARD.encode(der);
    let mut out = format!("-----BEGIN {label}-----\n");
    let mut i = 0;
    while i < b64.len() {
        let end = (i + 64).min(b64.len());
        out.push_str(&b64[i..end]); // base64 is ASCII → byte slicing is char-safe
        out.push('\n');
        i = end;
    }
    out.push_str("-----END ");
    out.push_str(label);
    out.push_str("-----\n");
    out
}

/// Extracts DER from a PEM block, ignoring the header/footer lines.
fn pem_to_der(pem: &str) -> Result<Vec<u8>, String> {
    let body: String = pem
        .lines()
        .filter(|line| !line.starts_with("-----"))
        .flat_map(str::chars)
        .filter(|c| !c.is_whitespace())
        .collect();
    if body.is_empty() {
        return Err("empty PEM body".to_owned());
    }
    base64::engine::general_purpose::STANDARD
        .decode(body.as_bytes())
        .map_err(|e| format!("invalid base64 in PEM: {e}"))
}

#[cfg(test)]
mod tests {
    use super::{der_to_pem, pem_to_der, AllowlistPolicy, CountersignPolicy, Denied};
    use connections::vault::port::CertRequest;
    use connections::vault::{AuthScope, Cn};

    fn request_for(cns: &[&str]) -> CertRequest {
        let scope = AuthScope::from_cns(cns.iter().map(|c| Cn::new(*c)));
        CertRequest::new(
            Cn::new(cns.first().copied().unwrap_or("subject")),
            scope,
            vec![1, 2, 3],
        )
    }

    #[test]
    fn pem_round_trips_der() {
        let der = b"\x00\x01DERBYTES\xff".to_vec();
        let pem = der_to_pem(&der, "CERTIFICATE REQUEST");
        assert!(pem.starts_with("-----BEGIN CERTIFICATE REQUEST-----"));
        assert_eq!(pem_to_der(&pem).expect("decode"), der);
    }

    #[test]
    fn pem_to_der_rejects_empty_body() {
        assert!(pem_to_der("-----BEGIN X-----\n-----END X-----\n").is_err());
    }

    #[test]
    fn allowlist_permits_subset_of_allowed() {
        let policy = AllowlistPolicy::new(AuthScope::from_cns([Cn::new("a"), Cn::new("b")]));
        let authorized = policy.authorize(&request_for(&["a"])).expect("permitted");
        assert!(authorized.contains(&Cn::new("a")));
    }

    #[test]
    fn allowlist_denies_unlisted_cn() {
        let policy = AllowlistPolicy::new(AuthScope::from_cns([Cn::new("a")]));
        let err = policy.authorize(&request_for(&["a", "rogue"])).unwrap_err();
        assert!(err.reason().contains("rogue"));
    }

    #[test]
    fn denied_display_carries_reason() {
        assert_eq!(Denied::new("nope").to_string(), "nope");
    }

    #[test]
    fn connect_builds_a_countersigner() {
        let policy = AllowlistPolicy::new(AuthScope::new());
        let cs = super::VaultPkiCountersigner::connect(
            policy,
            "http://127.0.0.1:8200",
            "hvs.test",
            "pki-int",
            "cic-module",
        );
        assert!(cs.is_ok());
    }
}
