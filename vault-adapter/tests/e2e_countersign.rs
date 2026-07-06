//! End-to-end: the module generates a real keypair + CSR (private key stays in
//! the module), and the Relay's `VaultPkiCountersigner` countersigns that request
//! via the PKI backend — closing the trust-flow loop from step 2 to step 4. The
//! signing backend is `MockPki`, so no live Vault is needed.

use module::ModuleIdentity;
use vault_adapter::{AllowlistPolicy, PkiBackend, VaultPkiCountersigner};
use vault_client_rs::{PkiOperations, PkiSignedCert, VaultError};
use vault_client_rs_test::mock::{pki_signed, MockPki};

use connections::vault::{AuthScope, Cn};

/// A PEM whose body base64-decodes to `COUNTERSIGNEDCERT`.
const SIGNED_PEM: &str =
    "-----BEGIN CERTIFICATE-----\nQ09VTlRFUlNJR05FRENFUlQ=\n-----END CERTIFICATE-----";

struct Backend {
    pki: MockPki,
}

impl PkiBackend for Backend {
    async fn sign_verbatim(
        &self,
        _mount: &str,
        role: &str,
        csr: &str,
    ) -> Result<PkiSignedCert, VaultError> {
        self.pki.sign_verbatim(role, csr).await
    }
}

#[tokio::test]
async fn module_generates_csr_and_relay_countersigns_it() {
    // Step 2 — the module generates its keypair + CSR; the private key never
    // leaves `ModuleIdentity`, only the request (subject + scope + CSR) is emitted.
    let (identity, request) = ModuleIdentity::generate(
        Cn::new("task:build"),
        AuthScope::from_cns([Cn::new("task:build")]),
    )
    .expect("module identity");
    assert!(!request.csr_der.is_empty());
    assert!(!identity.public_key_der().expect("pub").is_empty());

    // Step 4 — the Relay authorizes the scope and has Vault PKI countersign.
    let pki = MockPki::new().with_signed(pki_signed(SIGNED_PEM, &[SIGNED_PEM]));
    let policy = AllowlistPolicy::new(AuthScope::from_cns([Cn::new("task:build")]));
    let relay = VaultPkiCountersigner::new(policy, Backend { pki }, "pki-int", "cic-module");

    let cert = relay.countersign(&request).await.expect("countersigned");
    assert_eq!(cert.der(), b"COUNTERSIGNEDCERT");
    assert!(cert.scope().contains(&Cn::new("task:build")));
}
