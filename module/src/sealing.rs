//! Pubkey-sealing (feature-008 trust-flow step 7): ECIES over P-256.
//!
//! A returned secret is **encrypted to the module's public key** so that whatever
//! caches or routes it holds only ciphertext it cannot open — only the module
//! holding the matching private key can. This is public-key encryption to the
//! module's own key (ECIES: ephemeral ECDH → HKDF → AEAD), **not** Vault Transit
//! (which encrypts with its own managed keys, not to an external module key).
//!
//! Blob layout: `ephemeral_public(SEC1 uncompressed, 65B) || nonce(12B) ||
//! AES-256-GCM ciphertext`.

use aes_gcm::aead::{Aead, AeadCore, KeyInit};
use aes_gcm::{Aes256Gcm, Key, Nonce};
use connections::vault::{SealedSecret, SecretRef};
use hkdf::Hkdf;
use p256::ecdh::{diffie_hellman, EphemeralSecret};
use p256::elliptic_curve::sec1::ToEncodedPoint;
use p256::pkcs8::DecodePublicKey;
use p256::{NonZeroScalar, PublicKey};
use rand_core::OsRng;
use secrecy::SecretString;
use sha2::Sha256;

use crate::ModuleError;

/// Domain separation for the HKDF step — binds derived keys to this construction.
const HKDF_INFO: &[u8] = b"CIC-Relay/feature-008/pubkey-seal/v1";
/// SEC1 uncompressed P-256 public key length.
const EPH_PUB_LEN: usize = 65;
/// AES-GCM nonce length.
const NONCE_LEN: usize = 12;

/// Derives the AES-256 key from the ECDH shared secret via HKDF-SHA256.
fn derive_key(shared: &[u8]) -> Result<Key<Aes256Gcm>, ModuleError> {
    let hk = Hkdf::<Sha256>::new(None, shared);
    let mut okm = [0u8; 32];
    hk.expand(HKDF_INFO, &mut okm)
        .map_err(|_| ModuleError::new("hkdf expand"))?;
    Ok(Key::<Aes256Gcm>::from(okm))
}

/// Seals `plaintext` to `recipient_der` (the module's SPKI public key), tagging
/// it with `secret_ref`. The producer (host/Vault side) calls this; it never
/// needs — and never has — the private key.
///
/// # Errors
/// [`ModuleError`] if the recipient key is invalid or AEAD encryption fails.
pub fn seal(
    recipient_der: &[u8],
    secret_ref: SecretRef,
    plaintext: &[u8],
) -> Result<SealedSecret, ModuleError> {
    let recipient = PublicKey::from_public_key_der(recipient_der)
        .map_err(|e| ModuleError::new(format!("recipient key: {e}")))?;

    let ephemeral = EphemeralSecret::random(&mut OsRng);
    let eph_pub = ephemeral.public_key().to_encoded_point(false);
    let shared = ephemeral.diffie_hellman(&recipient);
    let key = derive_key(&shared.raw_secret_bytes()[..])?;

    let cipher = Aes256Gcm::new(&key);
    let nonce = Aes256Gcm::generate_nonce(&mut OsRng);
    let ciphertext = cipher
        .encrypt(&nonce, plaintext)
        .map_err(|_| ModuleError::new("aead seal"))?;

    let mut blob = Vec::with_capacity(EPH_PUB_LEN + NONCE_LEN + ciphertext.len());
    blob.extend_from_slice(eph_pub.as_bytes());
    blob.extend_from_slice(&nonce);
    blob.extend_from_slice(&ciphertext);
    Ok(SealedSecret::new(secret_ref, blob))
}

/// Opens a sealed blob with the module's private `scalar`, recovering the
/// plaintext as a redacting [`SecretString`].
///
/// # Errors
/// [`ModuleError`] if the blob is malformed, the key does not match, the
/// ciphertext was tampered with, or the plaintext is not UTF-8.
pub(crate) fn open(scalar: &NonZeroScalar, blob: &[u8]) -> Result<SecretString, ModuleError> {
    if blob.len() < EPH_PUB_LEN + NONCE_LEN {
        return Err(ModuleError::new("sealed blob too short"));
    }
    let (eph_pub_bytes, rest) = blob.split_at(EPH_PUB_LEN);
    let (nonce_bytes, ciphertext) = rest.split_at(NONCE_LEN);

    let eph_pub = PublicKey::from_sec1_bytes(eph_pub_bytes)
        .map_err(|e| ModuleError::new(format!("ephemeral key: {e}")))?;
    let shared = diffie_hellman(scalar, eph_pub.as_affine());
    let key = derive_key(&shared.raw_secret_bytes()[..])?;

    let mut nonce_arr = [0u8; NONCE_LEN];
    nonce_arr.copy_from_slice(nonce_bytes);
    let nonce = Nonce::from(nonce_arr);
    let cipher = Aes256Gcm::new(&key);
    let plaintext = cipher
        .decrypt(&nonce, ciphertext)
        .map_err(|_| ModuleError::new("aead open (wrong key or tampered)"))?;

    let text = String::from_utf8(plaintext).map_err(|_| ModuleError::new("plaintext not utf-8"))?;
    Ok(SecretString::from(text))
}
