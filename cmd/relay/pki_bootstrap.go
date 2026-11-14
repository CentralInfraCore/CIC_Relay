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
	return trustMode, assets, nil
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
		logger.WithFields(logrus.Fields{
			"trust_mode":         trustMode,
			"trust_store_loaded": true,
			"bundle_path":        bundlePath,
		}).Info("PKI trust store bootstrapped (prod)")
		return &PKIAssets{
			Roots:            pool,
			Intermediates:    x509.NewCertPool(),
			CRLs:             []*x509.RevocationList{},
			TrustStoreLoaded: true,
		}, nil

	default:
		return nil, fmt.Errorf("unknown CIC_TRUST_MODE: %q (valid: %q, %q)", trustMode, TrustModeDev, TrustModeProd)
	}
}
