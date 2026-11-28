package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/pkg/trustbundle"
)

const tbKey = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
const tbProfile = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func tbBundle(version string) string {
	return `{"kind":"CICTrustBundle","bundle_version":` + version + `,"issued_at":"2026-11-28T10:00:00Z",` +
		`"authorities":[{"key_id":"` + tbKey + `","roles":["module:sign"]}],` +
		`"accepted_profiles":[{"id":"oci-1","digest":"` + tbProfile + `"}]}`
}

type tbEnv struct{ bundle, state string }

// tbSetup writes a bundle and configures the environment; the pin is that of the written bytes.
func tbSetup(t *testing.T, content string) tbEnv {
	t.Helper()
	dir := t.TempDir()
	e := tbEnv{bundle: filepath.Join(dir, "bundle.json"), state: filepath.Join(dir, "state.json")}
	require.NoError(t, os.WriteFile(e.bundle, []byte(content), 0o600))
	t.Setenv(trustBundleEnv, e.bundle)
	t.Setenv(trustBundlePinEnv, trustbundle.Digest([]byte(content)))
	t.Setenv(trustBundleStateEnv, e.state)
	t.Setenv(trustBundleMinVerEnv, "")
	t.Setenv(trustBundleMaxAgeEnv, "")
	t.Setenv(moduleSignAuthoritiesEnv, "")
	return e
}

var tbNow = time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

func tbLoad() (*PKIAssets, error) {
	a := &PKIAssets{}
	return a, loadTrustBundle(a, logrus.New(), tbNow)
}

func TestTrustBundleUnsetChangesNothing(t *testing.T) {
	t.Setenv(trustBundleEnv, "")
	a, err := tbLoad()
	require.NoError(t, err)
	assert.Nil(t, a.ModuleSignAuthorities)
	assert.Nil(t, a.AcceptedProfiles)
}

func TestTrustBundleFeedsTheAuthorityBindingsAndTheProfilePins(t *testing.T) {
	e := tbSetup(t, tbBundle("5"))
	a, err := tbLoad()
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{tbKey: true}, a.ModuleSignAuthorities)
	assert.Equal(t, []trustbundle.Profile{{ID: "oci-1", Digest: tbProfile}}, a.AcceptedProfiles)
	st, err := trustbundle.LoadState(e.state)
	require.NoError(t, err)
	assert.EqualValues(t, 5, st.HighWatermark, "the high-water mark is persisted after admission")
}

// the admission feeds the real signer-authority decision: a key the bundle names is authorised, any other is not
func TestTrustBundleDrivesSignerAuthorized(t *testing.T) {
	tbSetup(t, tbBundle("5"))
	a, err := tbLoad()
	require.NoError(t, err)
	assert.True(t, signerAuthorized(a, tbKey))
	assert.False(t, signerAuthorized(a, "sha256:"+strings.Repeat("2", 64)))
}

func TestTrustBundleConfigurationErrorsStopTheStart(t *testing.T) {
	cases := map[string]func(t *testing.T, e tbEnv){
		"no pin":                 func(t *testing.T, e tbEnv) { t.Setenv(trustBundlePinEnv, "") },
		"no state path":          func(t *testing.T, e tbEnv) { t.Setenv(trustBundleStateEnv, "") },
		"both authority sources": func(t *testing.T, e tbEnv) { t.Setenv(moduleSignAuthoritiesEnv, "/x") },
		"bad minimum version":    func(t *testing.T, e tbEnv) { t.Setenv(trustBundleMinVerEnv, "five") },
		"bad max age":            func(t *testing.T, e tbEnv) { t.Setenv(trustBundleMaxAgeEnv, "soon") },
		"non-positive max age":   func(t *testing.T, e tbEnv) { t.Setenv(trustBundleMaxAgeEnv, "0s") },
		"unreadable bundle":      func(t *testing.T, e tbEnv) { t.Setenv(trustBundleEnv, e.bundle+".missing") },
		"corrupt state":          func(t *testing.T, e tbEnv) { require.NoError(t, os.WriteFile(e.state, []byte("x"), 0o600)) },
		"state cannot be written": func(t *testing.T, e tbEnv) {
			t.Setenv(trustBundleStateEnv, filepath.Join(filepath.Dir(e.state), "no", "state.json"))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := tbSetup(t, tbBundle("5"))
			mutate(t, e)
			a, err := tbLoad()
			require.Error(t, err)
			assert.Nil(t, a.ModuleSignAuthorities, "nothing is installed on a failure")
			assert.Nil(t, a.AcceptedProfiles)
		})
	}
}

func TestTrustBundleAdmissionFailuresCarryTheirCodes(t *testing.T) {
	t.Run("not pinned", func(t *testing.T) {
		tbSetup(t, tbBundle("5"))
		t.Setenv(trustBundlePinEnv, trustbundle.Digest([]byte("another bundle")))
		_, err := tbLoad()
		assert.Equal(t, trustbundle.CodeNotPinned, trustbundle.CodeOf(err))
	})
	t.Run("stale", func(t *testing.T) {
		tbSetup(t, tbBundle("5"))
		t.Setenv(trustBundleMinVerEnv, "6")
		_, err := tbLoad()
		assert.Equal(t, trustbundle.CodeStale, trustbundle.CodeOf(err))
	})
	t.Run("expired", func(t *testing.T) {
		tbSetup(t, tbBundle("5"))
		t.Setenv(trustBundleMaxAgeEnv, "1h")
		_, err := tbLoad()
		assert.Equal(t, trustbundle.CodeExpired, trustbundle.CodeOf(err))
	})
	t.Run("rollback across restarts", func(t *testing.T) {
		e := tbSetup(t, tbBundle("7"))
		_, err := tbLoad()
		require.NoError(t, err) // version 7 admitted, mark 7

		old := tbBundle("6") // an older, validly pinned bundle (the operator's pin pointing back, or a replaced file)
		require.NoError(t, os.WriteFile(e.bundle, []byte(old), 0o600))
		t.Setenv(trustBundlePinEnv, trustbundle.Digest([]byte(old)))
		a, err := tbLoad()
		assert.Equal(t, trustbundle.CodeRollback, trustbundle.CodeOf(err))
		assert.Nil(t, a.ModuleSignAuthorities)
		st, _ := trustbundle.LoadState(e.state)
		assert.EqualValues(t, 7, st.HighWatermark, "a refused bundle does not move the mark")
	})
}
