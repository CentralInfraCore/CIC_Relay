package main

import (
	"crypto/x509"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	TrustModeDev  = "dev"
	TrustModeProd = "prod" // scaffold: CICPlatformCA not embedded — out-of-band provisioning required
)

// resolveTrustMode reads CIC_TRUST_MODE env var, defaults to "dev".
// Missing or empty value is treated as dev with a WARN log.
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
			"trust_mode":          trustMode,
			"trust_store_loaded":  assets.TrustStoreLoaded,
			"ca_anchor":           "CICDeveloperCA2026 / CICRootCA2026",
		}).Info("PKI trust store bootstrapped")
		return assets, nil

	case TrustModeProd:
		// scaffold: CICPlatformCA requires out-of-band provisioning (promotion from dev branch).
		// The prod relay tree is a closed ecosystem — the CA is not embedded here.
		logger.WithFields(logrus.Fields{
			"trust_mode": trustMode,
			"outcome":    "scaffold",
			"reason":     "CICPlatformCA not embedded — prod PKI requires out-of-band cert provisioning",
		}).Warn("PKI verification skipped in prod mode (scaffold)")
		return &PKIAssets{
			Roots:         x509.NewCertPool(),
			Intermediates: x509.NewCertPool(),
			CRLs:          []*x509.RevocationList{},
		}, nil

	default:
		return nil, fmt.Errorf("unknown CIC_TRUST_MODE: %q (valid: %q, %q)", trustMode, TrustModeDev, TrustModeProd)
	}
}
