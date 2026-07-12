//! Live async bridge from the CIC secret-flow core to a `HashiCorp` Vault backend
//! (feature-008).
//!
//! This is the **adapter** layer: it owns *translation* between the
//! `vault-client-rs` driver (which owns *access*) and the `connections` core
//! types (which own *meaning*). It depends on the driver as a pinned git
//! dependency and produces core [`SealedSecret`] values.
//!
//! Crucially, it **never decrypts**: it reads the pubkey-sealed envelope stored
//! in Vault and wraps it opaquely, so only the module holding the matching
//! private key can open it. The `read_sealed` method is the live async
//! counterpart of the deterministic in-memory
//! [`connections::vault::port::SecretBackend`] contract.

use std::fmt;
use std::future::Future;

use base64::Engine;
use connections::vault::{SealedSecret, SecretRef};
use secrecy::SecretString;
use vault_client_rs::{AuthInfo, CertAuthOperations, VaultClient, VaultError};

pub mod countersign;
pub use countersign::{
    AllowlistPolicy, CountersignError, CountersignPolicy, Denied, PkiBackend, VaultPkiCountersigner,
};

/// The KV v2 field under which a pubkey-sealed secret envelope is stored.
const SEALED_FIELD: &str = "sealed";

/// Error from the Vault-backed adapter. Carries no plaintext secret material,
/// so it is always safe to log.
#[derive(Debug)]
pub enum AdapterError {
    /// No sealed secret exists at the reference (Vault reported not-found).
    NotFound(SecretRef),
    /// Vault refused access to the reference.
    Denied(SecretRef),
    /// The stored envelope was not valid base64.
    Decode(SecretRef),
    /// Certificate authentication was rejected by Vault.
    Unauthorized,
    /// Any other Vault backend failure.
    Backend(VaultError),
}

impl fmt::Display for AdapterError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::NotFound(r) => write!(f, "no sealed secret at {r}"),
            Self::Denied(r) => write!(f, "access denied to {r}"),
            Self::Decode(r) => write!(f, "sealed envelope at {r} is not valid base64"),
            Self::Unauthorized => f.write_str("certificate authentication rejected"),
            Self::Backend(e) => write!(f, "vault backend error: {e}"),
        }
    }
}

impl std::error::Error for AdapterError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Backend(e) => Some(e),
            _ => None,
        }
    }
}

/// Maps a Vault read error to an [`AdapterError`], attributing it to `secret_ref`.
fn map_read_error(err: VaultError, secret_ref: &SecretRef) -> AdapterError {
    match err {
        VaultError::NotFound { .. } | VaultError::FieldNotFound { .. } => {
            AdapterError::NotFound(secret_ref.clone())
        }
        VaultError::PermissionDenied { .. } | VaultError::AuthRequired => {
            AdapterError::Denied(secret_ref.clone())
        }
        other => AdapterError::Backend(other),
    }
}

/// Decodes a base64 `field` into a [`SealedSecret`] for `secret_ref`. The bytes
/// stay sealed — this never inspects or decrypts them.
fn decode_sealed(field: &str, secret_ref: &SecretRef) -> Result<SealedSecret, AdapterError> {
    base64::engine::general_purpose::STANDARD
        .decode(field)
        .map(|bytes| SealedSecret::new(secret_ref.clone(), bytes))
        .map_err(|_| AdapterError::Decode(secret_ref.clone()))
}

/// Maps a cert-auth login error to an [`AdapterError`].
fn map_auth_error(err: VaultError) -> AdapterError {
    match err {
        VaultError::PermissionDenied { .. } | VaultError::AuthRequired => {
            AdapterError::Unauthorized
        }
        other => AdapterError::Backend(other),
    }
}

/// A token issued by Vault cert-auth (the live counterpart of the
/// [`connections::vault::port::TokenProvider`] contract's output).
///
/// The `client_token` stays a redacting, zeroizing [`SecretString`] — never a
/// plain [`String`] — so it cannot leak into logs. Turning it into a
/// pubkey-sealed [`SealedSecret`] for at-rest storage is a separate step (the
/// pubkey-sealing slice), not yet wired.
#[derive(Debug, Clone)]
pub struct IssuedToken {
    client_token: SecretString,
    /// The token accessor — a non-secret handle for lookup/revocation.
    pub accessor: String,
    /// The effective policies attached to the token.
    pub policies: Vec<String>,
    /// Lease duration in seconds.
    pub lease_duration: u64,
    /// Whether the lease is renewable.
    pub renewable: bool,
}

impl IssuedToken {
    /// Projects Vault's [`AuthInfo`] into the fields we carry, keeping the token
    /// in its protected [`SecretString`] form. Takes a reference and clones, since
    /// [`AuthInfo`] is `Drop` (zeroizing) and cannot be moved out of.
    fn from_auth(auth: &AuthInfo) -> Self {
        Self {
            client_token: auth.client_token.clone(),
            accessor: auth.accessor.clone(),
            policies: auth.token_policies.clone(),
            lease_duration: auth.lease_duration,
            renewable: auth.renewable,
        }
    }

    /// The protected client token, for controlled hand-off to the sealing step.
    /// Reading the plaintext requires `secrecy::ExposeSecret` and deliberate intent.
    #[must_use]
    pub fn client_token(&self) -> &SecretString {
        &self.client_token
    }
}

/// The narrow set of Vault driver operations the adapter needs, as a seam.
///
/// This is the adapter's own port over the `vault-client-rs` driver: it names
/// exactly the two operations [`VaultSecretSource`] performs — a KV v2 field read
/// and a cert-auth login — each scoped by an explicit `mount`. The real
/// [`VaultClient`] implements it by delegating to the driver's mount-scoped
/// handlers; tests implement it with the driver-level trait mocks, so the
/// adapter's translation logic is exercised without `WireMock` or a live Vault.
///
/// Methods return `impl Future + Send` (rather than `async fn`) so the trait
/// stays object-safe-adjacent and imposes an explicit `Send` bound on the
/// returned futures.
pub trait VaultBackend: Send + Sync {
    /// Reads a single string `field` from the KV v2 secret at `mount`/`path`.
    fn read_field(
        &self,
        mount: &str,
        path: &str,
        field: &str,
    ) -> impl Future<Output = Result<String, VaultError>> + Send;

    /// Authenticates against the cert-auth backend at `mount`, optionally naming
    /// the cert `role`, yielding Vault's [`AuthInfo`].
    fn cert_login(
        &self,
        mount: &str,
        role: Option<&str>,
    ) -> impl Future<Output = Result<AuthInfo, VaultError>> + Send;
}

impl VaultBackend for VaultClient {
    async fn read_field(&self, mount: &str, path: &str, field: &str) -> Result<String, VaultError> {
        self.kv2(mount).read_field(path, field).await
    }

    async fn cert_login(&self, mount: &str, role: Option<&str>) -> Result<AuthInfo, VaultError> {
        self.auth().cert_at(mount).login(role).await
    }
}

/// A live secret source backed by a `HashiCorp` Vault KV v2 mount, via the
/// `vault-client-rs` driver (the default backend `B`).
///
/// It reads the pubkey-sealed envelope stored under the `sealed` field and wraps
/// it as a [`SealedSecret`]; it never decrypts. Generic over the [`VaultBackend`]
/// seam so it can be driven by the real driver or a mock.
#[derive(Debug)]
pub struct VaultSecretSource<B = VaultClient> {
    backend: B,
}

impl VaultSecretSource<VaultClient> {
    /// Connects to Vault at `address`, authenticating with `token`.
    ///
    /// # Errors
    /// [`VaultError`] if the address or client configuration is invalid.
    pub fn connect(address: &str, token: &str) -> Result<Self, VaultError> {
        Ok(Self::new(VaultClient::new(address, token)?))
    }

    /// Connects with an **mTLS client identity** for cert-auth login: the client
    /// presents `client_cert_pem` + `client_key_pem` in the TLS handshake and
    /// verifies the server against `ca_pem`. It starts tokenless —
    /// [`login`](Self::login) performs the cert-auth exchange and the resulting
    /// scoped token is what subsequent reads use.
    ///
    /// Gated behind the `live-vault` feature: it is exercised only by the opt-in
    /// live-Vault integration test (`tests/live_chain.rs`), never in the default
    /// build's unit/coverage/mutation runs.
    ///
    /// # Errors
    /// [`VaultError`] if the address, CA certificate, or client identity is invalid.
    #[cfg(feature = "live-vault")]
    pub fn connect_mtls(
        address: &str,
        ca_pem: &[u8],
        client_cert_pem: &[u8],
        client_key_pem: &[u8],
    ) -> Result<Self, VaultError> {
        let client = VaultClient::builder()
            .address(address)
            .ca_cert_pem(ca_pem.to_vec())
            .client_cert_pem(client_cert_pem.to_vec(), client_key_pem.to_vec())
            .build()?;
        Ok(Self::new(client))
    }
}

impl<B: VaultBackend> VaultSecretSource<B> {
    /// Wraps an existing [`VaultBackend`] (e.g. a [`VaultClient`]).
    #[must_use]
    pub fn new(backend: B) -> Self {
        Self { backend }
    }

    /// Reads the sealed envelope at `secret_ref` from Vault KV v2.
    ///
    /// The `mount` and `path` of the reference select the KV v2 secret; the
    /// `sealed` field carries the base64 ciphertext. Explicit versions are not
    /// yet wired — the latest version is read.
    ///
    /// # Errors
    /// [`AdapterError::NotFound`] if absent; [`AdapterError::Denied`] if access is
    /// refused; [`AdapterError::Decode`] if the stored field is not valid base64;
    /// [`AdapterError::Backend`] on any other Vault failure.
    pub async fn read_sealed(&self, secret_ref: &SecretRef) -> Result<SealedSecret, AdapterError> {
        let field = self
            .backend
            .read_field(&secret_ref.mount, &secret_ref.path, SEALED_FIELD)
            .await
            .map_err(|e| map_read_error(e, secret_ref))?;
        decode_sealed(&field, secret_ref)
    }

    /// Authenticates to Vault via the cert-auth backend at `mount`, presenting the
    /// (mTLS) client certificate configured on the transport and optionally naming
    /// the cert `role`. Returns the issued token with its lease metadata.
    ///
    /// This is the live async counterpart of the in-memory `TokenProvider`
    /// contract: the Relay-countersigned Cert is exchanged for a scoped token.
    ///
    /// # Errors
    /// [`AdapterError::Unauthorized`] if Vault rejects the certificate;
    /// [`AdapterError::Backend`] on any other Vault failure.
    pub async fn login(
        &self,
        mount: &str,
        role: Option<&str>,
    ) -> Result<IssuedToken, AdapterError> {
        let auth = self
            .backend
            .cert_login(mount, role)
            .await
            .map_err(map_auth_error)?;
        Ok(IssuedToken::from_auth(&auth))
    }
}

#[cfg(test)]
mod tests {
    use super::{
        decode_sealed, map_auth_error, map_read_error, AdapterError, IssuedToken, VaultSecretSource,
    };
    use base64::Engine;
    use connections::vault::SecretRef;
    use secrecy::{ExposeSecret, SecretString};
    use vault_client_rs::VaultError;

    fn secret_ref() -> SecretRef {
        SecretRef::new("secret", "db/creds")
    }

    #[test]
    fn decode_sealed_wraps_valid_base64_and_stays_redacted() {
        let payload = b"\x00\x01SEALEDBYTES\xff";
        let encoded = base64::engine::general_purpose::STANDARD.encode(payload);

        let sealed = decode_sealed(&encoded, &secret_ref()).expect("valid base64 decodes");
        assert_eq!(sealed.ciphertext(), payload);
        assert_eq!(sealed.secret_ref(), &secret_ref());
        assert!(!format!("{sealed:?}").contains("SEALEDBYTES")); // stays sealed
    }

    #[test]
    fn decode_sealed_rejects_invalid_base64() {
        let err = decode_sealed("not valid base64 !!!", &secret_ref()).unwrap_err();
        assert!(matches!(err, AdapterError::Decode(r) if r == secret_ref()));
    }

    #[test]
    fn map_read_error_classifies_vault_errors() {
        assert!(matches!(
            map_read_error(VaultError::NotFound { path: "p".into() }, &secret_ref()),
            AdapterError::NotFound(r) if r == secret_ref()
        ));
        assert!(matches!(
            map_read_error(
                VaultError::FieldNotFound {
                    mount: "m".into(),
                    path: "p".into(),
                    field: "f".into(),
                },
                &secret_ref(),
            ),
            AdapterError::NotFound(_)
        ));
        assert!(matches!(
            map_read_error(
                VaultError::PermissionDenied { errors: vec![] },
                &secret_ref()
            ),
            AdapterError::Denied(_)
        ));
        assert!(matches!(
            map_read_error(VaultError::AuthRequired, &secret_ref()),
            AdapterError::Denied(_)
        ));
        assert!(matches!(
            map_read_error(VaultError::EmptyResponse, &secret_ref()),
            AdapterError::Backend(_)
        ));
    }

    #[test]
    fn issued_token_protects_the_client_token() {
        // `AuthInfo` is `#[non_exhaustive]`, so we exercise the carrier directly.
        let token = IssuedToken {
            client_token: SecretString::from("hvs.SECRETTOKEN".to_string()),
            accessor: "acc-123".to_string(),
            policies: vec!["db.read".to_string()],
            lease_duration: 3600,
            renewable: true,
        };
        assert_eq!(token.accessor, "acc-123");
        assert_eq!(token.policies, ["db.read"]);
        assert_eq!(token.lease_duration, 3600);
        assert!(token.renewable);

        // The token is readable only via ExposeSecret, and Debug redacts it.
        assert_eq!(token.client_token().expose_secret(), "hvs.SECRETTOKEN");
        assert!(!format!("{token:?}").contains("hvs.SECRETTOKEN"));
    }

    #[test]
    fn map_auth_error_classifies_login_failures() {
        assert!(matches!(
            map_auth_error(VaultError::PermissionDenied { errors: vec![] }),
            AdapterError::Unauthorized
        ));
        assert!(matches!(
            map_auth_error(VaultError::AuthRequired),
            AdapterError::Unauthorized
        ));
        assert!(matches!(
            map_auth_error(VaultError::EmptyResponse),
            AdapterError::Backend(_)
        ));
    }

    #[test]
    fn adapter_error_displays_and_chains_without_leaking() {
        assert!(AdapterError::NotFound(secret_ref())
            .to_string()
            .contains("secret/db/creds"));
        assert!(AdapterError::Denied(secret_ref())
            .to_string()
            .contains("denied"));
        assert!(AdapterError::Decode(secret_ref())
            .to_string()
            .contains("base64"));

        assert!(AdapterError::Unauthorized.to_string().contains("rejected"));

        let backend = AdapterError::Backend(VaultError::EmptyResponse);
        assert!(!backend.to_string().is_empty());
        assert!(std::error::Error::source(&backend).is_some());
        assert!(std::error::Error::source(&AdapterError::NotFound(secret_ref())).is_none());
        assert!(std::error::Error::source(&AdapterError::Unauthorized).is_none());
    }

    #[test]
    fn connect_builds_a_source() {
        assert!(VaultSecretSource::connect("http://127.0.0.1:8200", "hvs.test").is_ok());
    }
}
