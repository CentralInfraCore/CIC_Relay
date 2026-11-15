package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
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

// crlIssuerPolicy is the operator's explicit declaration of which issuers must have a CRL.
// Trust is declared, not assumed: in prod the declaration is mandatory, and "no issuer
// requires a CRL" has to be written down as such (none).
type crlIssuerPolicy struct {
	none         bool            // explicitly: no issuer requires a CRL
	all          bool            // every CA certificate of the bundle requires a CRL
	fingerprints map[string]bool // sha256 (lower-case hex) of the DER of each required issuer certificate
}

// crlIssuerPolicyFromEnv reads CIC_CRL_REQUIRED_ISSUERS: "none", "all", or a comma-separated
// list of CA certificate SHA-256 fingerprints (hex, an optional "sha256:" prefix, colons
// allowed). Unset or empty is an error in prod: the policy must be declared.
func crlIssuerPolicyFromEnv() (crlIssuerPolicy, error) {
	v := strings.TrimSpace(os.Getenv("CIC_CRL_REQUIRED_ISSUERS"))
	switch strings.ToLower(v) {
	case "":
		return crlIssuerPolicy{}, fmt.Errorf("CIC_CRL_REQUIRED_ISSUERS is not set: declare which issuers require a CRL " +
			`("none", "all", or a comma-separated list of CA certificate sha256 fingerprints)`)
	case "none":
		return crlIssuerPolicy{none: true}, nil
	case "all":
		return crlIssuerPolicy{all: true}, nil
	}
	p := crlIssuerPolicy{fingerprints: map[string]bool{}}
	for _, raw := range strings.Split(v, ",") {
		fp := strings.ToLower(strings.TrimSpace(raw))
		fp = strings.ReplaceAll(strings.TrimPrefix(fp, "sha256:"), ":", "")
		if len(fp) != 64 || strings.Trim(fp, "0123456789abcdef") != "" {
			return crlIssuerPolicy{}, fmt.Errorf("invalid CIC_CRL_REQUIRED_ISSUERS entry %q (want a 64-digit hex sha256 fingerprint, \"none\" or \"all\")", raw)
		}
		p.fingerprints[fp] = true
	}
	return p, nil
}

func certFingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

// enforceCRLIssuerPolicy checks the declared requirement against the CRLs that were accepted
// (authenticated and fresh): every required issuer needs one CRL issued by that very
// certificate. A required issuer without one (missing) is an error, and so is a declared
// fingerprint that matches no CA of the bundle (a typo must not silently drop a requirement).
// An empty but valid, fresh CRL satisfies the requirement.
func enforceCRLIssuerPolicy(p crlIssuerPolicy, caCerts []*x509.Certificate, accepted []*x509.RevocationList) error {
	if p.none {
		return nil
	}
	matched := map[string]bool{}
	for _, ca := range caCerts {
		fp := certFingerprint(ca)
		if !p.all && !p.fingerprints[fp] {
			continue
		}
		matched[fp] = true
		covered := false
		for _, crl := range accepted {
			if bytes.Equal(crl.RawIssuer, ca.RawSubject) && crl.CheckSignatureFrom(ca) == nil {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("pki bootstrap: issuer %s (sha256:%s) requires a CRL, but the bundle has no valid, fresh CRL issued by it", ca.Subject, fp)
		}
	}
	for fp := range p.fingerprints {
		if !matched[fp] {
			return fmt.Errorf("pki bootstrap: CIC_CRL_REQUIRED_ISSUERS names sha256:%s, which is no CA certificate of the bundle", fp)
		}
	}
	if p.all && len(matched) == 0 {
		return fmt.Errorf("pki bootstrap: CIC_CRL_REQUIRED_ISSUERS=all but the bundle contains no CA certificate")
	}
	return nil
}
