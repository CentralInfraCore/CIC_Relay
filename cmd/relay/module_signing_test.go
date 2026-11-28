package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signingTrust runs the real start-up trust bootstrap for a mode and an opt-in value, and returns the test
// trust store carrying the AllowUnsignedModules decision that bootstrap made. The roots are the test PKI's
// so that a validly signed module can be verified; the decision under test is the one bootstrapTrust took.
func signingTrust(t *testing.T, mode, optIn string) *PKIAssets {
	t.Helper()
	t.Setenv("CIC_TRUST_MODE", mode)
	t.Setenv(allowUnsignedEnv, optIn)
	if mode == TrustModeProd {
		pemData, err := embeddedFS.ReadFile("embedded/pki/rootCA2026.pem")
		require.NoError(t, err)
		bundle := filepath.Join(t.TempDir(), "ca.pem")
		require.NoError(t, os.WriteFile(bundle, pemData, 0o600))
		t.Setenv("CIC_PLATFORM_CA_BUNDLE", bundle)
		t.Setenv("CIC_CRL_REQUIRED_ISSUERS", "none")
	}
	gotMode, boot, err := bootstrapTrust(embeddedFS, newDiscardLogger())
	require.NoError(t, err)
	require.Equal(t, mode, gotMode)

	a := sharedAssets(t)
	a.AllowUnsignedModules = boot.AllowUnsignedModules
	return a
}

// The decision table of #155, through the real bootstrap and both module-loading paths:
//
//	environment            unsigned module   bad signature or certificate
//	PROD                   DENY              DENY
//	PROD + opt-in          DENY              DENY
//	DEV (default)          DENY              DENY
//	DEV + explicit opt-in  ALLOW             DENY
func TestModuleSigning_DecisionTable(t *testing.T) {
	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	valid, err := fs.ReadFile(getSubFS(t, "pki/signers/sources/module"), "ee.pem")
	require.NoError(t, err)
	wrongEKU, err := fs.ReadFile(getSubFS(t, "pki/signers/negatives/client-only"), "ee.pem")
	require.NoError(t, err)

	type moduleCase struct {
		name string
		pem  []byte // nil: no signature file at all
		// allowed per environment, in the order of envs below
		allowed [4]bool
	}
	envs := []struct{ name, mode, optIn string }{
		{"PROD", TrustModeProd, ""},
		{"PROD + opt-in", TrustModeProd, "1"},
		{"DEV default", TrustModeDev, ""},
		{"DEV + opt-in", TrustModeDev, "1"},
	}
	garbageCertBlock := []byte("-----BEGIN CERTIFICATE-----\nYWJjZGVmZw==\n-----END CERTIFICATE-----\n")
	cases := []moduleCase{
		{"unsigned (no signature file)", nil, [4]bool{false, false, false, true}},
		{"signature file is not PEM", []byte("not a pem"), [4]bool{false, false, false, false}},
		{"PEM of the wrong type", []byte("-----BEGIN PRIVATE KEY-----\nYWJjZGVmZw==\n-----END PRIVATE KEY-----\n"), [4]bool{false, false, false, false}},
		{"certificate block that does not parse", garbageCertBlock, [4]bool{false, false, false, false}},
		{"empty signature file", []byte{}, [4]bool{false, false, false, false}},
		{"signer not in the trust store", generateForeignLeafPEM(t), [4]bool{false, false, false, false}},
		{"signer with the wrong key usage", wrongEKU, [4]bool{false, false, false, false}},
		{"validly signed", valid, [4]bool{true, true, true, true}},
	}

	for _, c := range cases {
		for i, e := range envs {
			for _, path := range []string{"embedded", "installed"} {
				t.Run(c.name+" / "+e.name+" / "+path, func(t *testing.T) {
					files, _ := buildModuleFS(t, wasm, c.pem) // the module is pki.test@1.0.0 = sharedTrustModuleID
					assets := signingTrust(t, e.mode, e.optIn)
					got := loadVia(t, path, files, assets)
					assert.Equal(t, c.allowed[i], got.registered, "%s in %s via %s", c.name, e.name, path)
				})
			}
		}
	}
}

func TestModuleSigning_AdmittedUnsignedModuleGetsNoCapability(t *testing.T) {
	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	files, _ := buildModuleFS(t, wasm, nil)
	assets := signingTrust(t, TrustModeDev, "1")
	require.True(t, assets.AllowUnsignedModules)
	for _, path := range []string{"embedded", "installed"} {
		got := loadVia(t, path, files, assets)
		assert.Empty(t, got.bound, "an unsigned module admitted by the opt-in is bound to no capability (%s)", path)
	}
}

func TestAllowUnsignedModulesFromEnv(t *testing.T) {
	newLog := func() (*logrus.Logger, *bytes.Buffer) {
		var b bytes.Buffer
		l := logrus.New()
		l.SetOutput(&b)
		return l, &b
	}
	for _, tc := range []struct {
		mode, value string
		want        bool
		wantErr     bool
		wantWarn    string
	}{
		{TrustModeDev, "", false, false, ""},
		{TrustModeDev, "0", false, false, ""},
		{TrustModeDev, "1", true, false, "WITHOUT a signature file"},
		{TrustModeProd, "", false, false, ""},
		{TrustModeProd, "0", false, false, ""},
		{TrustModeProd, "1", false, false, "ignored in prod mode"},
		// anything else is a start-up error, in either mode: a typo must not open an exception
		{TrustModeDev, "true", false, true, ""},
		{TrustModeDev, "yes", false, true, ""},
		{TrustModeDev, "2", false, true, ""},
		{TrustModeDev, " 1", false, true, ""},
		{TrustModeDev, "1 ", false, true, ""},
		{TrustModeDev, "TRUE", false, true, ""},
		{TrustModeProd, "true", false, true, ""},
	} {
		t.Run(tc.mode+"="+tc.value, func(t *testing.T) {
			t.Setenv(allowUnsignedEnv, tc.value)
			l, buf := newLog()
			got, err := allowUnsignedModulesFromEnv(tc.mode, l)
			if tc.wantErr {
				require.Error(t, err)
				assert.False(t, got)
				assert.Contains(t, err.Error(), allowUnsignedEnv)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			if tc.wantWarn != "" {
				assert.Contains(t, buf.String(), tc.wantWarn)
			}
		})
	}
}

// A bad opt-in value stops the relay at start-up, before any module is read.
func TestBootstrapTrust_RejectsABadUnsignedOptIn(t *testing.T) {
	t.Setenv("CIC_TRUST_MODE", TrustModeDev)
	t.Setenv(allowUnsignedEnv, "true")
	_, _, err := bootstrapTrust(embeddedFS, newDiscardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), allowUnsignedEnv)
}
