//! Live-path integration tests for [`HttpExecutor`] against a mocked HTTP server
//! (`wiremock`), mirroring `vault-adapter/tests/live_mock.rs`. They drive the
//! real `reqwest` transport end to end without a real upstream, and pin the two
//! load-bearing guarantees: the opened credential is delivered as a Bearer
//! `Authorization` header, and a *reached* endpoint is an `Ok` outcome for any
//! status.

use std::time::Duration;

use executor::{ExecRequest, ExecuteError, Method};
use http_executor::{EgressPolicy, HttpExecutor, Limits};
use secrecy::SecretString;
use wiremock::matchers::{body_bytes, header, method, path};
use wiremock::{Mock, MockServer, ResponseTemplate};

fn secret(value: &str) -> SecretString {
    SecretString::from(value.to_owned())
}

/// Builds an executor allowing plain http to the loopback host the mock server
/// listens on (`reqwest` dials the absolute URL, so no base is needed here).
fn loopback_executor() -> HttpExecutor {
    HttpExecutor::new(EgressPolicy::insecure_allowing(["127.0.0.1"])).expect("client builds")
}

#[tokio::test]
async fn delivers_bearer_credential_and_returns_ok_outcome() {
    let server = MockServer::start().await;
    // The matcher *requires* the Authorization header, so a 200 match proves the
    // opened credential was applied as a Bearer token.
    Mock::given(method("GET"))
        .and(path("/ping"))
        .and(header("authorization", "Bearer s3cr3t"))
        .respond_with(ResponseTemplate::new(200).set_body_bytes(b"pong".to_vec()))
        .mount(&server)
        .await;

    let exec = loopback_executor();
    let req = ExecRequest::new(Method::Get, format!("{}/ping", server.uri()));

    let out = exec
        .execute(&req, &secret("s3cr3t"))
        .await
        .expect("reaches endpoint");

    assert_eq!(out.status, 200);
    assert_eq!(out.body, b"pong");
    // The secret never rides along in the outcome.
    assert!(!format!("{out:?}").contains("s3cr3t"));
}

#[tokio::test]
async fn reached_endpoint_returning_401_is_an_ok_outcome_not_an_error() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/guarded"))
        .respond_with(ResponseTemplate::new(401))
        .mount(&server)
        .await;

    let exec = loopback_executor();
    let req = ExecRequest::new(Method::Get, format!("{}/guarded", server.uri()));

    let out = exec
        .execute(&req, &secret("whatever"))
        .await
        .expect("a 401 is a reached outcome");
    assert_eq!(out.status, 401);
}

#[tokio::test]
async fn redirects_are_returned_not_followed() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/moved"))
        .respond_with(
            ResponseTemplate::new(302).insert_header("location", "https://elsewhere.example/"),
        )
        .mount(&server)
        .await;

    let exec = loopback_executor();
    let req = ExecRequest::new(Method::Get, format!("{}/moved", server.uri()));

    let out = exec.execute(&req, &secret("x")).await.expect("reaches");
    // Auto-follow is disabled (M43): the 3xx comes back to the caller.
    assert_eq!(out.status, 302);
    assert_eq!(
        out.headers.get("location").map(String::as_str),
        Some("https://elsewhere.example/")
    );
}

#[tokio::test]
async fn forwards_method_and_body() {
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/submit"))
        .and(body_bytes(b"payload".to_vec()))
        .respond_with(ResponseTemplate::new(201))
        .mount(&server)
        .await;

    let exec = loopback_executor();
    let req = ExecRequest::new(Method::Post, format!("{}/submit", server.uri()))
        .with_body(b"payload".to_vec());

    let out = exec.execute(&req, &secret("x")).await.expect("reaches");
    assert_eq!(out.status, 201);
}

#[tokio::test]
async fn unreachable_endpoint_maps_to_unavailable_without_leaking() {
    // Allowlisted loopback host, but a port with nothing listening → connect
    // refused → Unavailable (never carries the secret).
    let exec = HttpExecutor::new(EgressPolicy::insecure_allowing(["127.0.0.1"])).expect("builds");
    let req = ExecRequest::new(Method::Get, "http://127.0.0.1:1/x");

    let err = exec
        .execute(&req, &secret("s3cr3t"))
        .await
        .expect_err("connection refused");
    assert!(matches!(err, ExecuteError::Unavailable(_)));
    assert!(!format!("{err:?}").contains("s3cr3t"));
}

#[tokio::test]
async fn oversized_response_body_is_refused_with_limit_exceeded() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/big"))
        .respond_with(ResponseTemplate::new(200).set_body_bytes(vec![b'x'; 4096]))
        .mount(&server)
        .await;

    let exec = HttpExecutor::with_limits(
        EgressPolicy::insecure_allowing(["127.0.0.1"]),
        Limits {
            max_response_bytes: 64,
            ..Limits::default()
        },
    )
    .expect("builds");
    let req = ExecRequest::new(Method::Get, format!("{}/big", server.uri()));

    let err = exec
        .execute(&req, &secret("x"))
        .await
        .expect_err("body over cap");
    assert!(matches!(err, ExecuteError::LimitExceeded(_)));
}

#[tokio::test]
async fn slow_endpoint_times_out_as_unavailable() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/slow"))
        .respond_with(ResponseTemplate::new(200).set_delay(Duration::from_millis(600)))
        .mount(&server)
        .await;

    let exec = HttpExecutor::with_limits(
        EgressPolicy::insecure_allowing(["127.0.0.1"]),
        Limits {
            request_timeout: Duration::from_millis(60),
            ..Limits::default()
        },
    )
    .expect("builds");
    let req = ExecRequest::new(Method::Get, format!("{}/slow", server.uri()));

    let err = exec
        .execute(&req, &secret("x"))
        .await
        .expect_err("times out");
    assert!(matches!(err, ExecuteError::Unavailable(_)));
}

#[tokio::test]
async fn hostname_target_resolves_through_the_guarded_resolver() {
    // Uses a *name* (localhost) rather than an IP literal, so reqwest exercises
    // the custom SSRF-guarding resolver; the insecure policy permits loopback, so
    // it resolves and the request reaches the mock.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/via-name"))
        .respond_with(ResponseTemplate::new(200).set_body_bytes(b"ok".to_vec()))
        .mount(&server)
        .await;

    let exec = HttpExecutor::new(EgressPolicy::insecure_allowing(["localhost"])).expect("builds");
    let target = format!("{}/via-name", server.uri()).replace("127.0.0.1", "localhost");
    let req = ExecRequest::new(Method::Get, target);

    let out = exec
        .execute(&req, &secret("x"))
        .await
        .expect("reaches via name");
    assert_eq!(out.status, 200);
    assert_eq!(out.body, b"ok");
}

#[tokio::test]
async fn hostname_resolving_to_private_ip_is_blocked_by_resolver() {
    // Production policy: the name is allowlisted, but it resolves to loopback, so
    // the guarded resolver refuses every address and the dial never happens.
    let exec = HttpExecutor::new(EgressPolicy::allowing(["localhost"])).expect("builds");
    let req = ExecRequest::new(Method::Get, "https://localhost:1/x");

    let err = exec
        .execute(&req, &secret("x"))
        .await
        .expect_err("resolver blocks loopback");
    assert!(matches!(err, ExecuteError::Unavailable(_)));
}
