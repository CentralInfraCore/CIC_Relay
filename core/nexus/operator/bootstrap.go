package operator

// NOTE: pre-PKI scaffold — HMAC-based token generation.
// PKI integration (CICRootCA → leaf cert) awaits M3 bootstrap.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"centralrelay/core/nexus/recorder"
	"centralrelay/core/nexus/types"
)

const bootstrapTokenTTL = 24 * time.Hour

// HostDefinition describes a host to be bootstrapped.
type HostDefinition struct {
	HostID   string
	Hostname string
	Labels   map[string]string
}

// BootstrapToken is a time-limited HMAC-SHA256 signed token for host registration.
type BootstrapToken struct {
	HostID    string
	Token     string // hex-encoded HMAC-SHA256
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Bootstrapper handles HMAC-based host token generation and validation.
// It is a pre-PKI scaffold: PKI wiring awaits M3 (CICRootCA bootstrap).
type Bootstrapper struct {
	secret   []byte
	recorder recorder.StateRecorder // nil = recording disabled
	logger   *logrus.Logger
}

// NewBootstrapper creates a Bootstrapper with the given HMAC secret.
// rec may be nil — recording is non-fatal when absent.
func NewBootstrapper(secret []byte, rec recorder.StateRecorder, logger *logrus.Logger) *Bootstrapper {
	return &Bootstrapper{secret: secret, recorder: rec, logger: logger}
}

// BootstrapHost generates a time-limited HMAC token for the given host
// and records the issuance event via the StateRecorder (if configured).
func (b *Bootstrapper) BootstrapHost(ctx context.Context, hostDef HostDefinition) (*BootstrapToken, error) {
	if hostDef.HostID == "" {
		return nil, fmt.Errorf("HostID must not be empty")
	}
	now := time.Now().UTC()
	tok := &BootstrapToken{
		HostID:    hostDef.HostID,
		IssuedAt:  now,
		ExpiresAt: now.Add(bootstrapTokenTTL),
		Token:     computeHMAC(b.secret, hostDef.HostID, now),
	}

	b.logger.WithFields(logrus.Fields{
		"host_id":    hostDef.HostID,
		"expires_at": tok.ExpiresAt,
	}).Info("bootstrap token issued")

	b.recordIssuance(ctx, tok)
	return tok, nil
}

// ValidateToken verifies that the token has not expired and the HMAC is correct.
func (b *Bootstrapper) ValidateToken(tok *BootstrapToken) error {
	if tok == nil {
		return fmt.Errorf("token is nil")
	}
	if time.Now().UTC().After(tok.ExpiresAt) {
		return fmt.Errorf("bootstrap token expired for host %s (expired %s)", tok.HostID, tok.ExpiresAt)
	}
	expected := computeHMAC(b.secret, tok.HostID, tok.IssuedAt)
	if !hmac.Equal([]byte(expected), []byte(tok.Token)) {
		return fmt.Errorf("invalid bootstrap token for host %s", tok.HostID)
	}
	return nil
}

// computeHMAC returns hex(HMAC-SHA256(secret, hostID|issuedAt)).
func computeHMAC(secret []byte, hostID string, issuedAt time.Time) string {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(hostID + "|" + issuedAt.Format(time.RFC3339Nano)))
	return hex.EncodeToString(h.Sum(nil))
}

// recordIssuance writes the token issuance to the audit recorder.
// Non-fatal: logs WARN if recorder is absent or fails.
func (b *Bootstrapper) recordIssuance(ctx context.Context, tok *BootstrapToken) {
	if b.recorder == nil {
		b.logger.Warn("bootstrap recorder not configured — token issuance not audited")
		return
	}
	expected := types.ExpectedState{
		RunID:     "bootstrap-" + tok.HostID,
		Timestamp: tok.IssuedAt,
	}
	if _, err := b.recorder.RecordState(ctx, expected, tok); err != nil {
		b.logger.WithError(err).Warn("failed to record bootstrap token issuance")
	}
}