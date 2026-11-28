// Package ociprofile is the OCI draft-cavage HTTP Signatures signing profile of the cic-flow.send
// host capability (D-11). It depends only on the hostsign seam and the standard library: it must
// not import core/..., and neither the core nor hostsign may import it (see hostsign/arch_test.go).
package ociprofile

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"centralrelay/pkg/hostsign"
)

// ProfileID is the identifier a guest request names.
const ProfileID = "http-signature:oci:draft-cavage@1"

// descriptor is the definition the profile digest covers: identity, scheme, algorithm and the
// exact signed header sets. Changing any of them changes the digest recorded in every interaction.
const descriptor = `{"id":"http-signature:oci:draft-cavage@1","scheme":"draft-cavage-http-signatures","algorithm":"rsa-sha256","signed_headers":["(request-target)","host","date"],"signed_headers_with_body":["(request-target)","host","date","x-content-sha256","content-type","content-length"],"default_content_type":"application/json"}`

// Profile is the OCI signing profile.
type Profile struct{}

// New returns the OCI profile.
func New() *Profile { return &Profile{} }

// ID implements hostsign.Profile.
func (*Profile) ID() string { return ProfileID }

// Digest implements hostsign.Profile: sha256 of the domain-separated profile descriptor.
func (*Profile) Digest() string {
	sum := sha256.Sum256([]byte("CICSIGNPROFILEv1\n" + descriptor))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Limits implements hostsign.Profile: the OCI profile does not narrow the operator limits.
func (*Profile) Limits() hostsign.Limits { return hostsign.Limits{} }

// bodyMethod is a helper of the cic-flow.send host capability.
func bodyMethod(m string) bool { return m == "POST" || m == "PUT" || m == "PATCH" }

// Prepare implements hostsign.Profile: the draft-cavage canonical string, the wire headers the
// host sets, and the Authorization assembler.
func (*Profile) Prepare(r hostsign.PrepRequest, date string) (*hostsign.Prepared, error) {
	if len(r.Body) > 0 && !bodyMethod(r.Method) {
		return nil, fmt.Errorf("a %s request carries no body in this profile", r.Method)
	}
	lines := []string{
		"(request-target): " + strings.ToLower(r.Method) + " " + r.Target(),
		"host: " + r.Host,
		"date: " + date,
	}
	signed := []string{"(request-target)", "host", "date"}
	wire := map[string]string{"date": date}
	if bodyMethod(r.Method) {
		sum := sha256.Sum256(r.Body)
		xcs := base64.StdEncoding.EncodeToString(sum[:])
		ct := r.Headers["content-type"]
		if ct == "" {
			ct = "application/json"
		}
		cl := strconv.Itoa(len(r.Body))
		lines = append(lines, "x-content-sha256: "+xcs, "content-type: "+ct, "content-length: "+cl)
		signed = append(signed, "x-content-sha256", "content-type", "content-length")
		wire["x-content-sha256"], wire["content-type"], wire["content-length"] = xcs, ct, cl
	}
	headers := strings.Join(signed, " ")
	return &hostsign.Prepared{
		StringToSign: []byte(strings.Join(lines, "\n")),
		Headers:      wire,
		Authorize: func(keyID, sig string) string {
			return `Signature version="1",keyId="` + keyID + `",algorithm="rsa-sha256",headers="` + headers + `",signature="` + sig + `"`
		},
	}, nil
}
