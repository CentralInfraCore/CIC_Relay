//! Live async HTTP adapter behind the [`executor`] port (feature-008; roadmap
//! M43 seed).
//!
//! Mirrors how `vault-adapter` sits behind `connections`: the sync [`Executor`]
//! *contract* in the `executor` core declares *what* an actuation is; this crate
//! performs it *how* — over the network with `reqwest`, under a minimal egress
//! policy. The opened credential (a [`secrecy::SecretString`], as `module::open`
//! yields) is applied as a Bearer `Authorization` header and is never logged.
//!
//! [`HttpExecutor::execute`] is the **live async counterpart** of the sync
//! [`Executor::execute`] contract: a *reached* endpoint yields
//! `Ok(`[`ExecOutcome`]`)` for any status; an [`ExecuteError`] means the target
//! was malformed, the egress policy refused it, or the endpoint was never reached.
//!
//! [`Executor`]: executor::Executor
//! [`Executor::execute`]: executor::Executor::execute

use std::collections::BTreeMap;
use std::collections::BTreeSet;

use executor::{ExecOutcome, ExecRequest, ExecuteError, Method};
use reqwest::Client;
use secrecy::{ExposeSecret, SecretString};

/// A minimal egress policy (roadmap M43 seed): allow only an explicit set of
/// hosts, and — in production shape — only over `https`.
///
/// Host matching is exact and case-insensitive. Wildcards are not supported (a
/// deliberate M43 invariant). An IP literal is only reachable if it is named
/// explicitly in the allowlist, matching "IP literal tiltott alapból, explicit
/// allowlisttel engedett".
#[derive(Debug, Clone)]
pub struct EgressPolicy {
    allowed_hosts: BTreeSet<String>,
    require_https: bool,
}

impl EgressPolicy {
    /// Allows exactly `hosts` (exact, lowercase match) over **https only**. This
    /// is the production shape.
    #[must_use]
    pub fn allowing(hosts: impl IntoIterator<Item = impl Into<String>>) -> Self {
        Self {
            allowed_hosts: hosts.into_iter().map(|h| h.into().to_lowercase()).collect(),
            require_https: true,
        }
    }

    /// Like [`allowing`](Self::allowing) but also permits plain `http` — intended
    /// for loopback/testing against a local mock server, never for production
    /// egress.
    #[must_use]
    pub fn insecure_allowing(hosts: impl IntoIterator<Item = impl Into<String>>) -> Self {
        Self {
            require_https: false,
            ..Self::allowing(hosts)
        }
    }

    /// Validates `target`, returning the parsed URL or the reason it is refused.
    fn check(&self, target: &str) -> Result<url::Url, ExecuteError> {
        if target.is_empty() {
            return Err(ExecuteError::InvalidRequest("empty target".to_owned()));
        }
        let url = url::Url::parse(target)
            .map_err(|e| ExecuteError::InvalidRequest(format!("unparseable target: {e}")))?;

        if self.require_https && url.scheme() != "https" {
            return Err(ExecuteError::Denied(format!(
                "scheme not allowed: {}",
                url.scheme()
            )));
        }
        let host = url
            .host_str()
            .ok_or_else(|| ExecuteError::InvalidRequest("target has no host".to_owned()))?;
        if !self.allowed_hosts.contains(&host.to_lowercase()) {
            return Err(ExecuteError::Denied(format!("host not allowed: {host}")));
        }
        Ok(url)
    }
}

/// A live HTTP actuator: the network implementation of the [`executor`] port.
///
/// [`executor`]: executor
pub struct HttpExecutor {
    client: Client,
    policy: EgressPolicy,
}

impl HttpExecutor {
    /// Builds an executor bound to `policy`, using a `rustls` client that does
    /// **not** auto-follow redirects (an M43 invariant: a `3xx` is returned to
    /// the caller, not silently chased to a new host).
    ///
    /// # Errors
    /// [`ExecuteError::Unavailable`] if the underlying HTTP client cannot be built.
    pub fn new(policy: EgressPolicy) -> Result<Self, ExecuteError> {
        let client = Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| ExecuteError::Unavailable("http client build failed".to_owned()))?;
        Ok(Self { client, policy })
    }

    /// Performs `request` over the network, applying `secret` as a Bearer
    /// `Authorization` header. The live async counterpart of the [`Executor`]
    /// port contract.
    ///
    /// A reached endpoint yields `Ok(`[`ExecOutcome`]`)` for **any** status.
    ///
    /// # Errors
    /// [`ExecuteError::InvalidRequest`] if the target is malformed;
    /// [`ExecuteError::Denied`] if the egress policy refuses it (non-https or a
    /// host outside the allowlist); [`ExecuteError::Unavailable`] if the endpoint
    /// is not reached or its response cannot be read. No error carries the secret.
    ///
    /// [`Executor`]: executor::Executor
    pub async fn execute(
        &self,
        request: &ExecRequest,
        secret: &SecretString,
    ) -> Result<ExecOutcome, ExecuteError> {
        let url = self.policy.check(&request.target)?;

        let mut builder = self.client.request(to_reqwest_method(request.method), url);
        for (name, value) in &request.headers {
            builder = builder.header(name, value);
        }
        // The single point where the opened credential is applied. reqwest keeps
        // the header out of its own Debug, and this crate never logs the request.
        builder = builder.bearer_auth(secret.expose_secret());
        if !request.body.is_empty() {
            builder = builder.body(request.body.clone());
        }

        // Coarse, non-secret error mapping: a reqwest error's Display can include
        // the URL, so we never surface it — the boundary redacts by default.
        let response = builder
            .send()
            .await
            .map_err(|_| ExecuteError::Unavailable("endpoint not reached".to_owned()))?;

        let status = response.status().as_u16();
        let headers = response
            .headers()
            .iter()
            .filter_map(|(name, value)| {
                value
                    .to_str()
                    .ok()
                    .map(|v| (name.as_str().to_owned(), v.to_owned()))
            })
            .collect::<BTreeMap<String, String>>();
        let body = response
            .bytes()
            .await
            .map_err(|_| ExecuteError::Unavailable("response body unreadable".to_owned()))?
            .to_vec();

        Ok(ExecOutcome {
            status,
            headers,
            body,
        })
    }
}

/// Maps the port's closed [`Method`] set onto `reqwest`'s.
fn to_reqwest_method(method: Method) -> reqwest::Method {
    match method {
        Method::Get => reqwest::Method::GET,
        Method::Post => reqwest::Method::POST,
        Method::Put => reqwest::Method::PUT,
        Method::Delete => reqwest::Method::DELETE,
    }
}

#[cfg(test)]
mod tests {
    use super::{to_reqwest_method, EgressPolicy};
    use executor::{ExecuteError, Method};

    #[test]
    fn method_maps_onto_reqwest() {
        assert_eq!(to_reqwest_method(Method::Get), reqwest::Method::GET);
        assert_eq!(to_reqwest_method(Method::Post), reqwest::Method::POST);
        assert_eq!(to_reqwest_method(Method::Put), reqwest::Method::PUT);
        assert_eq!(to_reqwest_method(Method::Delete), reqwest::Method::DELETE);
    }

    #[test]
    fn https_policy_denies_plain_http() {
        let policy = EgressPolicy::allowing(["api.example"]);
        let err = policy.check("http://api.example/x").unwrap_err();
        assert!(matches!(err, ExecuteError::Denied(m) if m.contains("scheme")));
    }

    #[test]
    fn policy_denies_host_outside_allowlist() {
        let policy = EgressPolicy::allowing(["api.example"]);
        let err = policy.check("https://evil.example/x").unwrap_err();
        assert!(matches!(err, ExecuteError::Denied(m) if m.contains("evil.example")));
    }

    #[test]
    fn policy_allows_allowlisted_https_host_case_insensitively() {
        let policy = EgressPolicy::allowing(["API.Example"]);
        let url = policy
            .check("https://api.example/path")
            .expect("allowlisted host passes");
        assert_eq!(url.host_str(), Some("api.example"));
    }

    #[test]
    fn policy_rejects_empty_and_malformed_targets() {
        let policy = EgressPolicy::allowing(["api.example"]);
        assert!(matches!(
            policy.check(""),
            Err(ExecuteError::InvalidRequest(_))
        ));
        assert!(matches!(
            policy.check("not a url"),
            Err(ExecuteError::InvalidRequest(_))
        ));
    }

    #[test]
    fn insecure_policy_permits_http_for_loopback() {
        let policy = EgressPolicy::insecure_allowing(["127.0.0.1"]);
        let url = policy
            .check("http://127.0.0.1:8080/ping")
            .expect("insecure policy permits loopback http");
        assert_eq!(url.host_str(), Some("127.0.0.1"));
    }
}
