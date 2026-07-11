//! Live async HTTP adapter behind the [`executor`] port (feature-008; roadmap
//! M43/M44 seeds).
//!
//! Mirrors how `vault-adapter` sits behind `connections`: the sync [`Executor`]
//! *contract* in the `executor` core declares *what* an actuation is; this crate
//! performs it *how* — over the network with `reqwest`, under a minimal egress
//! policy and hard limits. The opened credential (a [`secrecy::SecretString`], as
//! `module::open` yields) is applied as a Bearer `Authorization` header and is
//! never logged.
//!
//! [`HttpExecutor::execute`] is the **live async counterpart** of the sync
//! [`Executor::execute`] contract: a *reached* endpoint yields
//! `Ok(`[`ExecOutcome`]`)` for any status; an [`ExecuteError`] means the target
//! was malformed, the egress policy refused it, a limit was exceeded, or the
//! endpoint was never reached.
//!
//! Security posture (see `features/feature-008/http-executor-hardening.md`):
//! request/connect timeouts, a response-body size cap, ambient proxy env ignored
//! (`no_proxy`), and an SSRF guard denying private/link-local/loopback ranges —
//! both for IP-literal targets (at policy time) and for resolved hostnames (via a
//! custom [`reqwest::dns::Resolve`] on the connect path).
//!
//! [`Executor`]: executor::Executor
//! [`Executor::execute`]: executor::Executor::execute

use std::collections::{BTreeMap, BTreeSet};
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr, ToSocketAddrs};
use std::sync::Arc;
use std::time::Duration;

use executor::{ExecOutcome, ExecRequest, ExecuteError, Method};
use reqwest::Client;
use secrecy::{ExposeSecret, SecretString};

/// Hard limits applied to every actuation (roadmap M44 seed).
#[derive(Debug, Clone)]
pub struct Limits {
    /// Total time budget for a request, end to end.
    pub request_timeout: Duration,
    /// Time budget for establishing the connection.
    pub connect_timeout: Duration,
    /// Maximum response-body size read into memory; a larger body is refused with
    /// [`ExecuteError::LimitExceeded`] rather than read.
    pub max_response_bytes: usize,
}

impl Default for Limits {
    fn default() -> Self {
        Self {
            request_timeout: Duration::from_secs(30),
            connect_timeout: Duration::from_secs(10),
            max_response_bytes: 1 << 20, // 1 MiB
        }
    }
}

/// A minimal egress policy (roadmap M43 seed): allow only an explicit set of
/// hosts, and — in production shape — only over `https`, with private/loopback/
/// link-local IP ranges denied.
///
/// Host matching is exact and case-insensitive; a trailing dot is normalized
/// away. Wildcards are not supported (a deliberate M43 invariant). An IP literal
/// is reachable only if named explicitly in the allowlist **and** outside the
/// blocked ranges (unless [`insecure_allowing`](Self::insecure_allowing) permits
/// private IPs for loopback testing).
#[derive(Debug, Clone)]
pub struct EgressPolicy {
    allowed_hosts: BTreeSet<String>,
    require_https: bool,
    allow_private_ips: bool,
}

impl EgressPolicy {
    /// Allows exactly `hosts` (exact, lowercase match) over **https only**, with
    /// private/loopback/link-local IP ranges denied. This is the production shape.
    #[must_use]
    pub fn allowing(hosts: impl IntoIterator<Item = impl Into<String>>) -> Self {
        Self {
            allowed_hosts: hosts
                .into_iter()
                .map(|h| normalize_host(&h.into()))
                .collect(),
            require_https: true,
            allow_private_ips: false,
        }
    }

    /// Like [`allowing`](Self::allowing) but also permits plain `http` **and**
    /// private/loopback IP ranges — intended for loopback/testing against a local
    /// mock server, never for production egress.
    #[must_use]
    pub fn insecure_allowing(hosts: impl IntoIterator<Item = impl Into<String>>) -> Self {
        Self {
            require_https: false,
            allow_private_ips: true,
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

        let (host_key, ip) = match url.host() {
            Some(url::Host::Domain(d)) => (normalize_host(d), None),
            Some(url::Host::Ipv4(v4)) => (v4.to_string(), Some(IpAddr::V4(v4))),
            Some(url::Host::Ipv6(v6)) => (v6.to_string(), Some(IpAddr::V6(v6))),
            None => {
                return Err(ExecuteError::InvalidRequest(
                    "target has no host".to_owned(),
                ));
            }
        };

        if !self.allowed_hosts.contains(&host_key) {
            return Err(ExecuteError::Denied(format!(
                "host not allowed: {host_key}"
            )));
        }
        // Defense in depth: even an allowlisted IP literal is refused if it falls
        // in a blocked range (unless the loopback/testing escape is set).
        if let Some(ip) = ip {
            if !self.allow_private_ips && is_blocked_ip(ip) {
                return Err(ExecuteError::Denied(format!("blocked ip range: {ip}")));
            }
        }
        Ok(url)
    }
}

/// Lowercases a host and strips a single trailing dot (`api.example.` ==
/// `api.example`), so allowlist entries and targets normalize identically.
fn normalize_host(host: &str) -> String {
    host.strip_suffix('.').unwrap_or(host).to_lowercase()
}

/// Returns `true` if `ip` is in a private, loopback, link-local, unspecified or
/// broadcast range that an actuation must never reach (SSRF defense). IPv4-mapped
/// IPv6 addresses are unwrapped and judged as IPv4.
fn is_blocked_ip(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(v4) => is_blocked_v4(v4),
        IpAddr::V6(v6) => {
            if let Some(mapped) = v6.to_ipv4_mapped() {
                return is_blocked_v4(mapped);
            }
            v6.is_loopback()
                || v6.is_unspecified()
                || is_unique_local_v6(v6)
                || is_link_local_v6(v6)
        }
    }
}

fn is_blocked_v4(v4: Ipv4Addr) -> bool {
    v4.is_loopback()
        || v4.is_private()
        || v4.is_link_local()
        || v4.is_unspecified()
        || v4.is_broadcast()
}

/// `fc00::/7` — IPv6 unique local addresses.
fn is_unique_local_v6(v6: Ipv6Addr) -> bool {
    (v6.segments()[0] & 0xfe00) == 0xfc00
}

/// `fe80::/10` — IPv6 unicast link-local addresses.
fn is_link_local_v6(v6: Ipv6Addr) -> bool {
    (v6.segments()[0] & 0xffc0) == 0xfe80
}

/// A DNS resolver that filters out addresses in the blocked SSRF ranges. Because
/// reqwest calls it on the connect path, a hostname resolving to a private
/// address cannot be dialed (no resolve-then-connect TOCTOU for dialed addrs).
struct GuardedResolver {
    allow_private: bool,
}

impl reqwest::dns::Resolve for GuardedResolver {
    fn resolve(&self, name: reqwest::dns::Name) -> reqwest::dns::Resolving {
        let allow_private = self.allow_private;
        let host = name.as_str().to_owned();
        Box::pin(async move {
            let resolved =
                tokio::task::spawn_blocking(move || (host.as_str(), 0u16).to_socket_addrs())
                    .await
                    .map_err(|e| Box::new(e) as Box<dyn std::error::Error + Send + Sync>)?
                    .map_err(|e| Box::new(e) as Box<dyn std::error::Error + Send + Sync>)?;

            let allowed: Vec<SocketAddr> = resolved
                .filter(|addr| allow_private || !is_blocked_ip(addr.ip()))
                .collect();
            if allowed.is_empty() {
                return Err("all resolved addresses are blocked by egress policy".into());
            }
            Ok(Box::new(allowed.into_iter()) as reqwest::dns::Addrs)
        })
    }
}

/// A live HTTP actuator: the network implementation of the [`executor`] port.
///
/// [`executor`]: executor
pub struct HttpExecutor {
    client: Client,
    policy: EgressPolicy,
    limits: Limits,
}

impl HttpExecutor {
    /// Builds an executor bound to `policy` with default [`Limits`].
    ///
    /// # Errors
    /// [`ExecuteError::Unavailable`] if the underlying HTTP client cannot be built.
    pub fn new(policy: EgressPolicy) -> Result<Self, ExecuteError> {
        Self::with_limits(policy, Limits::default())
    }

    /// Builds an executor bound to `policy` and explicit `limits`, using a
    /// `rustls` client that ignores ambient proxy env (`no_proxy`), does **not**
    /// auto-follow redirects (an M43 invariant), enforces the timeouts, and dials
    /// only through the SSRF-guarding resolver.
    ///
    /// # Errors
    /// [`ExecuteError::Unavailable`] if the underlying HTTP client cannot be built.
    pub fn with_limits(policy: EgressPolicy, limits: Limits) -> Result<Self, ExecuteError> {
        let client = Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .no_proxy()
            .timeout(limits.request_timeout)
            .connect_timeout(limits.connect_timeout)
            .dns_resolver(Arc::new(GuardedResolver {
                allow_private: policy.allow_private_ips,
            }))
            .build()
            .map_err(|_| ExecuteError::Unavailable("http client build failed".to_owned()))?;
        Ok(Self {
            client,
            policy,
            limits,
        })
    }

    /// Performs `request` over the network, applying `secret` as a Bearer
    /// `Authorization` header. The live async counterpart of the [`Executor`]
    /// port contract.
    ///
    /// A reached endpoint yields `Ok(`[`ExecOutcome`]`)` for **any** status.
    ///
    /// # Errors
    /// [`ExecuteError::InvalidRequest`] if the target is malformed;
    /// [`ExecuteError::Denied`] if the egress policy refuses it (non-https, a host
    /// outside the allowlist, or a blocked IP literal);
    /// [`ExecuteError::LimitExceeded`] if the response body exceeds the cap;
    /// [`ExecuteError::Unavailable`] if the endpoint is not reached (timeout,
    /// connect failure, blocked resolution) or its body cannot be read. No error
    /// carries the secret.
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
        let mut response = builder
            .send()
            .await
            .map_err(|_| ExecuteError::Unavailable("endpoint not reached".to_owned()))?;

        // Refuse an oversized body up front if the endpoint declares one.
        if let Some(len) = response.content_length() {
            if len > self.limits.max_response_bytes as u64 {
                return Err(ExecuteError::LimitExceeded(format!(
                    "response exceeds {} bytes",
                    self.limits.max_response_bytes
                )));
            }
        }

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

        // Read incrementally with a hard cap so a lying/absent Content-Length
        // cannot force an unbounded read.
        let mut body = Vec::new();
        while let Some(chunk) = response
            .chunk()
            .await
            .map_err(|_| ExecuteError::Unavailable("response body unreadable".to_owned()))?
        {
            if body.len() + chunk.len() > self.limits.max_response_bytes {
                return Err(ExecuteError::LimitExceeded(format!(
                    "response exceeds {} bytes",
                    self.limits.max_response_bytes
                )));
            }
            body.extend_from_slice(&chunk);
        }

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
    use super::{is_blocked_ip, normalize_host, to_reqwest_method, EgressPolicy, Limits};
    use executor::{ExecuteError, Method};
    use std::net::IpAddr;
    use std::time::Duration;

    fn ip(s: &str) -> IpAddr {
        s.parse().expect("valid ip")
    }

    #[test]
    fn method_maps_onto_reqwest() {
        assert_eq!(to_reqwest_method(Method::Get), reqwest::Method::GET);
        assert_eq!(to_reqwest_method(Method::Post), reqwest::Method::POST);
        assert_eq!(to_reqwest_method(Method::Put), reqwest::Method::PUT);
        assert_eq!(to_reqwest_method(Method::Delete), reqwest::Method::DELETE);
    }

    #[test]
    fn default_limits_are_conservative() {
        let l = Limits::default();
        assert_eq!(l.request_timeout, Duration::from_secs(30));
        assert_eq!(l.connect_timeout, Duration::from_secs(10));
        assert_eq!(l.max_response_bytes, 1 << 20);
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
    fn policy_allows_allowlisted_https_host_case_and_trailing_dot_insensitively() {
        let policy = EgressPolicy::allowing(["API.Example"]);
        // Case and a trailing dot normalize to the same allowlist key, so the
        // request passes (the parsed URL may still carry the dot verbatim).
        assert!(policy.check("https://api.example./path").is_ok());
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
    fn production_policy_denies_blocked_ip_literal_even_if_allowlisted() {
        // An operator allowlists a loopback IP in a production (https) policy; the
        // SSRF range check still refuses it.
        let policy = EgressPolicy::allowing(["127.0.0.1"]);
        let err = policy.check("https://127.0.0.1/x").unwrap_err();
        assert!(matches!(err, ExecuteError::Denied(m) if m.contains("blocked ip")));
    }

    #[test]
    fn insecure_policy_permits_loopback_http() {
        let policy = EgressPolicy::insecure_allowing(["127.0.0.1"]);
        let url = policy
            .check("http://127.0.0.1:8080/ping")
            .expect("insecure policy permits loopback http");
        assert_eq!(url.host_str(), Some("127.0.0.1"));
    }

    #[test]
    fn ssrf_predicate_blocks_private_and_special_ranges() {
        for s in [
            "127.0.0.1",        // loopback v4
            "10.0.0.5",         // private
            "172.16.0.1",       // private
            "192.168.1.1",      // private
            "169.254.0.1",      // link-local
            "0.0.0.0",          // unspecified
            "255.255.255.255",  // broadcast
            "::1",              // loopback v6
            "::",               // unspecified v6
            "fc00::1",          // unique local
            "fe80::1",          // link-local v6
            "::ffff:127.0.0.1", // ipv4-mapped loopback
        ] {
            assert!(is_blocked_ip(ip(s)), "{s} must be blocked");
        }
    }

    #[test]
    fn ssrf_predicate_allows_public_addresses() {
        for s in ["8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1::1"] {
            assert!(!is_blocked_ip(ip(s)), "{s} must be allowed");
        }
    }

    #[test]
    fn normalize_host_lowercases_and_strips_trailing_dot() {
        assert_eq!(normalize_host("API.Example."), "api.example");
        assert_eq!(normalize_host("host"), "host");
    }
}
