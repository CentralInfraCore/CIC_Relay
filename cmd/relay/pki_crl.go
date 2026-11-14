package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"centralrelay/core/cabinet"
)

// crlPolicy decides what a bad CRL means. Strict (prod): a CRL that cannot be parsed,
// whose signature does not verify against a trusted issuer, or that is stale, is a
// start-up error. Lenient (dev): such a CRL is logged and not used. Either way a CRL
// with a bad signature is never used.
type crlPolicy struct {
	Strict    bool
	Freshness *cabinet.CRLFreshness // enforced in strict mode and carried to verification
}

// acceptCRLs returns the CRLs that may be used. Each one needs a valid signature from
// one of the issuers. Freshness is enforced in strict mode; lenient mode only warns.
func acceptCRLs(candidates []*x509.RevocationList, issuers []*x509.Certificate, p crlPolicy, logger *logrus.Logger) ([]*x509.RevocationList, error) {
	accepted := []*x509.RevocationList{}
	for _, crl := range candidates {
		if err := cabinet.VerifyCRLSignature(crl, issuers); err != nil {
			if p.Strict {
				return nil, fmt.Errorf("pki bootstrap: %w", err)
			}
			logger.WithError(err).Warn("CRL rejected: its entries are not used")
			continue
		}
		fresh := p.Freshness
		if fresh == nil {
			fresh = &cabinet.CRLFreshness{}
		}
		if err := cabinet.CheckCRLFresh(crl, fresh); err != nil {
			if p.Strict {
				return nil, fmt.Errorf("pki bootstrap: %w", err)
			}
			logger.WithError(err).Warn("CRL is stale, still used (dev mode)")
		}
		accepted = append(accepted, crl)
	}
	return accepted, nil
}

// parseBundleCRLs reads the certificates and CRLs of a PEM bundle. In strict mode a CRL
// block that does not parse is an error; in lenient mode it is skipped with a warning.
// Certificates that do not parse are ignored (the trust pool ignores them as well).
func parseBundleCRLs(pemData []byte, p crlPolicy, logger *logrus.Logger) (issuers []*x509.Certificate, crls []*x509.RevocationList, err error) {
	rest := pemData
	for len(rest) > 0 {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			if cert, perr := x509.ParseCertificate(block.Bytes); perr == nil && cert.IsCA {
				issuers = append(issuers, cert)
			}
		case "X509 CRL":
			crl, perr := x509.ParseRevocationList(block.Bytes)
			if perr != nil {
				if p.Strict {
					return nil, nil, fmt.Errorf("pki bootstrap: unparsable CRL in the CA bundle: %w", perr)
				}
				logger.WithError(perr).Warn("Failed to parse CRL")
				continue
			}
			crls = append(crls, crl)
		}
	}
	return issuers, crls, nil
}

// crlToleranceFromEnv reads CIC_CRL_MAX_STALENESS, the grace after a CRL's NextUpdate.
// Unset or empty means no grace; a value that is not a non-negative duration is an error.
func crlToleranceFromEnv() (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv("CIC_CRL_MAX_STALENESS"))
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid CIC_CRL_MAX_STALENESS %q (want a non-negative duration such as \"30m\")", v)
	}
	return d, nil
}
