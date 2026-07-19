package main

import (
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"centralrelay/core/cabinet"
)

// R3b (feature-011, r3-plan.md): the cic-flow trust anchors are delivered as
// DETACHED companions — separate PEM files, never embedded in the relay binary —
// so signing them after the build does not perturb the binary's hash (the
// self-reference problem). CICSourceCA arrives alongside the binary
// (CIC_SOURCE_CA_BUNDLE); CICLicenseCA comes from the license
// (CIC_LICENSE_CA_BUNDLE). Both are optional at startup; when absent, cic-flow is
// fail-closed (no provenance → no capability).

// loadCompanionCAPool reads a detached PEM CA bundle into a cert pool.
func loadCompanionCAPool(path string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(path) // #nosec G304 — operator-supplied trust anchor path
	if err != nil {
		return nil, fmt.Errorf("read CA bundle %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("no valid certificates in %q", path)
	}
	return pool, nil
}

// loadProvenanceAnchors loads the detached cic-flow anchors into pkiAssets:
// CICSourceCA from CIC_SOURCE_CA_BUNDLE (provenance) and CICLicenseCA from
// CIC_LICENSE_CA_BUNDLE (entitlement). Each is optional; a missing var leaves its
// anchor unloaded (fail-closed). A configured-but-unreadable bundle is an error.
func loadProvenanceAnchors(pkiAssets *PKIAssets, logger *logrus.Logger) error {
	if p := strings.TrimSpace(os.Getenv("CIC_SOURCE_CA_BUNDLE")); p != "" {
		pool, err := loadCompanionCAPool(p)
		if err != nil {
			return fmt.Errorf("load CICSourceCA companion: %w", err)
		}
		pkiAssets.SourceRoots = pool
		pkiAssets.SourceLoaded = true
		logger.WithFields(logrus.Fields{"anchor": "CICSourceCA", "bundle_path": p}).Info("cic-flow provenance anchor loaded (detached companion)")
	} else {
		logger.WithField("anchor", "CICSourceCA").Warn("cic-flow provenance anchor not configured — cic-flow capabilities fail-closed")
	}

	if p := strings.TrimSpace(os.Getenv("CIC_LICENSE_CA_BUNDLE")); p != "" {
		pool, err := loadCompanionCAPool(p)
		if err != nil {
			return fmt.Errorf("load CICLicenseCA companion: %w", err)
		}
		pkiAssets.LicenseRoots = pool
		pkiAssets.LicenseLoaded = true
		logger.WithFields(logrus.Fields{"anchor": "CICLicenseCA", "bundle_path": p}).Info("cic-flow entitlement anchor loaded (detached companion)")
	}
	return nil
}

// verifyCICFlowProvenance checks a module's signing cert against the CICSourceCA
// provenance anchor (EKU=CodeSigning, CRL). It is the fail-closed gate for
// binding a cic-flow capability: no SourceCA loaded, or a cert that does not chain
// to it, means no capability. (Entitlement against CICLicenseCA is a separate
// axis; its per-module attestation format is pending — see r3-plan.md.)
func verifyCICFlowProvenance(cert *x509.Certificate, pkiAssets *PKIAssets) error {
	if pkiAssets == nil || !pkiAssets.SourceLoaded || pkiAssets.SourceRoots == nil {
		return fmt.Errorf("CICSourceCA provenance anchor not loaded")
	}
	_, err := cabinet.VerifyCertificateChain(cert, cabinet.VerifyOptions{
		Roots:         pkiAssets.SourceRoots,
		Intermediates: pkiAssets.Intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		CRLs:          pkiAssets.CRLs,
	})
	if err != nil {
		return fmt.Errorf("provenance verification failed: %w", err)
	}
	return nil
}
