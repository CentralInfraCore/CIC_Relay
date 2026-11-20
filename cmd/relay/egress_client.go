package main

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"
)

// The egress limits of the `actuate` host function. They mirror the Rust
// http-executor (http-executor/src/lib.rs, Limits::default and EgressPolicy); the shared
// fixture testdata/egress-parity/policy.txt is asserted on both sides, so changing one
// without the other fails a test.
const (
	// egressRequestTimeout bounds one whole egress request, response body included.
	egressRequestTimeout = 30 * time.Second
	// egressConnectTimeout bounds the TCP connect and the TLS handshake.
	egressConnectTimeout = 10 * time.Second
	// egressMaxResponseBytes caps the response body returned to the module; a larger response is
	// refused (egressTooLargeMsg), never truncated.
	egressMaxResponseBytes = 1 << 20 // 1 MiB
)

// errEgressBlockedAddr is returned when a connection would reach an address an actuation
// must never reach.
var errEgressBlockedAddr = errors.New("egress: destination address is blocked (private, loopback, link-local, unspecified or broadcast)")

// isBlockedIP reports whether ip is in a private, loopback, link-local, unspecified or
// broadcast range (SSRF defense). IPv4-mapped IPv6 addresses are judged as IPv4. The
// ranges are exactly those of the Rust http-executor's is_blocked_ip.
func isBlockedIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		return v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() ||
			v4.IsUnspecified() || v4.Equal(net.IPv4bcast)
	}
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// newEgressClient builds the HTTP client of the `actuate` host function:
//   - a redirect is never followed (the 3xx is returned to the caller), so the egress
//     allow-list, which only sees the first URL, cannot be bypassed by a redirect;
//   - ambient proxy environment is ignored;
//   - connect and end-to-end timeouts are set;
//   - every connection is checked at dial time, after DNS resolution, against the blocked
//     address ranges (this also covers a DNS answer that points at a private address).
//
// allowPrivate lifts the address check; it exists only for tests against a local server.
func newEgressClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: egressConnectTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return errEgressBlockedAddr // an address we cannot read is not dialed
			}
			if ip := net.ParseIP(host); ip == nil || (!allowPrivate && isBlockedIP(ip)) {
				return errEgressBlockedAddr
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: egressRequestTimeout,
		Transport: &http.Transport{
			Proxy:               nil, // never ambient HTTP(S)_PROXY
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: egressConnectTimeout,
			ForceAttemptHTTP2:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
