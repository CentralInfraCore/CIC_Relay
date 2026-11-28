package hostsign

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	modPrefix  = "centralrelay/"
	ociPackage = modPrefix + "pkg/hostsign/ociprofile"
	seamPkg    = modPrefix + "pkg/hostsign"
)

// depsOf returns the transitive (non-test) dependencies of the packages matching pattern,
// including the packages themselves, as import paths.
func depsOf(t *testing.T, pattern string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-buildvcs=false", "-deps", "-f", "{{.ImportPath}}", pattern)
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.Fields(string(out))
}

func has(deps []string, pkg string) bool {
	for _, d := range deps {
		if d == pkg || strings.HasPrefix(d, pkg+"/") {
			return true
		}
	}
	return false
}

// AC-SS-07 (D-11): the Relay core (core/...) and the generic seam (pkg/hostsign) do not import the
// provider-specific profile, directly or transitively.
func TestAC_SS07_CoreAndSeamDoNotImportProviderProfile(t *testing.T) {
	assert.False(t, has(depsOf(t, "./core/..."), ociPackage), "core/... must not depend on ociprofile")
	assert.False(t, has(depsOf(t, "./pkg/hostsign"), ociPackage), "pkg/hostsign must not depend on ociprofile")
}

// The OCI profile depends only on the seam and the standard library: no core/..., no cmd/....
func TestAC_SS07_ProviderProfileDoesNotImportCore(t *testing.T) {
	deps := depsOf(t, "./pkg/hostsign/ociprofile")
	assert.False(t, has(deps, modPrefix+"core"), "ociprofile must not depend on core/...")
	assert.False(t, has(deps, modPrefix+"cmd"), "ociprofile must not depend on cmd/...")
	for _, d := range deps {
		if strings.HasPrefix(d, modPrefix) {
			assert.Contains(t, []string{ociPackage, seamPkg}, d, "unexpected module-internal dependency of ociprofile")
		} else {
			assert.False(t, strings.Contains(strings.Split(d, "/")[0], "."), "non-stdlib dependency %s", d)
		}
	}
}

// The checks above can fail: the dependency tool does see the real edges.
func TestArchTestCanSeeEdges(t *testing.T) {
	assert.True(t, has(depsOf(t, "./pkg/hostsign/ociprofile"), seamPkg), "ociprofile -> hostsign edge must be visible")
	assert.True(t, has(depsOf(t, "./cmd/relay"), seamPkg), "cmd/relay -> hostsign edge must be visible once wired")
	assert.True(t, has(depsOf(t, "./cmd/relay"), "centralrelay/core/cabinet"), "cmd/relay -> core edge must be visible")
}
