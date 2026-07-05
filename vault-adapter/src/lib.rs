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

use base64::Engine;
use connections::vault::{SealedSecret, SecretRef};
use vault_client_rs::{VaultClient, VaultError};

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
    /// Any other Vault backend failure.
    Backend(VaultError),
}

impl fmt::Display for AdapterError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::NotFound(r) => write!(f, "no sealed secret at {r}"),
            Self::Denied(r) => write!(f, "access denied to {r}"),
            Self::Decode(r) => write!(f, "sealed envelope at {r} is not valid base64"),
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

/// A live secret source backed by a `HashiCorp` Vault KV v2 mount, via the
/// `vault-client-rs` driver.
///
/// It reads the pubkey-sealed envelope stored under the `sealed` field and wraps
/// it as a [`SealedSecret`]; it never decrypts.
#[derive(Debug)]
pub struct VaultSecretSource {
    client: VaultClient,
}

impl VaultSecretSource {
    /// Wraps an existing [`VaultClient`].
    #[must_use]
    pub fn new(client: VaultClient) -> Self {
        Self { client }
    }

    /// Connects to Vault at `address`, authenticating with `token`.
    ///
    /// # Errors
    /// [`VaultError`] if the address or client configuration is invalid.
    pub fn connect(address: &str, token: &str) -> Result<Self, VaultError> {
        Ok(Self::new(VaultClient::new(address, token)?))
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
            .client
            .kv2(&secret_ref.mount)
            .read_field(&secret_ref.path, SEALED_FIELD)
            .await
            .map_err(|e| map_read_error(e, secret_ref))?;
        decode_sealed(&field, secret_ref)
    }
}

#[cfg(test)]
mod tests {
    use super::{decode_sealed, map_read_error, AdapterError, VaultSecretSource};
    use base64::Engine;
    use connections::vault::SecretRef;
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

        let backend = AdapterError::Backend(VaultError::EmptyResponse);
        assert!(!backend.to_string().is_empty());
        assert!(std::error::Error::source(&backend).is_some());
        assert!(std::error::Error::source(&AdapterError::NotFound(secret_ref())).is_none());
    }

    #[test]
    fn connect_builds_a_source() {
        assert!(VaultSecretSource::connect("http://127.0.0.1:8200", "hvs.test").is_ok());
    }
}
