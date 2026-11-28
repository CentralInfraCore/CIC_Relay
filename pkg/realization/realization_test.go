package realization

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const good = `{"kind":"CICRealizationContext","version":1,"context_id":"oci-fra-obs","provider":"oci","region":"eu-frankfurt-1",` +
	`"host":"iaas.eu-frankfurt-1.oraclecloud.com","base_path":"/20160918","scope_id":"ocid1.compartment.oc1..aaaa"}`

func code(t *testing.T, raw, pin string) string {
	t.Helper()
	_, err := Admit([]byte(raw), pin)
	require.Error(t, err)
	var e *Error
	require.True(t, errors.As(err, &e))
	return e.Code
}

func TestAdmitsThePinnedObjectAndAddressesItByContent(t *testing.T) {
	a, err := Admit([]byte(good), Digest([]byte(good)))
	require.NoError(t, err)
	assert.Equal(t, Digest([]byte(good)), a.Digest)
	assert.Equal(t, "oci", a.Context.Provider)
	assert.Equal(t, "iaas.eu-frankfurt-1.oraclecloud.com", a.Context.Host)
	changed := strings.Replace(good, "eu-frankfurt-1.oraclecloud", "eu-amsterdam-1.oraclecloud", 1)
	assert.NotEqual(t, Digest([]byte(good)), Digest([]byte(changed)), "a different environment is a different address")
}

func TestNotPinned(t *testing.T) {
	other := strings.Replace(good, "oci-fra-obs", "oci-other", 1)
	assert.Equal(t, CodeNotPinned, code(t, other, Digest([]byte(good))), "valid, but not the pinned one")
	assert.Equal(t, CodeNotPinned, code(t, good, ""), "no pin: not 'accept anything'")
	assert.Equal(t, CodeNotPinned, code(t, good, "sha256:XYZ"))
	assert.Equal(t, CodeNotPinned, code(t, `not json`, Digest([]byte(good))), "an unpinned object is not even parsed")
}

func TestInvalidShapeAndValues(t *testing.T) {
	cases := map[string]string{
		"duplicate key":    strings.Replace(good, `"version":1,`, `"version":1,"version":2,`, 1),
		"unknown key":      strings.Replace(good, `"kind"`, `"extra":1,"kind"`, 1),
		"missing key":      strings.Replace(good, `,"scope_id":"ocid1.compartment.oc1..aaaa"`, ``, 1),
		"case-variant key": strings.Replace(good, `"host"`, `"Host"`, 1),
		"trailing data":    good + ` {}`,
		"wrong kind":       strings.Replace(good, "CICRealizationContext", "Other", 1),
		"wrong version":    strings.Replace(good, `"version":1`, `"version":2`, 1),
		"string version":   strings.Replace(good, `"version":1`, `"version":"1"`, 1),
		"bad context id":   strings.Replace(good, "oci-fra-obs", "OCI FRA", 1),
		"bad provider":     strings.Replace(good, `"provider":"oci"`, `"provider":""`, 1),
		"bad region":       strings.Replace(good, `"region":"eu-frankfurt-1"`, `"region":"EU"`, 1),
		"uppercase host":   strings.Replace(good, "iaas.eu", "IAAS.eu", 1),
		"host with a port": strings.Replace(good, "oraclecloud.com\"", "oraclecloud.com:443\"", 1),
		"host without dot": strings.Replace(good, "iaas.eu-frankfurt-1.oraclecloud.com", "localhost", 1),
		"relative path":    strings.Replace(good, "/20160918", "20160918", 1),
		"path with query":  strings.Replace(good, "/20160918", "/20160918?x=1", 1),
		"path with space":  strings.Replace(good, "/20160918", "/2016 0918", 1),
		"bad scope":        strings.Replace(good, "ocid1.compartment.oc1..aaaa", "has space", 1),
		"empty scope":      strings.Replace(good, "ocid1.compartment.oc1..aaaa", "", 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) { assert.Equal(t, CodeInvalid, code(t, raw, Digest([]byte(raw)))) })
	}
	assert.Contains(t, (&Error{Code: CodeInvalid, Message: "m"}).Error(), CodeInvalid)
}
