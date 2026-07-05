//! Vault Connection object model — the core "meaning" layer of feature-008's
//! capability-based secret flow.
//!
//! This module owns the *meaning* of a Vault connection, not its *access*: it
//! carries no cryptography and talks to no Vault. Instead it models the flow as
//! a typestate so the ordering is enforced at compile time —
//! `request → countersign → initialize` — and it keeps secret material in an
//! opaque, redaction-only [`SealedSecret`] that has no plaintext field and can
//! never leak into logs.
//!
//! The layering (feature-008): **core owns meaning** (this module),
//! **Vault lib owns access** (a separate driver), **adapter owns translation**,
//! **runtime owns execution**.

use std::collections::BTreeSet;
use std::fmt;

/// A single X.509 Common Name.
///
/// In feature-008 the set of `Cn`s on a request-Cert is what encodes
/// content-dependent authorization: the Relay countersigns a chosen subset, and
/// Vault maps those to a policy. See [`AuthScope`].
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Cn(String);

impl Cn {
    /// Creates a `Cn` from any string-like value.
    #[must_use]
    pub fn new(value: impl Into<String>) -> Self {
        Self(value.into())
    }

    /// Returns the `Cn` as a string slice.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for Cn {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

/// The set of [`Cn`]s that encodes content-dependent authorization for a
/// connection request.
///
/// Backed by a [`BTreeSet`] so iteration order is deterministic and duplicates
/// collapse — a prerequisite for any hash or `ProofTrace` over the request.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct AuthScope {
    cns: BTreeSet<Cn>,
}

impl AuthScope {
    /// Creates an empty scope.
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Builds a scope from an iterator of [`Cn`]s, deduplicating and sorting.
    #[must_use]
    pub fn from_cns(cns: impl IntoIterator<Item = Cn>) -> Self {
        Self {
            cns: cns.into_iter().collect(),
        }
    }

    /// Adds `cn` to the scope; returns `true` if it was newly inserted.
    pub fn insert(&mut self, cn: Cn) -> bool {
        self.cns.insert(cn)
    }

    /// Returns `true` if `cn` is part of this scope.
    #[must_use]
    pub fn contains(&self, cn: &Cn) -> bool {
        self.cns.contains(cn)
    }

    /// Number of distinct [`Cn`]s in the scope.
    #[must_use]
    pub fn len(&self) -> usize {
        self.cns.len()
    }

    /// Returns `true` if the scope contains no [`Cn`]s.
    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.cns.is_empty()
    }

    /// Iterates the [`Cn`]s in deterministic (sorted) order.
    pub fn cns(&self) -> impl Iterator<Item = &Cn> {
        self.cns.iter()
    }
}

/// A stable reference to a secret in a Vault backend, independent of its value.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct SecretRef {
    /// The Vault mount point (e.g. `"secret"`).
    pub mount: String,
    /// The path within the mount.
    pub path: String,
    /// An optional explicit version; `None` means "latest".
    pub version: Option<u64>,
}

impl SecretRef {
    /// Creates a reference to the latest version of `path` under `mount`.
    #[must_use]
    pub fn new(mount: impl Into<String>, path: impl Into<String>) -> Self {
        Self {
            mount: mount.into(),
            path: path.into(),
            version: None,
        }
    }

    /// Pins this reference to an explicit `version`.
    #[must_use]
    pub fn with_version(mut self, version: u64) -> Self {
        self.version = Some(version);
        self
    }
}

impl fmt::Display for SecretRef {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self.version {
            Some(v) => write!(f, "{}/{}@{v}", self.mount, self.path),
            None => write!(f, "{}/{}", self.mount, self.path),
        }
    }
}

/// A request-Cert countersigned by the Relay.
///
/// This is public material — it carries the requester's public key and the
/// authorized [`AuthScope`], not any secret — so it is safe to clone and print.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CounterSignedCert {
    der: Vec<u8>,
    scope: AuthScope,
}

impl CounterSignedCert {
    /// Wraps the DER-encoded countersigned certificate together with the
    /// [`AuthScope`] the Relay actually authorized.
    #[must_use]
    pub fn new(der: Vec<u8>, scope: AuthScope) -> Self {
        Self { der, scope }
    }

    /// The DER-encoded certificate bytes.
    #[must_use]
    pub fn der(&self) -> &[u8] {
        &self.der
    }

    /// The authorization scope the Relay countersigned.
    #[must_use]
    pub fn scope(&self) -> &AuthScope {
        &self.scope
    }
}

/// A secret/token as returned by Vault, **sealed to the request's public key**.
///
/// The core invariant of feature-008: this type holds only ciphertext — the
/// plaintext never exists here, and only the module holding the matching private
/// key can open it (elsewhere, in the runtime). [`Debug`] and [`Display`] are
/// redacted, so a sealed secret can never leak into logs or traces. The exposed
/// [`ciphertext`](SealedSecret::ciphertext) is safe precisely because it is
/// encrypted and cannot be opened here.
#[derive(Clone, PartialEq, Eq)]
pub struct SealedSecret {
    secret_ref: SecretRef,
    ciphertext: Vec<u8>,
}

impl SealedSecret {
    /// Wraps `ciphertext` that has already been sealed to the requester's public
    /// key, tagged with the [`SecretRef`] it came from.
    #[must_use]
    pub fn new(secret_ref: SecretRef, ciphertext: Vec<u8>) -> Self {
        Self {
            secret_ref,
            ciphertext,
        }
    }

    /// The reference this sealed secret was read from.
    #[must_use]
    pub fn secret_ref(&self) -> &SecretRef {
        &self.secret_ref
    }

    /// The sealed ciphertext bytes. Safe to expose: it is encrypted to the
    /// requester's public key and cannot be opened here.
    #[must_use]
    pub fn ciphertext(&self) -> &[u8] {
        &self.ciphertext
    }
}

impl fmt::Debug for SealedSecret {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("SealedSecret")
            .field("secret_ref", &self.secret_ref)
            .field("ciphertext", &"<redacted>")
            .finish()
    }
}

impl fmt::Display for SealedSecret {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "SealedSecret({}, <redacted>)", self.secret_ref)
    }
}

/// Typestate: the connection has been requested — the module generated a Cert and
/// is awaiting the Relay's countersignature. The private key stays with the module.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Requested;

/// Typestate: the Relay has countersigned the request-Cert.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Countersigned {
    cert: CounterSignedCert,
}

/// Typestate: the connection is initialized and holds the sealed Vault token.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Initialized {
    cert: CounterSignedCert,
    token: SealedSecret,
}

/// A Vault Connection, parameterized by its lifecycle state `S`.
///
/// The typestate makes the feature-008 flow order a compile-time guarantee: a
/// token can only be reached via `request → countersign → initialize`, and the
/// token is always a [`SealedSecret`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Connection<S> {
    subject: Cn,
    scope: AuthScope,
    state: S,
}

impl<S> Connection<S> {
    /// The subject [`Cn`] this connection was requested for.
    #[must_use]
    pub fn subject(&self) -> &Cn {
        &self.subject
    }

    /// The [`AuthScope`] requested for this connection.
    #[must_use]
    pub fn scope(&self) -> &AuthScope {
        &self.scope
    }
}

impl Connection<Requested> {
    /// Begins a connection request for `subject`, asking for authorization
    /// `scope`. Models step 1–2 of the flow: the module has generated its Cert;
    /// the private key never leaves the module.
    #[must_use]
    pub fn request(subject: Cn, scope: AuthScope) -> Self {
        Self {
            subject,
            scope,
            state: Requested,
        }
    }

    /// Consumes the Relay's countersigned Cert, advancing to
    /// [`Countersigned`]. Models step 3–4: the Relay's content-dependent
    /// authorization.
    #[must_use]
    pub fn countersign(self, cert: CounterSignedCert) -> Connection<Countersigned> {
        Connection {
            subject: self.subject,
            scope: self.scope,
            state: Countersigned { cert },
        }
    }
}

impl Connection<Countersigned> {
    /// The countersigned Cert presented to Vault for cert-auth.
    #[must_use]
    pub fn cert(&self) -> &CounterSignedCert {
        &self.state.cert
    }

    /// Initializes the connection with the sealed token Vault returned, advancing
    /// to [`Initialized`]. Models step 6–7: `vault obj init`.
    #[must_use]
    pub fn initialize(self, token: SealedSecret) -> Connection<Initialized> {
        Connection {
            subject: self.subject,
            scope: self.scope,
            state: Initialized {
                cert: self.state.cert,
                token,
            },
        }
    }
}

impl Connection<Initialized> {
    /// The countersigned Cert this connection authenticated with.
    #[must_use]
    pub fn cert(&self) -> &CounterSignedCert {
        &self.state.cert
    }

    /// The sealed Vault token. Always sealed — the plaintext is never exposed
    /// here; it is opened only inside the module that holds the private key.
    #[must_use]
    pub fn token(&self) -> &SealedSecret {
        &self.state.token
    }
}

#[cfg(test)]
mod tests {
    use super::{AuthScope, Cn, Connection, CounterSignedCert, SealedSecret, SecretRef};

    fn scope(cns: &[&str]) -> AuthScope {
        AuthScope::from_cns(cns.iter().map(|c| Cn::new(*c)))
    }

    #[test]
    fn cn_roundtrips_and_displays() {
        let cn = Cn::new("relay.worker.build");
        assert_eq!(cn.as_str(), "relay.worker.build");
        assert_eq!(cn.to_string(), "relay.worker.build");
    }

    #[test]
    fn authscope_dedups_sorts_and_queries() {
        let mut s = AuthScope::new();
        assert!(s.is_empty());
        assert_eq!(s.len(), 0);

        assert!(s.insert(Cn::new("b")));
        assert!(s.insert(Cn::new("a")));
        assert!(!s.insert(Cn::new("a"))); // duplicate

        assert!(!s.is_empty());
        assert_eq!(s.len(), 2);
        assert!(s.contains(&Cn::new("a")));
        assert!(!s.contains(&Cn::new("c")));

        let ordered: Vec<_> = s.cns().map(Cn::as_str).collect();
        assert_eq!(ordered, ["a", "b"]); // deterministic order
    }

    #[test]
    fn secretref_builds_and_displays() {
        let latest = SecretRef::new("secret", "db/creds");
        assert_eq!(latest.version, None);
        assert_eq!(latest.to_string(), "secret/db/creds");

        let pinned = SecretRef::new("secret", "db/creds").with_version(7);
        assert_eq!(pinned.version, Some(7));
        assert_eq!(pinned.to_string(), "secret/db/creds@7");
    }

    #[test]
    fn countersigned_cert_exposes_der_and_scope() {
        let cert = CounterSignedCert::new(vec![0x30, 0x82], scope(&["x"]));
        assert_eq!(cert.der(), &[0x30, 0x82]);
        assert!(cert.scope().contains(&Cn::new("x")));
    }

    #[test]
    fn sealed_secret_never_reveals_ciphertext_when_formatted() {
        let secret = SealedSecret::new(SecretRef::new("secret", "token"), b"PLAINTEXTish".to_vec());
        assert_eq!(secret.ciphertext(), b"PLAINTEXTish");
        assert_eq!(secret.secret_ref(), &SecretRef::new("secret", "token"));

        let dbg = format!("{secret:?}");
        let disp = format!("{secret}");
        assert!(dbg.contains("<redacted>"));
        assert!(!dbg.contains("PLAINTEXTish"));
        assert!(disp.contains("<redacted>"));
        assert!(!disp.contains("PLAINTEXTish"));
        assert!(disp.contains("secret/token")); // ref is not secret
    }

    #[test]
    fn connection_typestate_flows_request_to_initialized() {
        let subject = Cn::new("relay.self");
        let sc = scope(&["relay.self", "db.read"]);

        let requested = Connection::request(subject.clone(), sc.clone());
        assert_eq!(requested.subject(), &subject);
        assert_eq!(requested.scope(), &sc);

        let cert = CounterSignedCert::new(vec![1, 2, 3], sc.clone());
        let countersigned = requested.countersign(cert.clone());
        assert_eq!(countersigned.cert(), &cert);
        assert_eq!(countersigned.subject(), &subject); // preserved across transition

        let token = SealedSecret::new(SecretRef::new("secret", "token"), vec![9, 9]);
        let initialized = countersigned.initialize(token.clone());
        assert_eq!(initialized.token(), &token);
        assert_eq!(initialized.cert(), &cert);
        assert_eq!(initialized.scope(), &sc); // preserved end-to-end
    }
}
