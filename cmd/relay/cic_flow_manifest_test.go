package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A project.yaml shaped like cic-module-oracle-cloud's.
const ociProjectYAML = `
metadata:
  buildHash: BUILDHASH
  cicSign: 'TBD'
abi:
  imports:
    - module: cic-flow
      functions: [sign, actuate]
capability_manifest:
  version: v0.1.0
  egress:
    - host: "*.oraclecloud.com"
      methods: [GET, POST, PUT, DELETE, PATCH]
  secrets:
    - handle: "oci/*"
      ops: [sign]
`

func TestParseModuleProject(t *testing.T) {
	p, err := parseModuleProject([]byte(ociProjectYAML))
	require.NoError(t, err)
	assert.Equal(t, "BUILDHASH", p.Metadata.BuildHash)
	require.Len(t, p.ABI.Imports, 1)
	assert.Equal(t, "cic-flow", p.ABI.Imports[0].Module)
	assert.Equal(t, []string{"sign", "actuate"}, p.ABI.Imports[0].Functions)
	require.Len(t, p.CapabilityManifest.Egress, 1)
	assert.Equal(t, "*.oraclecloud.com", p.CapabilityManifest.Egress[0].Host)
	require.Len(t, p.CapabilityManifest.Secrets, 1)
	assert.Equal(t, "oci/*", p.CapabilityManifest.Secrets[0].Handle)
}

func TestParseModuleProject_BadYAML(t *testing.T) {
	_, err := parseModuleProject([]byte("\tnot: [valid"))
	assert.Error(t, err)
}

func TestHostGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"api.example.com", "api.example.com", true},
		{"api.example.com", "API.EXAMPLE.COM", true}, // case-insensitive
		{"api.example.com", "other.example.com", false},
		{"*.oraclecloud.com", "iaas.eu-frankfurt-1.oraclecloud.com", true},
		{"*.oraclecloud.com", "objectstorage.eu.oraclecloud.com", true},
		{"*.oraclecloud.com", "oraclecloud.com", false},      // apex, not a subdomain
		{"*.oraclecloud.com", "evil-oraclecloud.com", false}, // no dot boundary
		{"*.oraclecloud.com", "oraclecloud.com.evil.com", false},
		{"*", "anything.com", false},  // bare * rejected
		{"*.", "x.", false},           // empty suffix rejected
		{"a.*.com", "a.b.com", false}, // infix wildcard rejected
		{"", "a.com", false},          // empty pattern
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, hostGlobMatch(c.pattern, c.host), "pattern=%q host=%q", c.pattern, c.host)
	}
}

func TestEgressPolicy_Allows(t *testing.T) {
	pol := egressPolicy{
		{Host: "*.oraclecloud.com", Methods: []string{"GET", "PUT"}, Paths: []string{"/20160918/"}, Ports: []int{443}},
	}
	// exact permitted
	assert.True(t, pol.allows("PUT", "iaas.eu.oraclecloud.com", 443, "/20160918/vcns/x"))
	assert.True(t, pol.allows("get", "iaas.eu.oraclecloud.com", 443, "/20160918/vcns")) // method case-insensitive
	// method not allowed
	assert.False(t, pol.allows("DELETE", "iaas.eu.oraclecloud.com", 443, "/20160918/vcns"))
	// host not matched
	assert.False(t, pol.allows("GET", "evil.com", 443, "/20160918/vcns"))
	// port not allowed
	assert.False(t, pol.allows("GET", "iaas.eu.oraclecloud.com", 8443, "/20160918/vcns"))
	// path prefix not matched
	assert.False(t, pol.allows("GET", "iaas.eu.oraclecloud.com", 443, "/other/thing"))

	// empty policy denies everything
	assert.False(t, egressPolicy(nil).allows("GET", "api.example.com", 443, "/x"))
}

func TestEgressPolicy_Defaults(t *testing.T) {
	// empty methods/paths/ports → any method, any path, port 443
	pol := egressPolicy{{Host: "api.example.com"}}
	assert.True(t, pol.allows("DELETE", "api.example.com", 443, "/anything"))
	assert.False(t, pol.allows("GET", "api.example.com", 80, "/anything")) // default port is 443 only
}

func TestBindCICFlowDeps(t *testing.T) {
	m := capabilityManifest{
		Egress:  egressPolicy{{Host: "*.oraclecloud.com"}},
		Secrets: []secretRule{{Handle: "oci/prod", Ops: []string{"sign"}}},
	}
	var gotHandle string
	resolve := func(h string) (signer, error) { gotHandle = h; return &stubSigner{sig: "sig"}, nil }

	deps, err := bindCICFlowDeps(m, resolve, &stubDoer{})
	require.NoError(t, err)
	assert.Equal(t, "oci/prod", gotHandle)
	assert.NotNil(t, deps.sign)
	assert.Equal(t, m.Egress, deps.egress)

	// nil resolver → nil signer (sign inert), egress still bound
	deps, err = bindCICFlowDeps(m, nil, &stubDoer{})
	require.NoError(t, err)
	assert.Nil(t, deps.sign)
	assert.Len(t, deps.egress, 1)

	// no sign handle → nil signer
	deps, err = bindCICFlowDeps(capabilityManifest{Egress: m.Egress}, resolve, &stubDoer{})
	require.NoError(t, err)
	assert.Nil(t, deps.sign)

	// resolver error → propagated
	_, err = bindCICFlowDeps(m, func(string) (signer, error) { return nil, errors.New("vault down") }, &stubDoer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vault down")
}

func TestCheckImportsWithinManifest(t *testing.T) {
	ok, err := parseModuleProject([]byte(ociProjectYAML))
	require.NoError(t, err)
	require.NoError(t, ok.checkImportsWithinManifest()) // declares both

	// imports sign but no sign secret → reject
	p := &moduleProject{}
	p.ABI.Imports = []struct {
		Module    string   `yaml:"module"`
		Functions []string `yaml:"functions"`
	}{{Module: "cic-flow", Functions: []string{"sign"}}}
	err = p.checkImportsWithinManifest()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no secrets entry with ops:[sign]")

	// imports actuate but no egress → reject
	p2 := &moduleProject{}
	p2.ABI.Imports = []struct {
		Module    string   `yaml:"module"`
		Functions []string `yaml:"functions"`
	}{{Module: "cic-flow", Functions: []string{"actuate"}}}
	err = p2.checkImportsWithinManifest()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no egress")

	// unrelated import module ignored
	p3 := &moduleProject{}
	p3.ABI.Imports = []struct {
		Module    string   `yaml:"module"`
		Functions []string `yaml:"functions"`
	}{{Module: "git", Functions: []string{"clone"}}}
	require.NoError(t, p3.checkImportsWithinManifest())
}

func TestVerifyBuildHash(t *testing.T) {
	wasm := []byte("\x00asm\x01\x00\x00\x00fake-module")
	sum := sha256.Sum256(wasm)
	hexsum := hex.EncodeToString(sum[:])

	p := &moduleProject{}
	p.Metadata.BuildHash = hexsum
	require.NoError(t, p.verifyBuildHash(wasm))

	p.Metadata.BuildHash = "deadbeef"
	err := p.verifyBuildHash(wasm)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buildHash mismatch")

	p.Metadata.BuildHash = ""
	assert.Error(t, p.verifyBuildHash(wasm))
}

func TestGrantsSignAndHandle(t *testing.T) {
	m := capabilityManifest{Secrets: []secretRule{{Handle: "oci/x", Ops: []string{"read", "SIGN"}}}}
	assert.True(t, m.grantsSign()) // case-insensitive
	h, ok := m.signHandle()
	assert.True(t, ok)
	assert.Equal(t, "oci/x", h)

	none := capabilityManifest{Secrets: []secretRule{{Handle: "oci/y", Ops: []string{"read"}}}}
	assert.False(t, none.grantsSign())
	_, ok = none.signHandle()
	assert.False(t, ok)
}

// ensure keyResolver stays type-compatible with the signer interface.
var _ keyResolver = func(string) (signer, error) { return nil, nil }
