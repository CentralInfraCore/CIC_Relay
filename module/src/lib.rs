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
use connections::vault::{AuthScope, Cn};
use p256::ecdsa::{DerSignature, SigningKey};
use p256::pkcs8::EncodePublicKey;
use rand_core::OsRng;
use x509_cert::builder::{Builder, RequestBuilder};
use x509_cert::der::asn1::Ia5String;
use x509_cert::der::Encode;
use x509_cert::ext::pkix::name::GeneralName;
use x509_cert::ext::pkix::SubjectAltName;
use x509_cert::name::Name;

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
    /// leaves. This is the key a returned secret will later be sealed to.
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
    use super::ModuleIdentity;
    use connections::vault::{AuthScope, Cn};

    fn scope(cns: &[&str]) -> AuthScope {
        AuthScope::from_cns(cns.iter().map(|c| Cn::new(*c)))
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
}
