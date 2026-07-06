//! Module-side identity for the CIC secret flow (feature-008), trust-flow step 2.
//!
//! A workload (the "module") generates its own keypair and a PKCS#10 CSR whose
//! subject/SANs encode the task's CNs, and hands the resulting [`CertRequest`] to
//! the Relay for countersignature. **The private key never leaves this boundary** —
//! [`ModuleIdentity`] holds it (zeroizing) and only ever emits public material
//! (the CSR now; the public key for pubkey-sealing later).
//!
//! Pure-Rust crypto (`p256` + `x509-cert`), so the module side carries no C
//! dependency and stays within the workspace's permissive-only supply chain. The
//! key is ECDSA P-256; the intermediate CA that countersigns it is independent.

use std::fmt;
use std::str::FromStr;

use connections::vault::port::CertRequest;
use connections::vault::{AuthScope, Cn, SealedSecret};
use p256::ecdsa::{DerSignature, SigningKey};
use p256::pkcs8::EncodePublicKey;
use rand_core::OsRng;
use secrecy::SecretString;
use x509_cert::builder::{Builder, RequestBuilder};
use x509_cert::der::asn1::Ia5String;
use x509_cert::der::Encode;
use x509_cert::ext::pkix::name::GeneralName;
use x509_cert::ext::pkix::SubjectAltName;
use x509_cert::name::Name;

pub mod sealing;
pub use sealing::seal;

/// A failure while generating the module identity or its CSR. Carries no key
/// material, so it is always safe to log.
#[derive(Debug)]
pub struct ModuleError(String);

impl ModuleError {
    fn new(message: impl Into<String>) -> Self {
        Self(message.into())
    }

    /// The public failure message.
    #[must_use]
    pub fn message(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for ModuleError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "module identity error: {}", self.0)
    }
}

impl std::error::Error for ModuleError {}

/// A module's cryptographic identity: an ECDSA P-256 keypair whose **private half
/// never leaves this type**. It is the key holder of the trust flow — it emits the
/// CSR now and (later) opens pubkey-sealed secrets. The signing key zeroizes on drop.
pub struct ModuleIdentity {
    signing_key: SigningKey,
}

impl fmt::Debug for ModuleIdentity {
    /// Redacts the signing key — a module identity never leaks its private half,
    /// not even in logs or traces.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ModuleIdentity")
            .field("signing_key", &"<redacted>")
            .finish()
    }
}

impl ModuleIdentity {
    /// Generates a fresh identity and the [`CertRequest`] to countersign for
    /// `subject`/`scope`: a new P-256 keypair plus a CSR whose subject CN is
    /// `subject` and whose DNS SANs carry the `scope` CNs (the task encoding).
    ///
    /// # Errors
    /// [`ModuleError`] if the subject/CNs cannot be encoded or the CSR cannot be
    /// built and signed.
    pub fn generate(subject: Cn, scope: AuthScope) -> Result<(Self, CertRequest), ModuleError> {
        let signing_key = SigningKey::random(&mut OsRng);
        let csr_der = build_csr(&signing_key, &subject, &scope)?;
        let request = CertRequest::new(subject, scope, csr_der);
        Ok((Self { signing_key }, request))
    }

    /// The module's public key as SPKI DER — safe to share; the private half never
    /// leaves. This is the key a returned secret is sealed to (see [`seal`]).
    ///
    /// # Errors
    /// [`ModuleError`] if the public key cannot be encoded.
    pub fn public_key_der(&self) -> Result<Vec<u8>, ModuleError> {
        self.signing_key
            .verifying_key()
            .to_public_key_der()
            .map(|doc| doc.as_bytes().to_vec())
            .map_err(|e| ModuleError::new(e.to_string()))
    }

    /// Opens a [`SealedSecret`] that was sealed to this module's public key,
    /// recovering the plaintext as a redacting [`SecretString`]. This is the
    /// module acting as the execution endpoint — the secret is decrypted *inside*
    /// the module, never handed out in the open.
    ///
    /// # Errors
    /// [`ModuleError`] if the blob is malformed, was sealed to a different key, or
    /// was tampered with.
    pub fn open(&self, sealed: &SealedSecret) -> Result<SecretString, ModuleError> {
        sealing::open(self.signing_key.as_nonzero_scalar(), sealed.ciphertext())
    }
}

/// Builds a DER PKCS#10 CSR for `subject`/`scope`, signed by `signing_key`.
fn build_csr(
    signing_key: &SigningKey,
    subject: &Cn,
    scope: &AuthScope,
) -> Result<Vec<u8>, ModuleError> {
    let name = Name::from_str(&format!("CN={}", subject.as_str()))
        .map_err(|e| ModuleError::new(format!("invalid subject: {e}")))?;

    let mut builder = RequestBuilder::new(name, signing_key)
        .map_err(|e| ModuleError::new(format!("csr builder: {e}")))?;

    if !scope.is_empty() {
        let mut sans = Vec::new();
        for cn in scope.cns() {
            let dns = Ia5String::new(cn.as_str())
                .map_err(|e| ModuleError::new(format!("invalid cn '{cn}': {e}")))?;
            sans.push(GeneralName::DnsName(dns));
        }
        builder
            .add_extension(&SubjectAltName(sans))
            .map_err(|e| ModuleError::new(format!("san extension: {e}")))?;
    }

    let csr = builder
        .build::<DerSignature>()
        .map_err(|e| ModuleError::new(format!("csr sign: {e}")))?;
    csr.to_der()
        .map_err(|e| ModuleError::new(format!("csr encode: {e}")))
}

#[cfg(test)]
mod tests {
    use super::{seal, ModuleIdentity};
    use connections::vault::{AuthScope, Cn, SealedSecret, SecretRef};
    use secrecy::ExposeSecret;

    fn scope(cns: &[&str]) -> AuthScope {
        AuthScope::from_cns(cns.iter().map(|c| Cn::new(*c)))
    }

    fn secret_ref() -> SecretRef {
        SecretRef::new("secret", "db/creds")
    }

    #[test]
    fn debug_redacts_the_signing_key() {
        let (id, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("gen");
        assert_eq!(
            format!("{id:?}"),
            "ModuleIdentity { signing_key: \"<redacted>\" }"
        );
    }

    #[test]
    fn generate_yields_request_matching_inputs() {
        let (_id, req) =
            ModuleIdentity::generate(Cn::new("task:build"), scope(&["task:build", "env:prod"]))
                .expect("generate");
        assert_eq!(req.subject, Cn::new("task:build"));
        assert!(req.scope.contains(&Cn::new("env:prod")));
        // A DER SEQUENCE-encoded CSR was produced.
        assert!(!req.csr_der.is_empty());
        assert_eq!(req.csr_der.first(), Some(&0x30));
    }

    #[test]
    fn generate_works_with_empty_scope() {
        let (_id, req) =
            ModuleIdentity::generate(Cn::new("task:build"), AuthScope::new()).expect("generate");
        assert!(req.scope.is_empty());
        assert!(!req.csr_der.is_empty());
    }

    #[test]
    fn each_identity_has_a_distinct_key() {
        let (a, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("a");
        let (b, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("b");
        assert_ne!(
            a.public_key_der().expect("a pub"),
            b.public_key_der().expect("b pub")
        );
    }

    #[test]
    fn public_key_der_is_stable_and_nonempty() {
        let (id, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("gen");
        let first = id.public_key_der().expect("pub");
        assert!(!first.is_empty());
        assert_eq!(first, id.public_key_der().expect("pub again"));
    }

    #[test]
    fn non_ascii_cn_in_scope_is_rejected() {
        // SAN CNs must be IA5 (ASCII); a non-ASCII CN fails encoding with a
        // public, key-free error message.
        let err = ModuleIdentity::generate(Cn::new("task"), scope(&["tásk"])).unwrap_err();
        assert!(!err.message().is_empty());
        assert!(err.to_string().starts_with("module identity error"));
    }

    #[test]
    fn seal_open_round_trips() {
        let (id, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("gen");
        let pubkey = id.public_key_der().expect("pub");
        let sealed = seal(&pubkey, secret_ref(), b"hvs.TOKEN").expect("seal");

        // The plaintext never appears in the sealed blob.
        assert!(!sealed
            .ciphertext()
            .windows(b"hvs.TOKEN".len())
            .any(|w| w == b"hvs.TOKEN"));

        let opened = id.open(&sealed).expect("open");
        assert_eq!(opened.expose_secret(), "hvs.TOKEN");
    }

    #[test]
    fn a_different_module_cannot_open() {
        let (a, _) = ModuleIdentity::generate(Cn::new("a"), AuthScope::new()).expect("a");
        let (b, _) = ModuleIdentity::generate(Cn::new("b"), AuthScope::new()).expect("b");
        let sealed = seal(&a.public_key_der().expect("pub"), secret_ref(), b"top").expect("seal");
        assert!(b.open(&sealed).is_err());
    }

    #[test]
    fn tampered_ciphertext_fails_to_open() {
        let (id, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("gen");
        let sealed = seal(&id.public_key_der().expect("pub"), secret_ref(), b"top").expect("seal");
        let mut bytes = sealed.ciphertext().to_vec();
        let last = bytes.len() - 1;
        bytes[last] ^= 0xff;
        let tampered = SealedSecret::new(secret_ref(), bytes);
        assert!(id.open(&tampered).is_err());
    }

    #[test]
    fn open_rejects_a_short_blob() {
        let (id, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("gen");
        let short = SealedSecret::new(secret_ref(), vec![0u8; 10]);
        assert!(id.open(&short).is_err());
    }

    #[test]
    fn open_rejects_a_blob_one_byte_under_the_header_length() {
        // Header is eph_pub(65) + nonce(12) = 77 bytes; one byte under that must
        // still be flagged as the length check, not fail later while slicing.
        let (id, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("gen");
        let short = SealedSecret::new(secret_ref(), vec![0u8; 76]);
        let err = id.open(&short).unwrap_err();
        assert!(err.message().contains("too short"));
    }

    #[test]
    fn open_accepts_a_blob_exactly_at_the_header_length() {
        // At exactly 77 bytes the length guard must pass — the header is
        // present in full (even though the header bytes here are garbage, so
        // it still fails, just later, at ephemeral-key parsing).
        let (id, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("gen");
        let at_header = SealedSecret::new(secret_ref(), vec![0u8; 77]);
        let err = id.open(&at_header).unwrap_err();
        assert!(!err.message().contains("too short"));
    }

    #[test]
    fn seal_rejects_an_invalid_recipient_key() {
        let err = seal(b"not a valid SPKI DER key", secret_ref(), b"x").unwrap_err();
        assert!(err.message().contains("recipient key"));
    }

    #[test]
    fn open_rejects_non_utf8_plaintext() {
        let (id, _) = ModuleIdentity::generate(Cn::new("m"), AuthScope::new()).expect("gen");
        let sealed = seal(
            &id.public_key_der().expect("pub"),
            secret_ref(),
            &[0xff, 0xfe],
        )
        .expect("seal");
        let err = id.open(&sealed).unwrap_err();
        assert!(err.message().contains("utf-8"));
    }
}
