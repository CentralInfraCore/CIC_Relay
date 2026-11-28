package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"centralrelay/pkg/trustbundle"
)

// The operator's trust-bundle configuration (INT-12). The pin, the minimum version and the age limit are the operator's
// trust domain; none of them is read from the bundle.
const (
	// trustBundleEnv is the path of the bundle file.
	trustBundleEnv = "CIC_TRUST_BUNDLE"
	// trustBundlePinEnv is the sha256:<hex> of the exact bundle bytes (required).
	trustBundlePinEnv = "CIC_TRUST_BUNDLE_PIN"
	// trustBundleMinVerEnv is minimum_bundle_version (optional, default 0).
	trustBundleMinVerEnv = "CIC_TRUST_BUNDLE_MIN_VERSION"
	// trustBundleMaxAgeEnv is a Go duration such as 720h (optional: no age limit).
	trustBundleMaxAgeEnv = "CIC_TRUST_BUNDLE_MAX_AGE"
	// trustBundleStateEnv is the path of the persisted high-water mark (required).
	trustBundleStateEnv = "CIC_TRUST_BUNDLE_STATE"
)

// loadTrustBundle admits the operator-pinned trust bundle into pkiAssets. Unset (CIC_TRUST_BUNDLE empty): nothing
// changes and the standalone authority file (CIC_MODULE_SIGN_AUTHORITIES) stays the source. Set: the pin and the state
// path are required, the standalone authority file must not be set (two sources of authority are one too many), the
// bundle must pass every admission check, and only then is the high-water mark advanced and persisted. Any failure is an
// error and the caller stops the start-up.
func loadTrustBundle(pkiAssets *PKIAssets, logger *logrus.Logger, now time.Time) error {
	path := strings.TrimSpace(os.Getenv(trustBundleEnv))
	if path == "" {
		return nil
	}
	if strings.TrimSpace(os.Getenv(moduleSignAuthoritiesEnv)) != "" {
		return fmt.Errorf("%s and %s are both set: the trust bundle is the only source of authority bindings", trustBundleEnv, moduleSignAuthoritiesEnv)
	}
	pol := trustbundle.Policy{PinnedDigest: strings.TrimSpace(os.Getenv(trustBundlePinEnv))}
	if pol.PinnedDigest == "" {
		return fmt.Errorf("%s is set but %s is not: an unpinned bundle authenticates nothing", trustBundleEnv, trustBundlePinEnv)
	}
	statePath := strings.TrimSpace(os.Getenv(trustBundleStateEnv))
	if statePath == "" {
		return fmt.Errorf("%s is set but %s is not: anti-rollback needs a persisted high-water mark", trustBundleEnv, trustBundleStateEnv)
	}
	if v := strings.TrimSpace(os.Getenv(trustBundleMinVerEnv)); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", trustBundleMinVerEnv, err)
		}
		pol.MinVersion = n
	}
	if v := strings.TrimSpace(os.Getenv(trustBundleMaxAgeEnv)); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s must be a positive duration", trustBundleMaxAgeEnv)
		}
		pol.MaxAge = d
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied trust-domain path
	if err != nil {
		return fmt.Errorf("read trust bundle %q: %w", path, err)
	}
	unlock, err := trustbundle.Lock(statePath)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := trustbundle.LoadState(statePath)
	if err != nil {
		return err
	}
	adm, err := trustbundle.Admit(raw, pol, st, now)
	if err != nil {
		return err
	}
	if _, err := trustbundle.Commit(statePath, st, adm); err != nil {
		return err
	}
	pkiAssets.ModuleSignAuthorities = adm.ModuleSign
	pkiAssets.AcceptedProfiles = adm.Profiles
	pkiAssets.ProfileSignAuthorities = adm.ProfileSign
	pkiAssets.VerifierAttestAuthorities = adm.VerifierAttest
	pkiAssets.RecorderAttestAuthorities = adm.RecorderAttest
	logger.WithFields(logrus.Fields{"bundle_version": adm.Version, "digest": adm.Digest, "authorities": len(adm.ModuleSign), "profiles": len(adm.Profiles)}).
		Info("trust bundle admitted (digest-pinned, anti-rollback)")
	return nil
}
