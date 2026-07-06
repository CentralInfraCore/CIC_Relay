//! Integration test driving the live PKI-signing path of `VaultPkiCountersigner`
//! against a mocked Vault HTTP server (`/v1/{mount}/sign-verbatim/{role}`),
//! covering the real `VaultClient` `PkiBackend` impl end-to-end.

use vault_adapter::{AllowlistPolicy, VaultPkiCountersigner};
use vault_client_rs::VaultClient;
use wiremock::matchers::{method, path};
use wiremock::{Mock, MockServer, ResponseTemplate};

use connections::vault::port::CertRequest;
use connections::vault::{AuthScope, Cn};

/// A PEM whose body base64-decodes to `COUNTERSIGNEDCERT`.
const SIGNED_PEM: &str =
    "-----BEGIN CERTIFICATE-----\nQ09VTlRFUlNJR05FRENFUlQ=\n-----END CERTIFICATE-----";

#[tokio::test]
async fn countersign_signs_via_the_pki_engine() {
    let server = MockServer::start().await;

    Mock::given(method("POST"))
        .and(path("/v1/pki-int/sign-verbatim/cic-module"))
        .respond_with(ResponseTemplate::new(200).set_body_json(serde_json::json!({
            "data": {
                "certificate": SIGNED_PEM,
                "issuing_ca": SIGNED_PEM,
                "ca_chain": [SIGNED_PEM],
                "serial_number": "00:11:22:33",
                "expiration": 0
            }
        })))
        .expect(1)
        .mount(&server)
        .await;

    let client = VaultClient::builder()
        .address(&server.uri())
        .token_str("test-token")
        .max_retries(0)
        .build()
        .expect("client builds");

    let policy = AllowlistPolicy::new(AuthScope::from_cns([Cn::new("task:build")]));
    let cs = VaultPkiCountersigner::new(policy, client, "pki-int", "cic-module");

    let request = CertRequest::new(
        Cn::new("task:build"),
        AuthScope::from_cns([Cn::new("task:build")]),
        vec![0xDE, 0xAD, 0xBE, 0xEF],
    );

    let cert = cs.countersign(&request).await.expect("countersigned");
    assert_eq!(cert.der(), b"COUNTERSIGNEDCERT");
    assert!(cert.scope().contains(&Cn::new("task:build")));
}
