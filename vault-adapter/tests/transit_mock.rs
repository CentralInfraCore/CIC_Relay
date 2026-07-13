//! Exercises the **real** `<VaultClient as TransitBackend>` impl behind
//! [`VaultTransitSigner`] against a `wiremock` HTTP server — no live Vault, no
//! `live-vault` feature. This is the default-build counterpart of the opt-in
//! `transit_sign.rs` live test: it proves the driver wiring (endpoint, request
//! shape, response parsing) and, because it observes concrete sign/verify
//! results, kills the mutation-testing survivors the real delegation would
//! otherwise leave (`Ok(String::new())`, `Ok(true)`, `Ok(false)`).

use vault_adapter::{VaultSignature, VaultTransitSigner};
use vault_client_rs_test::test_client;
use vault_client_rs_test::wiremock::matchers::{method, path};
use vault_client_rs_test::wiremock::{Mock, MockServer, ResponseTemplate};

const MOUNT: &str = "cic-transit";
const KEY: &str = "cic-audit";

/// A stand-in audit-chain head `record_hash` (hex string bytes), the domain the
/// signer signs.
const HEAD: &[u8] = b"3b1f...record-hash-hex";

#[tokio::test]
async fn real_client_sign_returns_the_vault_signature() {
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path(format!("/v1/{MOUNT}/sign/{KEY}")))
        .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
            "data": { "signature": "vault:v1:REAL-TRANSIT-SIG" }
        })))
        .expect(1)
        .mount(&server)
        .await;

    let signer = VaultTransitSigner::new(test_client(&server).await, MOUNT, KEY);
    let sig = signer.sign(HEAD).await.expect("sign");
    // Kills the `Ok(String::new())` / `Ok("xyzzy")` mutants on the real impl.
    assert_eq!(sig.as_str(), "vault:v1:REAL-TRANSIT-SIG");
}

#[tokio::test]
async fn real_client_verify_reports_a_valid_signature() {
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path(format!("/v1/{MOUNT}/verify/{KEY}")))
        .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
            "data": { "valid": true }
        })))
        .expect(1)
        .mount(&server)
        .await;

    let signer = VaultTransitSigner::new(test_client(&server).await, MOUNT, KEY);
    let sig = VaultSignature::from_wire("vault:v1:stored-signature");
    // Kills the `Ok(false)` mutant on the real verify impl.
    assert!(signer.verify(HEAD, &sig).await.expect("verify"));
}

#[tokio::test]
async fn real_client_verify_reports_an_invalid_signature() {
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path(format!("/v1/{MOUNT}/verify/{KEY}")))
        .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
            "data": { "valid": false }
        })))
        .expect(1)
        .mount(&server)
        .await;

    let signer = VaultTransitSigner::new(test_client(&server).await, MOUNT, KEY);
    let sig = VaultSignature::from_wire("vault:v1:tampered");
    // Kills the `Ok(true)` mutant on the real verify impl.
    assert!(!signer.verify(HEAD, &sig).await.expect("verify"));
}
