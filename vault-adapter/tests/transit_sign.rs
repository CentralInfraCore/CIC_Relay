#![cfg(feature = "live-vault")]
//! Opt-in **live-Vault** test of the feature-009 audit-chain signer: it signs an
//! audit-chain head `record_hash` with a Vault-Transit-held ECDSA key and verifies
//! it back — proving the signing key never leaves Vault (the production custody
//! model), the tamper-evident-log head-anchor shape, and the stored-signature
//! round-trip.
//!
//! It is a no-op unless `VAULT_LIVE_ADDR` is set, so it never runs in the default
//! unit/coverage/mutation builds. Run it via `make test-vault-live`, which spins a
//! throwaway `-dev-tls` server, enables a Transit mount with an `ecdsa-p256`
//! signing key, and points the `VAULT_LIVE_*` env vars at it.

use vault_adapter::{VaultSignature, VaultTransitSigner};
use vault_client_rs::VaultClient;

/// A realistic audit-chain head: a 64-hex-char `record_hash`. The signer signs
/// `record_hash.as_bytes()`, exactly the domain `http-executor`'s in-process
/// `RecordSigner` signs, so a Vault anchor and an in-process signature bind the
/// same value.
const HEAD_HASH: &str = "3b1f9c0a7e5d2b48f6a1c3e59d7042b8c1f6a9d3e0b4278c5a1f9e3d6b0c2748a";

struct Config {
    addr: String,
    token: String,
    cacert: String,
    transit_mount: String,
    transit_key: String,
}

/// Reads the `VAULT_LIVE_*` env, or `None` (skip) if the server address is unset.
fn config() -> Option<Config> {
    let addr = std::env::var("VAULT_LIVE_ADDR").ok()?;
    let get = |k: &str, default: &str| std::env::var(k).unwrap_or_else(|_| default.to_owned());
    Some(Config {
        addr,
        token: get("VAULT_LIVE_TOKEN", "root"),
        cacert: std::env::var("VAULT_LIVE_CACERT").expect("VAULT_LIVE_CACERT must be set"),
        transit_mount: get("VAULT_LIVE_TRANSIT_MOUNT", "cic-transit"),
        transit_key: get("VAULT_LIVE_TRANSIT_KEY", "cic-audit"),
    })
}

/// Builds a token-auth client verifying the dev server's CA. The Relay's audit
/// signer runs with a token that carries the Transit sign/verify capability.
fn client(cfg: &Config, ca_pem: &[u8]) -> VaultClient {
    VaultClient::builder()
        .address(&cfg.addr)
        .token_str(&cfg.token)
        .ca_cert_pem(ca_pem.to_vec())
        .build()
        .expect("transit client")
}

#[tokio::test]
async fn transit_head_anchor_signs_and_verifies_against_live_vault() {
    let Some(cfg) = config() else {
        eprintln!("[skip] VAULT_LIVE_ADDR unset — run via `make test-vault-live`");
        return;
    };
    let ca_pem = std::fs::read(&cfg.cacert).expect("read CA cert");

    let signer =
        VaultTransitSigner::new(client(&cfg, &ca_pem), &cfg.transit_mount, &cfg.transit_key);
    let head = HEAD_HASH.as_bytes();

    // 1. Anchor the chain head with the Vault-held key. The private key never
    //    leaves Vault; we only receive the `vault:v1:…` signature.
    let sig = signer.sign(head).await.expect("transit sign");
    assert!(
        sig.as_str().starts_with("vault:v1:"),
        "expected a versioned Vault signature, got {sig}"
    );

    // 2. The anchor verifies against the same Vault-held key.
    assert!(
        signer.verify(head, &sig).await.expect("transit verify"),
        "the head anchor must verify against its own key"
    );

    // 3. A tampered head (any earlier entry mutated changes the head hash) must
    //    NOT verify — this is the tamper-evidence the anchor buys.
    let tampered = b"0000000000000000000000000000000000000000000000000000000000000000";
    assert!(
        !signer.verify(tampered, &sig).await.expect("transit verify"),
        "a mutated chain head must not verify under the original anchor"
    );

    // 4. Stored-signature round-trip: reconstructing the signature from its wire
    //    form (as persisted alongside the chain) still verifies.
    let restored = VaultSignature::from_wire(sig.as_str());
    assert!(
        signer
            .verify(head, &restored)
            .await
            .expect("transit verify"),
        "a signature reloaded from storage must still verify"
    );

    eprintln!("[ok] live transit: head anchor sign → verify → tamper-detect → stored round-trip");
}
