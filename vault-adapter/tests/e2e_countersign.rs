//! End-to-end: the module generates a real keypair + CSR (private key stays in
//! the module), and the Relay's `VaultPkiCountersigner` countersigns that request
//! via the PKI backend — closing the trust-flow loop from step 2 to step 4. The
//! signing backend is `MockPki`, so no live Vault is needed. The full-flow test
//! extends this through steps 5–7: seal a token to the module's key, drive the
//! `Connection` typestate, and open it inside the module.

use module::{seal, ModuleIdentity};
use vault_adapter::{AllowlistPolicy, PkiBackend, VaultPkiCountersigner};
use vault_client_rs::{PkiOperations, PkiSignedCert, VaultError};
use vault_client_rs_test::mock::{pki_signed, MockPki};

use connections::vault::{AuthScope, Cn, Connection, SecretRef};
use secrecy::ExposeSecret;

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

#[tokio::test]
async fn full_trust_flow_from_csr_to_opened_token() {
    // Step 2 — module generates its identity + request.
    let (identity, request) = ModuleIdentity::generate(
        Cn::new("task:build"),
        AuthScope::from_cns([Cn::new("task:build")]),
    )
    .expect("module identity");
    let subject = request.subject.clone();
    let scope = request.scope.clone();

    // Steps 3–4 — the Relay authorizes and countersigns.
    let pki = MockPki::new().with_signed(pki_signed(SIGNED_PEM, &[SIGNED_PEM]));
    let policy = AllowlistPolicy::new(AuthScope::from_cns([Cn::new("task:build")]));
    let relay = VaultPkiCountersigner::new(policy, Backend { pki }, "pki-int", "cic-module");
    let cert = relay.countersign(&request).await.expect("countersigned");

    // Step 7 — a returned Vault token is sealed to the module's public key by the
    // host side, which never holds the private key.
    let token = "hvs.THE_ACTUAL_TOKEN";
    let sealed = seal(
        &identity.public_key_der().expect("pub"),
        SecretRef::new("secret", "cic/token"),
        token.as_bytes(),
    )
    .expect("seal");

    // The typestate carries it: Requested → Countersigned → Initialized.
    let connection = Connection::request(subject, scope)
        .countersign(cert)
        .initialize(sealed);

    // The module — the only private-key holder — opens the sealed token to use it.
    let opened = identity.open(connection.token()).expect("open");
    assert_eq!(opened.expose_secret(), token);
}
