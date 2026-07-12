//! Live Vault-Transit-backed audit-chain signer (feature-009): the runtime
//! counterpart of `http-executor`'s in-process `RecordSigner`, but the ECDSA
//! signing key lives in Vault's Transit engine and **never leaves it** — exactly
//! the custody model the countersign CA key already uses (`pki/sign-verbatim`).
//!
//! # Why a head anchor, not per-entry sync signing
//!
//! `http-executor`'s `HashChainedAuditSink` signs each entry *synchronously* with
//! a `RecordSigner` (an in-process ECDSA key). A Vault Transit call is *async*
//! network I/O; driving it from that sync `record()` path would mean blocking on
//! a runtime from inside a runtime — a footgun that panics under `tokio`.
//!
//! Because the chain is hash-linked (`record_hash_n` transitively binds every
//! earlier entry), signing the **head** `record_hash` with a Vault-held key
//! anchors the whole prefix in a single call — the standard tamper-evident-log
//! notarization shape. So this signer takes raw `record_hash` bytes (the exact
//! `ChainedEntry.record_hash.as_bytes()` domain the in-process signer signs) and
//! returns a Vault signature over them. The in-process `LocalEcdsaSigner` stays
//! for the pure/embedded path; this is its production key-custody counterpart.

use std::fmt;
use std::future::Future;

use vault_client_rs::{TransitOperations, TransitSignParams, VaultClient, VaultError};

/// A Vault Transit signature (`vault:v<version>:<base64>`), as returned by the
/// engine. Public, non-secret material — safe to store alongside the audit chain
/// and safe to log.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct VaultSignature(String);

impl VaultSignature {
    /// Reconstructs a signature from its stored wire form (`vault:v1:…`), so a
    /// signature persisted alongside the audit chain can later be re-verified via
    /// [`VaultTransitSigner::verify`].
    #[must_use]
    pub fn from_wire(wire: impl Into<String>) -> Self {
        Self(wire.into())
    }

    /// The wire form (`vault:v1:…`), for storage or a follow-up `verify`.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }

    /// Consumes the wrapper, yielding the owned wire string.
    #[must_use]
    pub fn into_inner(self) -> String {
        self.0
    }
}

impl fmt::Display for VaultSignature {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

/// A signing/verification failure from the Transit engine. Carries no secret
/// material (the signed input is a public hash, the key never leaves Vault), so
/// it is always safe to log.
#[derive(Debug)]
pub enum SignError {
    /// Vault policy refused the sign/verify operation (the token lacks the
    /// Transit capability, or the key is not signable).
    Denied,
    /// Any other Transit backend failure.
    Backend(VaultError),
}

impl fmt::Display for SignError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Denied => f.write_str("vault transit sign/verify denied by policy"),
            Self::Backend(e) => write!(f, "vault transit backend error: {e}"),
        }
    }
}

impl std::error::Error for SignError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Backend(e) => Some(e),
            Self::Denied => None,
        }
    }
}

/// Maps a Transit sign/verify error to a [`SignError`].
fn map_sign_error(err: VaultError) -> SignError {
    match err {
        VaultError::PermissionDenied { .. } | VaultError::AuthRequired => SignError::Denied,
        other => SignError::Backend(other),
    }
}

/// The narrow Transit operations the signer needs, as a seam over the driver.
///
/// This is the signer's own port over the `vault-client-rs` driver: it names
/// exactly the two operations [`VaultTransitSigner`] performs — a Transit `sign`
/// and a Transit `verify`, each scoped by an explicit `mount` and key `name`. The
/// real [`VaultClient`] implements it via the driver's mount-scoped Transit
/// handler; tests implement it with an in-memory fake, so the signer's
/// translation logic is exercised without a live Vault.
///
/// Methods return `impl Future + Send` (rather than `async fn`) so the trait
/// imposes an explicit `Send` bound on the returned futures, matching the rest of
/// the adapter's seams.
pub trait TransitBackend: Send + Sync {
    /// Signs `input` with the Transit key `name` at `mount`, returning the
    /// engine's `vault:v…` signature string.
    fn transit_sign(
        &self,
        mount: &str,
        name: &str,
        input: &[u8],
    ) -> impl Future<Output = Result<String, VaultError>> + Send;

    /// Verifies `signature` over `input` against the Transit key `name` at
    /// `mount`.
    fn transit_verify(
        &self,
        mount: &str,
        name: &str,
        input: &[u8],
        signature: &str,
    ) -> impl Future<Output = Result<bool, VaultError>> + Send;
}

impl TransitBackend for VaultClient {
    async fn transit_sign(
        &self,
        mount: &str,
        name: &str,
        input: &[u8],
    ) -> Result<String, VaultError> {
        // Default params: for an ecdsa-p256 key Vault signs `sha2-256(input)` and
        // marshals as ASN.1 (DER). Signing and verifying both use the defaults, so
        // they agree; the input here is already a 32-byte `record_hash`.
        self.transit(mount)
            .sign(name, input, &TransitSignParams::default())
            .await
    }

    async fn transit_verify(
        &self,
        mount: &str,
        name: &str,
        input: &[u8],
        signature: &str,
    ) -> Result<bool, VaultError> {
        self.transit(mount).verify(name, input, signature).await
    }
}

/// A Vault-Transit-backed audit-chain signer. The ECDSA signing key `key` at the
/// Transit `mount` lives in Vault; this type only asks Vault to sign or verify —
/// it never holds private key material.
///
/// Generic over the [`TransitBackend`] seam so it can be driven by the real
/// driver or an in-memory fake.
#[derive(Debug)]
pub struct VaultTransitSigner<B = VaultClient> {
    backend: B,
    mount: String,
    key: String,
}

impl VaultTransitSigner<VaultClient> {
    /// Connects to Vault at `address` with token auth, targeting the Transit key
    /// `key` at `mount`.
    ///
    /// This is the plain-transport constructor (system-trusted TLS or http). A
    /// custom-CA or mTLS client is built by the caller and wrapped with
    /// [`new`](Self::new) instead — mirroring [`VaultSecretSource`].
    ///
    /// [`VaultSecretSource`]: crate::VaultSecretSource
    ///
    /// # Errors
    /// [`VaultError`] if the address or client configuration is invalid.
    pub fn connect(address: &str, token: &str, mount: &str, key: &str) -> Result<Self, VaultError> {
        Ok(Self::new(VaultClient::new(address, token)?, mount, key))
    }
}

impl<B: TransitBackend> VaultTransitSigner<B> {
    /// Wraps an existing [`TransitBackend`] (e.g. a configured [`VaultClient`]),
    /// targeting the Transit key `key` at `mount`.
    #[must_use]
    pub fn new(backend: B, mount: &str, key: &str) -> Self {
        Self {
            backend,
            mount: mount.to_owned(),
            key: key.to_owned(),
        }
    }

    /// Signs `message` — an audit-chain head `record_hash`, as bytes — with the
    /// Vault-held key. One call anchors the whole hash-linked prefix (see the
    /// module docs). `message` is expected to be the hex `record_hash` string's
    /// bytes, the same domain `http-executor`'s in-process `RecordSigner` signs.
    ///
    /// # Errors
    /// [`SignError::Denied`] if Vault policy refuses the operation;
    /// [`SignError::Backend`] on any other Transit failure.
    pub async fn sign(&self, message: &[u8]) -> Result<VaultSignature, SignError> {
        self.backend
            .transit_sign(&self.mount, &self.key, message)
            .await
            .map(VaultSignature)
            .map_err(map_sign_error)
    }

    /// Verifies a Vault `signature` over `message` against the Vault-held key.
    /// Returns `Ok(false)` for a well-formed but non-matching signature, and
    /// [`SignError`] only when Vault itself refuses or fails.
    ///
    /// # Errors
    /// [`SignError::Denied`] if Vault policy refuses the operation;
    /// [`SignError::Backend`] on any other Transit failure.
    pub async fn verify(
        &self,
        message: &[u8],
        signature: &VaultSignature,
    ) -> Result<bool, SignError> {
        self.backend
            .transit_verify(&self.mount, &self.key, message, signature.as_str())
            .await
            .map_err(map_sign_error)
    }
}

#[cfg(test)]
mod tests {
    use super::{map_sign_error, SignError, TransitBackend, VaultSignature, VaultTransitSigner};
    use std::collections::HashMap;
    use std::sync::Mutex;
    use vault_client_rs::VaultError;

    /// An in-memory Transit fake: `sign` returns a deterministic tag over
    /// `mount|key|input`, `verify` recomputes and compares. Enough to exercise the
    /// signer's translation, argument wiring, and error mapping without a Vault.
    #[derive(Default)]
    struct FakeTransit {
        /// Forced error to return instead of signing/verifying, if set.
        fail: Option<fn() -> VaultError>,
        /// Records the last `(mount, key, input)` a sign saw, for wiring assertions.
        seen: Mutex<HashMap<String, String>>,
    }

    impl FakeTransit {
        fn failing(f: fn() -> VaultError) -> Self {
            Self {
                fail: Some(f),
                seen: Mutex::new(HashMap::new()),
            }
        }

        fn tag(mount: &str, name: &str, input: &[u8]) -> String {
            // Not cryptographic — a stable, collision-resistant-enough fingerprint
            // so a different mount/key/input yields a different "signature".
            format!("vault:v1:{mount}/{name}/{}", hex(input))
        }
    }

    fn hex(bytes: &[u8]) -> String {
        use std::fmt::Write as _;
        bytes.iter().fold(String::new(), |mut s, b| {
            let _ = write!(s, "{b:02x}");
            s
        })
    }

    impl TransitBackend for FakeTransit {
        async fn transit_sign(
            &self,
            mount: &str,
            name: &str,
            input: &[u8],
        ) -> Result<String, VaultError> {
            if let Some(f) = self.fail {
                return Err(f());
            }
            self.seen
                .lock()
                .unwrap()
                .insert("sign".to_owned(), format!("{mount}|{name}|{}", hex(input)));
            Ok(Self::tag(mount, name, input))
        }

        async fn transit_verify(
            &self,
            mount: &str,
            name: &str,
            input: &[u8],
            signature: &str,
        ) -> Result<bool, VaultError> {
            if let Some(f) = self.fail {
                return Err(f());
            }
            Ok(signature == Self::tag(mount, name, input))
        }
    }

    fn signer(backend: FakeTransit) -> VaultTransitSigner<FakeTransit> {
        VaultTransitSigner::new(backend, "cic-transit", "cic-audit")
    }

    #[tokio::test]
    async fn sign_then_verify_round_trips() {
        let s = signer(FakeTransit::default());
        let head = b"a1b2c3d4"; // stands in for a record_hash's bytes
        let sig = s.sign(head).await.expect("sign");
        assert!(sig.as_str().starts_with("vault:v1:"));
        assert!(s.verify(head, &sig).await.expect("verify"));
    }

    #[tokio::test]
    async fn verify_rejects_a_tampered_message() {
        let s = signer(FakeTransit::default());
        let sig = s.sign(b"original-head").await.expect("sign");
        // A different head (a mutated chain) must not verify under the same sig.
        assert!(!s.verify(b"tampered-head", &sig).await.expect("verify"));
    }

    #[tokio::test]
    async fn sign_passes_the_configured_mount_and_key_through() {
        let backend = FakeTransit::default();
        let s = signer(backend);
        s.sign(b"\x00\x01\x02").await.expect("sign");
        let seen = s.backend_seen();
        assert_eq!(seen, "cic-transit|cic-audit|000102");
    }

    #[tokio::test]
    async fn policy_denial_maps_to_denied() {
        let s = signer(FakeTransit::failing(|| VaultError::PermissionDenied {
            errors: vec!["permission denied".to_owned()],
        }));
        assert!(matches!(s.sign(b"h").await, Err(SignError::Denied)));
        let sig = VaultSignature("vault:v1:x".to_owned());
        assert!(matches!(s.verify(b"h", &sig).await, Err(SignError::Denied)));
    }

    #[tokio::test]
    async fn auth_required_maps_to_denied() {
        let s = signer(FakeTransit::failing(|| VaultError::AuthRequired));
        assert!(matches!(s.sign(b"h").await, Err(SignError::Denied)));
    }

    #[tokio::test]
    async fn other_failures_map_to_backend() {
        let s = signer(FakeTransit::failing(|| VaultError::EmptyResponse));
        assert!(matches!(s.sign(b"h").await, Err(SignError::Backend(_))));
    }

    #[test]
    fn map_sign_error_classifies_vault_errors() {
        assert!(matches!(
            map_sign_error(VaultError::PermissionDenied { errors: vec![] }),
            SignError::Denied
        ));
        assert!(matches!(
            map_sign_error(VaultError::AuthRequired),
            SignError::Denied
        ));
        assert!(matches!(
            map_sign_error(VaultError::EmptyResponse),
            SignError::Backend(_)
        ));
    }

    #[test]
    fn sign_error_displays_and_chains_without_leaking() {
        assert!(SignError::Denied.to_string().contains("denied"));
        let backend = SignError::Backend(VaultError::EmptyResponse);
        assert!(!backend.to_string().is_empty());
        assert!(std::error::Error::source(&backend).is_some());
        assert!(std::error::Error::source(&SignError::Denied).is_none());
    }

    #[test]
    fn connect_builds_a_signer() {
        assert!(VaultTransitSigner::connect(
            "http://127.0.0.1:8200",
            "hvs.test",
            "cic-transit",
            "cic-audit"
        )
        .is_ok());
    }

    #[test]
    fn vault_signature_exposes_and_displays_its_wire_form() {
        let sig = VaultSignature::from_wire("vault:v1:abc");
        assert_eq!(sig, VaultSignature("vault:v1:abc".to_owned()));
        assert_eq!(sig.as_str(), "vault:v1:abc");
        assert_eq!(sig.to_string(), "vault:v1:abc");
        assert_eq!(sig.clone().into_inner(), "vault:v1:abc");
    }

    impl VaultTransitSigner<FakeTransit> {
        /// Test-only peek at what the fake backend recorded for the last sign.
        fn backend_seen(&self) -> String {
            self.backend
                .seen
                .lock()
                .unwrap()
                .get("sign")
                .cloned()
                .unwrap_or_default()
        }
    }
}
