package operator

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/core/nexus/types"
)

// mockStateRecorder records calls for assertions.
type mockStateRecorder struct {
	called bool
	err    error
}

func (m *mockStateRecorder) RecordState(_ context.Context, _ types.ExpectedState, _ interface{}) (string, error) {
	m.called = true
	return "abc123", m.err
}

func silentLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(&bytes.Buffer{})
	return l
}

func TestBootstrapHost_Valid(t *testing.T) {
	rec := &mockStateRecorder{}
	b := NewBootstrapper([]byte("test-secret"), rec, silentLogger())

	tok, err := b.BootstrapHost(context.Background(), HostDefinition{
		HostID:   "host-001",
		Hostname: "node1.example.com",
	})

	require.NoError(t, err)
	require.NotNil(t, tok)
	assert.Equal(t, "host-001", tok.HostID)
	assert.NotEmpty(t, tok.Token)
	assert.True(t, tok.ExpiresAt.After(tok.IssuedAt))
	assert.True(t, rec.called, "recorder should have been called")
}

func TestBootstrapHost_EmptyHostID(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	tok, err := b.BootstrapHost(context.Background(), HostDefinition{})
	assert.Nil(t, tok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HostID")
}

func TestBootstrapHost_NilRecorder(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	tok, err := b.BootstrapHost(context.Background(), HostDefinition{HostID: "host-002"})
	require.NoError(t, err)
	assert.NotNil(t, tok, "token issued even without recorder")
}

func TestBootstrapHost_RecorderError_NonFatal(t *testing.T) {
	rec := &mockStateRecorder{err: assert.AnError}
	b := NewBootstrapper([]byte("secret"), rec, silentLogger())
	tok, err := b.BootstrapHost(context.Background(), HostDefinition{HostID: "host-003"})
	require.NoError(t, err, "recorder failure must not fail BootstrapHost")
	assert.NotNil(t, tok)
}

func TestBootstrapToken_TTL(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	tok, _ := b.BootstrapHost(context.Background(), HostDefinition{HostID: "host-ttl"})
	require.NotNil(t, tok)
	assert.WithinDuration(t, tok.IssuedAt.Add(bootstrapTokenTTL), tok.ExpiresAt, time.Second)
}

func TestValidateToken_Valid(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	tok, _ := b.BootstrapHost(context.Background(), HostDefinition{HostID: "host-ok"})
	require.NotNil(t, tok)
	assert.NoError(t, b.ValidateToken(tok))
}

func TestValidateToken_Expired(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	tok := &BootstrapToken{
		HostID:    "host-exp",
		IssuedAt:  time.Now().UTC().Add(-48 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(-24 * time.Hour),
		Token:     computeHMAC([]byte("secret"), "host-exp", time.Now().UTC().Add(-48*time.Hour)),
	}
	err := b.ValidateToken(tok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestValidateToken_InvalidHMAC(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	tok := &BootstrapToken{
		HostID:    "host-bad",
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(bootstrapTokenTTL),
		Token:     "deadbeef",
	}
	err := b.ValidateToken(tok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid bootstrap token")
}

func TestValidateToken_Nil(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	assert.Error(t, b.ValidateToken(nil))
}

func TestComputeHMAC_Deterministic(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h1 := computeHMAC([]byte("key"), "host-x", ts)
	h2 := computeHMAC([]byte("key"), "host-x", ts)
	assert.Equal(t, h1, h2)
}

func TestComputeHMAC_DifferentSecrets(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h1 := computeHMAC([]byte("key1"), "host-x", ts)
	h2 := computeHMAC([]byte("key2"), "host-x", ts)
	assert.NotEqual(t, h1, h2)
}