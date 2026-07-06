//! Driver-level tests for `VaultPkiCountersigner`, exercising the policy decision
//! and PKI-signing translation against the `vault-client-rs-test` `MockPki` — no
//! live Vault, no HTTP.

use vault_adapter::{AllowlistPolicy, CountersignError, PkiBackend, VaultPkiCountersigner};
use vault_client_rs::{PkiOperations, PkiSignedCert, VaultError};
use vault_client_rs_test::mock::{pki_signed, MockError, MockPki};

use connections::vault::port::CertRequest;
use connections::vault::{AuthScope, Cn};

/// Wires `MockPki` behind the adapter's `PkiBackend` seam. `mount` is ignored.
struct MockPkiBackend {
    pki: MockPki,
}

impl PkiBackend for MockPkiBackend {
    async fn sign_verbatim(
        &self,
        _mount: &str,
        role: &str,
        csr: &str,
    ) -> Result<PkiSignedCert, VaultError> {
        self.pki.sign_verbatim(role, csr).await
    }
}

/// A PEM whose body base64-decodes to `COUNTERSIGNEDCERT`.
const SIGNED_PEM: &str =
    "-----BEGIN CERTIFICATE-----\nQ09VTlRFUlNJR05FRENFUlQ=\n-----END CERTIFICATE-----";

fn request_for(cns: &[&str]) -> CertRequest {
    let scope = AuthScope::from_cns(cns.iter().map(|c| Cn::new(*c)));
    CertRequest::new(Cn::new("module"), scope, vec![0xDE, 0xAD, 0xBE, 0xEF])
}

fn countersigner(
    allowed: &[&str],
    pki: MockPki,
) -> VaultPkiCountersigner<AllowlistPolicy, MockPkiBackend> {
    let policy = AllowlistPolicy::new(AuthScope::from_cns(allowed.iter().map(|c| Cn::new(*c))));
    VaultPkiCountersigner::new(policy, MockPkiBackend { pki }, "pki-int", "cic-module")
}

#[tokio::test]
async fn permitted_request_is_countersigned() {
    let pki = MockPki::new().with_signed(pki_signed(SIGNED_PEM, &[SIGNED_PEM]));
    let cs = countersigner(&["a", "b"], pki);

    let cert = cs
        .countersign(&request_for(&["a"]))
        .await
        .expect("countersigned");
    assert_eq!(cert.der(), b"COUNTERSIGNEDCERT");
    // The authorized scope is carried on the result.
    assert!(cert.scope().contains(&Cn::new("a")));
}

#[tokio::test]
async fn denied_cn_is_refused() {
    // The mock would panic if signing were reached (sign_verbatim unprogrammed
    // returns an Api error, not a panic) — but policy short-circuits before it.
    let cs = countersigner(&["a"], MockPki::new());

    let err = cs
        .countersign(&request_for(&["a", "rogue"]))
        .await
        .unwrap_err();
    assert!(matches!(err, CountersignError::Denied(d) if d.reason().contains("rogue")));
}

#[tokio::test]
async fn vault_failure_maps_to_backend() {
    let pki = MockPki::new().with_error(MockError::Denied);
    let cs = countersigner(&["a"], pki);

    let err = cs.countersign(&request_for(&["a"])).await.unwrap_err();
    assert!(matches!(err, CountersignError::Backend(_)));
}

#[tokio::test]
async fn undecodable_certificate_maps_to_cert_encoding() {
    let bad = "-----BEGIN CERTIFICATE-----\nnot!base64!\n-----END CERTIFICATE-----";
    let pki = MockPki::new().with_signed(pki_signed(bad, &[bad]));
    let cs = countersigner(&["a"], pki);

    let err = cs.countersign(&request_for(&["a"])).await.unwrap_err();
    assert!(matches!(err, CountersignError::CertEncoding(_)));
}
