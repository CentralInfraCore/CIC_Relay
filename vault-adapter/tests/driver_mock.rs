//! Driver-level tests for `VaultSecretSource`, exercising the adapter's
//! translation logic against the `vault-client-rs-test` trait mocks — no HTTP,
//! no `WireMock`, no live Vault. The complementary HTTP-layer coverage lives in
//! `live_mock.rs`.

use vault_adapter::{AdapterError, VaultBackend, VaultSecretSource};
use vault_client_rs::{AuthInfo, CertAuthOperations, VaultError};
use vault_client_rs_test::mock::{auth_info, MockCertAuth, MockError, MockKv2};

use connections::vault::SecretRef;
use secrecy::ExposeSecret;
use serde_json::json;

/// Wires the driver-level trait mocks behind the adapter's [`VaultBackend`] seam.
/// The `mount` is ignored — the mocks are programmed by path/role directly.
struct MockBackend {
    kv2: MockKv2,
    cert: MockCertAuth,
}

impl VaultBackend for MockBackend {
    async fn read_field(
        &self,
        _mount: &str,
        path: &str,
        field: &str,
    ) -> Result<String, VaultError> {
        self.kv2.read_field(path, field).await
    }

    async fn cert_login(&self, _mount: &str, role: Option<&str>) -> Result<AuthInfo, VaultError> {
        self.cert.login(role).await
    }
}

fn secret_ref() -> SecretRef {
    SecretRef::new("secret", "db/creds")
}

fn source_with(kv2: MockKv2, cert: MockCertAuth) -> VaultSecretSource<MockBackend> {
    VaultSecretSource::new(MockBackend { kv2, cert })
}

#[tokio::test]
async fn read_sealed_wraps_the_sealed_field() {
    // "U0VBTEVETUFSS0VS" == base64("SEALEDMARKER"); the marker is not a substring
    // of the redacted Debug's field names.
    let kv2 = MockKv2::new().with_data("db/creds", json!({ "sealed": "U0VBTEVETUFSS0VS" }));
    let source = source_with(kv2, MockCertAuth::new());

    let sealed = source.read_sealed(&secret_ref()).await.expect("read");
    assert_eq!(sealed.ciphertext(), b"SEALEDMARKER");
    assert_eq!(sealed.secret_ref(), &secret_ref());
    // The ciphertext never surfaces in Debug.
    assert!(!format!("{sealed:?}").contains("SEALEDMARKER"));
}

#[tokio::test]
async fn read_sealed_missing_maps_to_not_found() {
    let kv2 = MockKv2::new().with_error("db/creds", MockError::NotFound);
    let source = source_with(kv2, MockCertAuth::new());

    let err = source.read_sealed(&secret_ref()).await.unwrap_err();
    assert!(matches!(err, AdapterError::NotFound(r) if r == secret_ref()));
}

#[tokio::test]
async fn read_sealed_denied_maps_to_denied() {
    let kv2 = MockKv2::new().with_error("db/creds", MockError::Denied);
    let source = source_with(kv2, MockCertAuth::new());

    let err = source.read_sealed(&secret_ref()).await.unwrap_err();
    assert!(matches!(err, AdapterError::Denied(r) if r == secret_ref()));
}

#[tokio::test]
async fn read_sealed_invalid_base64_maps_to_decode() {
    let kv2 = MockKv2::new().with_data("db/creds", json!({ "sealed": "not!valid!base64" }));
    let source = source_with(kv2, MockCertAuth::new());

    let err = source.read_sealed(&secret_ref()).await.unwrap_err();
    assert!(matches!(err, AdapterError::Decode(r) if r == secret_ref()));
}

#[tokio::test]
async fn login_returns_the_issued_token() {
    let cert = MockCertAuth::new().with_login(auth_info(
        "hvs.SECRET",
        "acc-9",
        &["db.read", "db.write"],
        3600,
        true,
    ));
    let source = source_with(MockKv2::new(), cert);

    let token = source.login("cert", Some("web")).await.expect("login");
    assert_eq!(token.accessor, "acc-9");
    assert_eq!(token.policies, ["db.read", "db.write"]);
    assert_eq!(token.lease_duration, 3600);
    assert!(token.renewable);
    assert_eq!(token.client_token().expose_secret(), "hvs.SECRET");
    // The token stays redacted under Debug.
    assert!(!format!("{token:?}").contains("hvs.SECRET"));
}

#[tokio::test]
async fn login_denied_maps_to_unauthorized() {
    let cert = MockCertAuth::new().with_error(MockError::Denied);
    let source = source_with(MockKv2::new(), cert);

    let err = source.login("cert", None).await.unwrap_err();
    assert!(matches!(err, AdapterError::Unauthorized));
}
