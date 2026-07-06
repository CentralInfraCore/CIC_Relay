//! Integration tests driving the live async paths of `VaultSecretSource`
//! (`read_sealed`, `login`) against a mocked Vault HTTP server, mirroring the
//! driver's own wiremock test setup (`/v1/...` paths, KV v2 and auth envelopes).

use base64::Engine;
use connections::vault::SecretRef;
use secrecy::ExposeSecret;
use vault_adapter::{AdapterError, VaultSecretSource};
use vault_client_rs::VaultClient;
use wiremock::matchers::{method, path};
use wiremock::{Mock, MockServer, ResponseTemplate};

/// Builds a source pointed at the mock server, with retries off so error-status
/// mocks are hit exactly once.
fn source(server: &MockServer) -> VaultSecretSource {
    let client = VaultClient::builder()
        .address(&server.uri())
        .token_str("test-token")
        .max_retries(0)
        .build()
        .expect("client builds");
    VaultSecretSource::new(client)
}

#[tokio::test]
async fn read_sealed_returns_the_sealed_envelope() {
    let server = MockServer::start().await;
    let payload = b"\x00\x01SEALEDBYTES\xff";
    let encoded = base64::engine::general_purpose::STANDARD.encode(payload);

    Mock::given(method("GET"))
        .and(path("/v1/secret/data/db/creds"))
        .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
            "data": {
                "data": { "sealed": encoded },
                "metadata": {
                    "version": 1,
                    "created_time": "2025-01-01T00:00:00Z",
                    "deletion_time": "",
                    "destroyed": false
                }
            }
        })))
        .expect(1)
        .mount(&server)
        .await;

    let sealed = source(&server)
        .read_sealed(&SecretRef::new("secret", "db/creds"))
        .await
        .expect("sealed envelope resolves");

    assert_eq!(sealed.ciphertext(), payload);
    assert!(!format!("{sealed:?}").contains("SEALEDBYTES")); // stays sealed
}

#[tokio::test]
async fn read_sealed_maps_404_to_not_found() {
    let server = MockServer::start().await;

    Mock::given(method("GET"))
        .and(path("/v1/secret/data/missing"))
        .respond_with(ResponseTemplate::new(404).set_body_json(serde_json::json!({ "errors": [] })))
        .expect(1)
        .mount(&server)
        .await;

    let missing = SecretRef::new("secret", "missing");
    let err = source(&server)
        .read_sealed(&missing)
        .await
        .expect_err("missing ref errors");

    assert!(matches!(err, AdapterError::NotFound(r) if r == missing));
}

#[tokio::test]
async fn login_returns_the_issued_token() {
    let server = MockServer::start().await;

    Mock::given(method("POST"))
        .and(path("/v1/auth/cert/login"))
        .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
            "auth": {
                "client_token": "hvs.ISSUEDTOKEN",
                "accessor": "acc-9",
                "policies": ["default"],
                "token_policies": ["db.read"],
                "metadata": null,
                "lease_duration": 3600,
                "renewable": true,
                "entity_id": "ent-1",
                "token_type": "service",
                "orphan": false,
                "mfa_requirement": null,
                "num_uses": 0
            }
        })))
        .expect(1)
        .mount(&server)
        .await;

    let token = source(&server)
        .login("cert", Some("web"))
        .await
        .expect("login issues a token");

    assert_eq!(token.policies, ["db.read"]);
    assert_eq!(token.lease_duration, 3600);
    assert!(token.renewable);
    assert_eq!(token.client_token().expose_secret(), "hvs.ISSUEDTOKEN");
}

#[tokio::test]
async fn login_maps_403_to_unauthorized() {
    let server = MockServer::start().await;

    Mock::given(method("POST"))
        .and(path("/v1/auth/cert/login"))
        .respond_with(
            ResponseTemplate::new(403)
                .set_body_json(serde_json::json!({ "errors": ["permission denied"] })),
        )
        .expect(1)
        .mount(&server)
        .await;

    let err = source(&server)
        .login("cert", None)
        .await
        .expect_err("rejected cert errors");

    assert!(matches!(err, AdapterError::Unauthorized));
}
