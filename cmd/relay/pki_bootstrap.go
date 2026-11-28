package main

import (
	"crypto/x509"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"centralrelay/core/cabinet"
)

// The values of CIC_TRUST_MODE. There is no default: an unset or unknown mode is a start-up error.
const (
	// TrustModeDev trusts the development CAs embedded in the binary (embedded/pki) and is the only
	// mode in which CIC_ALLOW_UNSIGNED_MODULES=1 is honoured.
	TrustModeDev = "dev"
	// TrustModeProd trusts only the CA bundle provisioned out-of-band at CIC_PLATFORM_CA_BUNDLE (the
	// CICPlatformCA is not embedded), with strict CRL handling.
	TrustModeProd = "prod" // CICPlatformCA is not embedded: provisioned out-of-band (CIC_PLATFORM_CA_BUNDLE)
)

// resolveTrustMode reads the CIC_TRUST_MODE environment variable and returns the trust mode.
// There is no default: a missing, empty or whitespace-only value is an error, so the
// operator always chooses the trust anchor explicitly instead of inheriting the
// development CAs embedded in the binary. Unknown values are rejected by bootstrapPKI.
func resolveTrustMode() (string, error) {
	mode := strings.TrimSpace(os.Getenv("CIC_TRUST_MODE"))
	if mode == "" {
		return "", fmt.Errorf("CIC_TRUST_MODE is not set (valid: %q, %q); there is no default", TrustModeDev, TrustModeProd)
	}
	return mode, nil
}

// requireTrustStore refuses a prod relay without a loaded trust store. bootstrapPKI
// already fails in that case today; this is the guard that keeps a later change from
// reintroducing the silent "trust ecosystem not bootstrapped" skip in loadModule.
func requireTrustStore(trustMode string, assets *PKIAssets) error {
	if trustMode == TrustModeProd && (assets == nil || !assets.TrustStoreLoaded) {
		return fmt.Errorf("CIC_TRUST_MODE=%s requires a loaded trust store", TrustModeProd)
	}
	return nil
}

// allowUnsignedEnv is the explicit operator opt-in for modules without a signature file. It exists in dev mode
// only; in prod it is ignored. It never admits a module whose signature is present but bad.
const allowUnsignedEnv = "CIC_ALLOW_UNSIGNED_MODULES"

// allowUnsignedModulesFromEnv decides whether unsigned modules are admitted. Unset, empty or "0" means no;
// exactly "1" means yes in dev mode and is ignored (with a warning) in prod; any other value is a start-up error,
// so that a typo can not open an exception.
func allowUnsignedModulesFromEnv(trustMode string, logger *logrus.Logger) (bool, error) {
	v := os.Getenv(allowUnsignedEnv)
	switch v {
	case "", "0":
		return false, nil
	case "1":
		if trustMode != TrustModeDev {
			logger.Warnf("%s=1 is ignored in %s mode: unsigned modules are refused", allowUnsignedEnv, trustMode)
			return false, nil
		}
		logger.Warnf("%s=1: modules WITHOUT a signature file are admitted in dev mode (they get no capability); a bad signature is still refused", allowUnsignedEnv)
		return true, nil
	default:
		return false, fmt.Errorf("%s must be unset, 0 or 1 (got %q)", allowUnsignedEnv, v)
	}
}

// bootstrapTrust is the single start-up entry for trust: it resolves the mode,
// loads the trust store and applies the prod guard.
func bootstrapTrust(embeddedPKI fs.FS, logger *logrus.Logger) (string, *PKIAssets, error) {
	trustMode, err := resolveTrustMode()
	if err != nil {
		return "", nil, err
	}
	assets, err := bootstrapPKI(trustMode, embeddedPKI, logger)
	if err != nil {
		return "", nil, err
	}
	if err := requireTrustStore(trustMode, assets); err != nil {
		return "", nil, err
	}
	allow, err := allowUnsignedModulesFromEnv(trustMode, logger)
	if err != nil {
		return "", nil, err
	}
	assets.AllowUnsignedModules = allow
	assets.TrustMode = trustMode
	return trustMode, assets, nil
}

// bootstrapPKI loads the CA trust store based on trustMode.
//
// dev  → loads CICDeveloperCA + rootCA from embeddedPKI; TrustStoreLoaded=true
// prod → loads the CA bundle (certificates and CRLs) named by CIC_PLATFORM_CA_BUNDLE; TrustStoreLoaded=true
//
// In prod a missing or unreadable bundle, a bundle without certificates, a bad or stale CRL,
// or an unmet CIC_CRL_REQUIRED_ISSUERS policy is an error.
//
// Any other value returns an error.
func bootstrapPKI(trustMode string, embeddedPKI fs.FS, logger *logrus.Logger) (*PKIAssets, error) {
	switch trustMode {
	case TrustModeDev:
		assets, err := loadPKIAssets(embeddedPKI, "embedded/pki", logger)
		if err != nil {
			return nil, fmt.Errorf("pki bootstrap (dev): %w", err)
		}
		logger.WithFields(logrus.Fields{
			"trust_mode":         trustMode,
			"trust_store_loaded": assets.TrustStoreLoaded,
			"ca_anchor":          "CICDeveloperCA2026 / CICRootCA2026",
		}).Info("PKI trust store bootstrapped")
		return assets, nil

	case TrustModeProd:
		bundlePath := strings.TrimSpace(os.Getenv("CIC_PLATFORM_CA_BUNDLE"))
		if bundlePath == "" {
			return nil, fmt.Errorf("CIC_TRUST_MODE=prod requires CIC_PLATFORM_CA_BUNDLE (path to CA PEM bundle)")
		}
		pemData, err := os.ReadFile(bundlePath)
		if err != nil {
			return nil, fmt.Errorf("pki bootstrap (prod): read CA bundle %q: %w", bundlePath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("pki bootstrap (prod): no valid certificates in %q", bundlePath)
		}
		// CRLs travel in the same bundle (X509 CRL blocks). In prod a CRL that cannot be
		// parsed, is not signed by a CA of the bundle, or is stale is a start-up error.
		tolerance, err := crlToleranceFromEnv()
		if err != nil {
			return nil, fmt.Errorf("pki bootstrap (prod): %w", err)
		}
		policy := crlPolicy{Strict: true, Freshness: &cabinet.CRLFreshness{Tolerance: tolerance}}
		issuers, candidateCRLs, err := parseBundleCRLs(pemData, policy, logger)
		if err != nil {
			return nil, fmt.Errorf("pki bootstrap (prod): %w", err)
		}
		crls, err := acceptCRLs(candidateCRLs, issuers, policy, logger)
		if err != nil {
			return nil, fmt.Errorf("pki bootstrap (prod): %w", err)
		}
		// The operator declares which issuers must have a CRL; a required issuer without a
		// valid, fresh one is a start-up error. dev (the embedded loader) has no such policy:
		// it stays lenient and ignores CIC_CRL_REQUIRED_ISSUERS.
		issuerPolicy, err := crlIssuerPolicyFromEnv()
		if err != nil {
			return nil, fmt.Errorf("pki bootstrap (prod): %w", err)
		}
		if err := enforceCRLIssuerPolicy(issuerPolicy, issuers, crls); err != nil {
			return nil, fmt.Errorf("pki bootstrap (prod): %w", err)
		}
		if issuerPolicy.none {
			logger.Warn("CIC_CRL_REQUIRED_ISSUERS=none: no issuer is required to have a CRL")
		}
		if len(crls) == 0 {
			logger.WithField("bundle_path", bundlePath).Warn("Revocation checking is inactive: the CA bundle contains no CRL")
		}
		// No pin source exists yet: pinning is off, and in prod that is stated, not implicit.
		logger.WithField("spki_pins", 0).Warn("SPKI pinning is not configured: the pin set is empty")
		logger.WithFields(logrus.Fields{
			"trust_mode":         trustMode,
			"trust_store_loaded": true,
			"bundle_path":        bundlePath,
			"crls":               len(crls),
		}).Info("PKI trust store bootstrapped (prod)")
		return &PKIAssets{
			Roots:            pool,
			Intermediates:    x509.NewCertPool(),
			CRLs:             crls,
			CRLFreshness:     policy.Freshness,
			TrustStoreLoaded: true,
		}, nil

	default:
		return nil, fmt.Errorf("unknown CIC_TRUST_MODE: %q (valid: %q, %q)", trustMode, TrustModeDev, TrustModeProd)
	}
}
