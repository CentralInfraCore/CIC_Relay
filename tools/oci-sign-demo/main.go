// Command oci-sign-demo proves the feature-011 OCI *sign-then-send* signing
// profile end to end, with no relay, no Vault, and no cgo — the smallest thing
// that de-risks R2 (#88) before wiring the real capability.
//
// It builds the OCI draft-cavage HTTP-Signatures canonical string for a sample
// `PUT /vcns/{id}`, signs it RSA-SHA256 (PKCS#1 v1.5) — the exact operation
// `cic-flow.sign` performs, here over an in-process RSA key that stands in for
// the scoped Vault Transit key (in production the key never leaves Vault) —
// assembles the `Authorization: Signature …` header, and actuates against a fake
// OCI endpoint that **verifies the signature against the RSA public key exactly
// as OCI would**. It then tampers the body and shows the endpoint reject it, so
// the signature demonstrably binds the request.
//
// This isolates the crypto profile (canonical header set + order, RSA-SHA256
// PKCS#1 v1.5) — the highest-risk unknown — from the wazero plumbing (already
// unit-tested) and the Vault custody (already proven by `make demo-trustflow`).
// Run: `make demo-oci-sign` (opt-in, not a CI gate).
package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	keyID = "ocid1.tenancy.oc1..demo/ocid1.user.oc1..demo/aa:bb:cc:dd"
	host  = "iaas.eu-frankfurt-1.oraclecloud.com"
)

// signedHeaders is the OCI header set + order for body methods (POST/PUT/PATCH).
var signedHeaders = []string{"(request-target)", "host", "date", "x-content-sha256", "content-type", "content-length"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n[!] demo failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// A per-run RSA-2048 key stands in for the scoped Vault Transit key that
	// cic-flow.sign uses; in production the key never leaves Vault.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	// The fake OCI endpoint: it verifies the RSA signature exactly as OCI would.
	srv := httptest.NewServer(http.HandlerFunc(ociVerifyHandler(&key.PublicKey)))
	defer srv.Close()

	method := http.MethodPut
	target := "/20160918/vcns/ocid1.vcn.oc1..demo"
	body := []byte(`{"displayName":"production-vcn","cidrBlocks":["10.10.0.0/16"]}`)

	fmt.Println("[*] feature-011 sign-then-send profile proof (no relay, no Vault, no cgo)")
	fmt.Printf("      request: %s %s  host=%s  body=%d bytes\n\n", method, target, host, len(body))

	// --- guest side: build canonical string, sign, assemble Authorization ---
	date := time.Now().UTC().Format(http.TimeFormat)
	xContentSHA := b64sha(body)
	wire := map[string]string{
		"date":             date,
		"x-content-sha256": xContentSHA,
		"content-type":     "application/json",
		"content-length":   strconv.Itoa(len(body)),
	}
	stringToSign := ociStringToSign(method, target, host, wire)
	fmt.Println("[*] canonical string_to_sign (built by the guest — not secret):")
	for _, line := range strings.Split(stringToSign, "\n") {
		fmt.Printf("      %s\n", line)
	}

	// cic-flow.sign: RSA-SHA256 PKCS#1 v1.5 over the canonical string.
	sig, err := signRSASHA256(key, stringToSign)
	if err != nil {
		return err
	}
	authz := ociAuthorization(keyID, signedHeaders, sig)
	fmt.Printf("\n[*] cic-flow.sign → signature (base64, %d chars); Authorization assembled\n", len(sig))

	// --- cic-flow.actuate: carry our OWN Authorization to the endpoint ---
	status, err := actuate(srv.URL+target, method, host, body, authz, wire)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("OCI endpoint rejected the signed request: HTTP %d", status)
	}
	fmt.Printf("[ok] OCI endpoint accepted the request (HTTP %d) — signature verified against the RSA public key\n", status)

	// --- tamper: the signature must bind the request ---
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-2] ^= 0xFF // flip a byte inside the body
	status, err = actuate(srv.URL+target, method, host, tampered, authz, wire)
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return fmt.Errorf("tampered body was accepted — the signature did not bind the request")
	}
	fmt.Printf("[ok] tampered body rejected (HTTP %d) — the signature binds the exact request\n", status)

	fmt.Println("\n[ok] R2 signing profile proven: OCI draft-cavage canonical string + RSA-SHA256 (PKCS#1 v1.5)")
	fmt.Println("     verifies as OCI would; the caller's own Authorization is the authenticator.")
	return nil
}

// ociStringToSign builds the draft-cavage canonical string over signedHeaders.
// (request-target) = "<lowercase-method> <path>"; host from the wire host; the
// rest from the request headers.
func ociStringToSign(method, target, hostHeader string, wire map[string]string) string {
	var b strings.Builder
	for i, h := range signedHeaders {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch h {
		case "(request-target)":
			fmt.Fprintf(&b, "(request-target): %s %s", strings.ToLower(method), target)
		case "host":
			fmt.Fprintf(&b, "host: %s", hostHeader)
		default:
			fmt.Fprintf(&b, "%s: %s", h, wire[h])
		}
	}
	return b.String()
}

// ociAuthorization assembles the OCI `Signature` Authorization header.
func ociAuthorization(kid string, headers []string, sigB64 string) string {
	return fmt.Sprintf(
		`Signature version="1",keyId=%q,algorithm="rsa-sha256",headers=%q,signature=%q`,
		kid, strings.Join(headers, " "), sigB64,
	)
}

// signRSASHA256 signs the string RSA-SHA256 with PKCS#1 v1.5 — OCI's scheme, and
// what the OCI cic-flow signer must ask Vault Transit for (signature_algorithm=
// pkcs1v15; Vault's RSA default is PSS, which OCI rejects — see design.md).
func signRSASHA256(key *rsa.PrivateKey, stringToSign string) (string, error) {
	h := sha256.Sum256([]byte(stringToSign))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// actuate is the cic-flow.actuate step: send the request carrying the caller's
// own Authorization header verbatim. Returns the HTTP status.
func actuate(url, method, hostHeader string, body []byte, authz string, wire map[string]string) (int, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Host = hostHeader // the signed host, not the TCP host
	req.Header.Set("authorization", authz)
	for k, v := range wire {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

var sigParamRE = regexp.MustCompile(`signature="([^"]+)"`)

// ociVerifyHandler returns an HTTP handler that re-derives the canonical string
// from the received request and verifies the RSA-SHA256 signature against pub,
// exactly as OCI's control plane would. 200 iff valid, else 401.
func ociVerifyHandler(pub *rsa.PublicKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		received, _ := io.ReadAll(r.Body)
		// The x-content-sha256 the client signed must match the body we received.
		if r.Header.Get("x-content-sha256") != b64sha(received) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		wire := map[string]string{
			"date":             r.Header.Get("date"),
			"x-content-sha256": r.Header.Get("x-content-sha256"),
			"content-type":     r.Header.Get("content-type"),
			"content-length":   r.Header.Get("content-length"),
		}
		stringToSign := ociStringToSign(r.Method, r.URL.RequestURI(), r.Host, wire)

		m := sigParamRE.FindStringSubmatch(r.Header.Get("authorization"))
		if m == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sig, err := base64.StdEncoding.DecodeString(m[1])
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h := sha256.Sum256([]byte(stringToSign))
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}

func b64sha(b []byte) string {
	s := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(s[:])
}
