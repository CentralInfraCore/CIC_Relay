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

const (
	TrustModeDev  = "dev"
	TrustModeProd = "prod" // scaffold: CICPlatformCA not embedded — out-of-band provisioning required
)

// resolveTrustMode reads the CIC_TRUST_MODE environment variable and returns the trust mode (dev or prod).
// Missing or empty values default to "dev" with a warning log.
func resolveTrustMode(logger *logrus.Logger) string {
	mode := strings.TrimSpace(os.Getenv("CIC_TRUST_MODE"))
	if mode == "" {
		logger.WithFields(logrus.Fields{
			"trust_mode": TrustModeDev,
			"reason":     "CIC_TRUST_MODE not set — defaulting to dev",
		}).Warn("trust mode defaulting to dev")
		return TrustModeDev
	}
	return mode
}

// bootstrapPKI loads the CA trust store based on trustMode.
//
// dev  → loads CICDeveloperCA + rootCA from embeddedPKI; TrustStoreLoaded=true
// prod → scaffold: CICPlatformCA not embedded; returns empty pool with WARN
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
