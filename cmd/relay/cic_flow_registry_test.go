package main

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCICFlowRegistry_DefaultsDoer(t *testing.T) {
	reg := newCICFlowRegistry(nil, nil)
	assert.Equal(t, http.DefaultClient, reg.doer)
	_, ok := reg.lookup("anything")
	assert.False(t, ok) // empty → deny
}

func TestCICFlowRegistry_RegisterVerified_Binds(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	require.NoError(t, reg.registerVerified("cic-module-oracle-cloud@0.1.0", []byte(ociProjectYAML)))

	deps, ok := reg.lookup("cic-module-oracle-cloud@0.1.0")
	require.True(t, ok)
	require.Len(t, deps.egress, 1)
	assert.Equal(t, "*.oraclecloud.com", deps.egress[0].Host)
	assert.Nil(t, deps.sign) // nil resolver → signer inert until OCI key provisioned
}

func TestCICFlowRegistry_RegisterVerified_BindsSigner(t *testing.T) {
	sg := &stubSigner{sig: "s"}
	reg := newCICFlowRegistry(func(h string) (signer, error) {
		assert.Equal(t, "oci/*", h)
		return sg, nil
	}, &stubDoer{})
	require.NoError(t, reg.registerVerified("m@1", []byte(ociProjectYAML)))
	deps, ok := reg.lookup("m@1")
	require.True(t, ok)
	assert.NotNil(t, deps.sign)
}

func TestCICFlowRegistry_RegisterVerified_NoCICFlowImports(t *testing.T) {
	const noFlow = `
metadata:
  buildHash: x
abi:
  imports:
    - module: git
      functions: [clone]
`
	reg := newCICFlowRegistry(nil, &stubDoer{})
	require.NoError(t, reg.registerVerified("plain@1", []byte(noFlow)))
	_, ok := reg.lookup("plain@1")
	assert.False(t, ok) // nothing bound — module doesn't use cic-flow
}

func TestCICFlowRegistry_RegisterVerified_OverDeclared(t *testing.T) {
	// imports cic-flow.sign but declares no sign secret → rejected.
	const bad = `
abi:
  imports:
    - module: cic-flow
      functions: [sign]
capability_manifest:
  egress:
    - host: "*.oraclecloud.com"
`
	reg := newCICFlowRegistry(nil, &stubDoer{})
	err := reg.registerVerified("bad@1", []byte(bad))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ops:[sign]")
	_, ok := reg.lookup("bad@1")
	assert.False(t, ok)
}

func TestCICFlowRegistry_RegisterVerified_BadYAML(t *testing.T) {
	reg := newCICFlowRegistry(nil, &stubDoer{})
	assert.Error(t, reg.registerVerified("x@1", []byte("\tnot: [yaml")))
}

func TestCICFlowRegistry_RegisterVerified_ResolverError(t *testing.T) {
	reg := newCICFlowRegistry(func(string) (signer, error) {
		return nil, errors.New("vault down")
	}, &stubDoer{})
	err := reg.registerVerified("m@1", []byte(ociProjectYAML))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vault down")
}
