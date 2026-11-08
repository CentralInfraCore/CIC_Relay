#![cfg(feature = "live-vault")]
//! Opt-in **live-Vault** end-to-end test of the full feature-008 trust-flow chain
//! against a real (throwaway) OpenBao/Vault dev server.
//!
//! It is a no-op unless `VAULT_LIVE_ADDR` is set, so it never runs in the default
//! unit/coverage/mutation builds. Run it via `make test-vault-live`, which spins a
//! throwaway `-dev-tls` server, provisions the PKI / cert-auth / KV mounts, and
//! points the `VAULT_LIVE_*` env vars at it.
//!
//! Chain exercised against the live server:
//! ```text
//! module CSR ──PKI sign-verbatim──► countersigned cert (Relay's key stays in Vault)
//!            ──mTLS cert-auth login──► scoped token
//!            ──read_sealed (KV)──► open inside the module ──► plaintext
//! ```
//! The KV secret is pre-sealed to the module's own key here (the request-time
//! A-version Vault-side wrapping is a deferred edge — see feature-008/spec.md).

use base64::Engine;
use connections::vault::{AuthScope, Cn, SecretRef};
use module::{seal, ModuleIdentity};
use p256::ecdsa::SigningKey;
use p256::elliptic_curve::Generate;
use p256::pkcs8::{EncodePrivateKey, LineEnding};
use secrecy::ExposeSecret;
use vault_adapter::{AllowlistPolicy, VaultPkiCountersigner, VaultSecretSource};
use vault_client_rs::VaultClient;

/// Connection + mount coordinates the launcher provides via env.
struct Config {
    addr: String,
    token: String,
    cacert: String,
    pki_mount: String,
    pki_role: String,
    cert_mount: String,
    cert_role: String,
    kv_mount: String,
    kv_path: String,
}

/// Reads the `VAULT_LIVE_*` env, or `None` (skip) if the server address is unset.
fn config() -> Option<Config> {
    let addr = std::env::var("VAULT_LIVE_ADDR").ok()?;
    let get = |k: &str, default: &str| std::env::var(k).unwrap_or_else(|_| default.to_owned());
    Some(Config {
        addr,
        token: get("VAULT_LIVE_TOKEN", "root"),
        cacert: std::env::var("VAULT_LIVE_CACERT").expect("VAULT_LIVE_CACERT must be set"),
        pki_mount: get("VAULT_LIVE_PKI_MOUNT", "cic-pki"),
        pki_role: get("VAULT_LIVE_PKI_ROLE", "cic-module"),
        cert_mount: get("VAULT_LIVE_CERT_MOUNT", "cic-cert"),
        cert_role: get("VAULT_LIVE_CERT_ROLE", "cic-module"),
        kv_mount: get("VAULT_LIVE_KV_MOUNT", "cic-kv"),
        kv_path: get("VAULT_LIVE_KV_PATH", "module-secret"),
    })
}

/// Wraps DER in a PEM CERTIFICATE block (for the mTLS client identity).
fn der_to_cert_pem(der: &[u8]) -> String {
    let b64 = base64::engine::general_purpose::STANDARD.encode(der);
    let mut out = String::from("-----BEGIN CERTIFICATE-----\n");
    for line in b64.as_bytes().chunks(64) {
        out.push_str(std::str::from_utf8(line).expect("base64 is ascii"));
        out.push('\n');
    }
    out.push_str("-----END CERTIFICATE-----\n");
    out
}

/// Builds an admin (token-auth) client verifying the dev server's CA.
fn admin_client(cfg: &Config, ca_pem: &[u8]) -> VaultClient {
    VaultClient::builder()
        .address(&cfg.addr)
        .token_str(&cfg.token)
        .ca_cert_pem(ca_pem.to_vec())
        .build()
        .expect("admin client")
}

#[tokio::test]
async fn full_trust_flow_chain_against_live_vault() {
    let Some(cfg) = config() else {
        eprintln!("[skip] VAULT_LIVE_ADDR unset — run via `make test-vault-live`");
        return;
    };
    let ca_pem = std::fs::read(&cfg.cacert).expect("read CA cert");

    // 1. The module holds a per-request P-256 key: its CSR signer, its mTLS client
    //    identity, and the recipient a returned secret is sealed to.
    let signing = SigningKey::generate();
    let key_pem = signing.to_pkcs8_pem(LineEnding::LF).expect("pkcs8 pem");
    let identity = ModuleIdentity::from_pkcs8_pem(&key_pem).expect("module identity");
    let subject = Cn::new("relay.worker");
    let scope = AuthScope::from_cns([Cn::new("db.read")]);

    // 2. Live countersign: module CSR → Vault PKI sign-verbatim. The Relay's
    //    intermediate signing key never leaves Vault.
    let request = identity
        .certificate_request(subject.clone(), scope.clone())
        .expect("csr");
    let countersigner = VaultPkiCountersigner::new(
        AllowlistPolicy::new(scope.clone()),
        admin_client(&cfg, &ca_pem),
        &cfg.pki_mount,
        &cfg.pki_role,
    );
    let cert = countersigner
        .countersign(&request)
        .await
        .expect("countersign");
    let cert_pem = der_to_cert_pem(cert.der());

    // 3. Seed KV: seal a secret to the module's key and store it (admin write).
    //    Stands in for the deferred request-time Vault-side wrap.
    let secret_value = "hvs.LIVE-CHAIN-SECRET";
    let secret_ref = SecretRef::new(&cfg.kv_mount, &cfg.kv_path);
    let sealed = seal(
        &identity.public_key_der().expect("pubkey"),
        secret_ref.clone(),
        secret_value.as_bytes(),
    )
    .expect("seal");
    let b64 = base64::engine::general_purpose::STANDARD.encode(sealed.ciphertext());
    admin_client(&cfg, &ca_pem)
        .kv2(&cfg.kv_mount)
        .write(&cfg.kv_path, &serde_json::json!({ "sealed": b64 }))
        .await
        .expect("kv seed write");

    // 4. mTLS cert-auth login: present the countersigned cert + module key in the
    //    TLS handshake → a scoped token gated by the cert's authorization.
    let source = VaultSecretSource::connect_mtls(
        &cfg.addr,
        &ca_pem,
        cert_pem.as_bytes(),
        key_pem.as_bytes(),
    )
    .expect("mtls client");
    let token = source
        .login(&cfg.cert_mount, Some(&cfg.cert_role))
        .await
        .expect("cert-auth login");
    assert!(
        !token.policies.is_empty(),
        "the countersigned identity must receive a scoped policy"
    );

    // 5. read_sealed with the scoped token, then open inside the module.
    let read_back = source.read_sealed(&secret_ref).await.expect("read_sealed");
    let opened = identity.open(&read_back).expect("open");
    assert_eq!(
        opened.expose_secret(),
        secret_value,
        "the opened plaintext must round-trip the sealed secret"
    );

    eprintln!("[ok] live chain: countersign → mTLS cert-auth login → read_sealed → open");
}
