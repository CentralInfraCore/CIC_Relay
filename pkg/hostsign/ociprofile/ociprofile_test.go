package ociprofile_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/hostsign"
	"centralrelay/pkg/hostsign/ociprofile"
)

const (
	host = "iaas.eu-frankfurt-1.oraclecloud.com"
	res  = "ocid1.vcn.oc1.eu-frankfurt-1.aaaa"
	bind = "sha256:abababababababababababababababababababababababababababababababab"
)

func TestIdentityAndDigest(t *testing.T) {
	p := ociprofile.New()
	assert.Equal(t, "http-signature:oci:draft-cavage@1", p.ID())
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, p.Digest())
	assert.Equal(t, p.Digest(), ociprofile.New().Digest(), "stable")
	assert.Zero(t, p.Limits())
}

// Golden strings: the same canonical form the guest used to build itself (module oci_sign.go).
func TestCanonicalGolden(t *testing.T) {
	p := ociprofile.New()
	date := "Fri, 27 Nov 2026 12:00:00 GMT"
	get, err := p.Prepare(hostsign.PrepRequest{Method: "GET", Host: host, Path: "/20160918/vcns/" + res}, date)
	require.NoError(t, err)
	assert.Equal(t, "(request-target): get /20160918/vcns/"+res+"\nhost: "+host+"\ndate: "+date, string(get.StringToSign))
	assert.Equal(t, map[string]string{"date": date}, get.Headers)
	assert.Equal(t, `Signature version="1",keyId="t/u/f",algorithm="rsa-sha256",headers="(request-target) host date",signature="SIG"`, get.Authorize("t/u/f", "SIG"))

	body := []byte(`{"displayName":"v"}`)
	put, err := p.Prepare(hostsign.PrepRequest{Method: "PUT", Host: host, Path: "/p", Body: body, Query: map[string]string{"b": "2", "a": "1"}}, date)
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	xcs := base64.StdEncoding.EncodeToString(sum[:])
	assert.Equal(t, "(request-target): put /p?a=1&b=2\nhost: "+host+"\ndate: "+date+"\nx-content-sha256: "+xcs+"\ncontent-type: application/json\ncontent-length: 19", string(put.StringToSign))
	assert.Contains(t, put.Authorize("k", "s"), `headers="(request-target) host date x-content-sha256 content-type content-length"`)

	_, err = p.Prepare(hostsign.PrepRequest{Method: "GET", Host: host, Path: "/p", Body: []byte("x")}, date)
	assert.Error(t, err, "a GET carries no body")
}

type rsaSigner struct{ key *rsa.PrivateKey }

func (s rsaSigner) Sign(_ context.Context, data []byte) (string, error) {
	h := sha256.Sum256(data)
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, h[:])
	return base64.StdEncoding.EncodeToString(sig), err
}

var sigRe = regexp.MustCompile(`signature="([^"]+)"`)

// End to end through the engine with a real RSA key: a fake OCI endpoint re-derives the canonical
// string from the received request and verifies the signature as OCI would; a PUT with a body is
// accepted, and the guest-visible response carries no signature.
func TestEndToEndVerifiesAsOCI(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var seen int
	handler := func(req *http.Request) (*http.Response, error) {
		seen++
		body, _ := io.ReadAll(req.Body)
		auth := req.Header.Get("authorization")
		assert.Contains(t, auth, `keyId="ten/usr/fp"`)
		hs := regexp.MustCompile(`headers="([^"]+)"`).FindStringSubmatch(auth)
		require.Len(t, hs, 2)
		var lines []string
		for _, h := range strings.Fields(hs[1]) {
			switch h {
			case "(request-target)":
				lines = append(lines, "(request-target): "+strings.ToLower(req.Method)+" "+req.URL.RequestURI())
			case "host":
				lines = append(lines, "host: "+req.Host)
			default:
				lines = append(lines, h+": "+req.Header.Get(h))
			}
		}
		sum := sha256.Sum256(body)
		assert.Equal(t, base64.StdEncoding.EncodeToString(sum[:]), req.Header.Get("x-content-sha256"))
		raw, _ := base64.StdEncoding.DecodeString(sigRe.FindStringSubmatch(auth)[1])
		h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
		status := 200
		if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, h[:], raw) != nil {
			status = 401
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}
	e := newEngine(t, key, handler)
	req := map[string]any{
		"v": 1, "signing_profile": ociprofile.ProfileID, "key_ref": "oci/default",
		"request": map[string]any{"method": "PUT", "host": host, "path": "/20160918/vcns/" + res,
			"body": map[string]any{"base64": base64.StdEncoding.EncodeToString([]byte(`{"displayName":"v"}`))}, "headers": map[string]any{"if-match": "e1"}},
		"intent": map[string]any{"operation": "execute", "resource_id": res, "binding_digest": bind},
	}
	raw, _ := json.Marshal(req)
	out := e.Handle(context.Background(), raw)
	var resp hostsign.Response
	require.NoError(t, json.Unmarshal(out, &resp), string(out))
	require.Equal(t, "completed", resp.Outcome, string(out))
	assert.Equal(t, 200, resp.HTTP.Status, "the signature verified at the endpoint")
	assert.Equal(t, 1, seen)
	assert.NotContains(t, strings.ToLower(string(out)), "signature")
	recs := e.Log().Records()
	require.Len(t, recs, 1)
	assert.Equal(t, ociprofile.New().Digest(), recs[0].SigningProfileDigest)

	// control: a signer with a different key is rejected by the same endpoint (the check can fail)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	e2 := newEngine(t, other, handler)
	out = e2.Handle(context.Background(), raw)
	require.NoError(t, json.Unmarshal(out, &resp), string(out))
	assert.Equal(t, 401, resp.HTTP.Status, "a signature by another key must not verify")
}

func newEngine(t *testing.T, signKey *rsa.PrivateKey, h func(*http.Request) (*http.Response, error)) *hostsign.Engine {
	t.Helper()
	pol := hostsign.Policy{
		Hosts:      []string{host},
		Operations: map[string]hostsign.OperationPolicy{"execute": {Methods: []string{"PUT"}, PathTemplate: "/20160918/vcns/{resource_id}"}},
		Bindings:   map[string]string{res: bind},
		Limits:     hostsign.Limits{TimeoutMS: 1000, MaxRequestBytes: 4096, MaxResponseBytes: 4096},
	}
	e, err := hostsign.NewEngine(hostsign.Config{
		Policy: pol, Profiles: []hostsign.Profile{ociprofile.New()},
		Keys: func(ref string) (hostsign.Key, bool) {
			return hostsign.Key{Signer: rsaSigner{signKey}, KeyID: "ten/usr/fp"}, ref == "oci/default"
		},
		Egress:        func(string, string, int, string) bool { return true },
		Doer:          doerFunc(h),
		Now:           func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
		ProfileDigest: bind,
	})
	require.NoError(t, err)
	return e
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
