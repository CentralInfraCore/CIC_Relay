package operator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLifecycleManager(rec *mockStateRecorder) (*Bootstrapper, *LifecycleManager) {
	// Avoid the nil-interface gotcha: pass a typed nil as a nil StateRecorder interface.
	var b *Bootstrapper
	var lm *LifecycleManager
	reg := NewHostRegistry()
	if rec != nil {
		b = NewBootstrapper([]byte("test-secret"), rec, silentLogger())
		lm = NewLifecycleManager(b, reg, rec, silentLogger())
	} else {
		b = NewBootstrapper([]byte("test-secret"), nil, silentLogger())
		lm = NewLifecycleManager(b, reg, nil, silentLogger())
	}
	return b, lm
}

func TestOnboardHost_Valid(t *testing.T) {
	rec := &mockStateRecorder{}
	b, lm := newTestLifecycleManager(rec)

	tok, err := b.BootstrapHost(context.Background(), HostDefinition{HostID: "host-A"})
	require.NoError(t, err)

	err = lm.OnboardHost(context.Background(), tok)
	assert.NoError(t, err)

	h, ok := lm.registry.get("host-A")
	require.True(t, ok)
	assert.True(t, h.Active)
	assert.Equal(t, "host-A", h.HostID)
}

func TestOnboardHost_ExpiredToken(t *testing.T) {
	_, lm := newTestLifecycleManager(nil)

	tok := &BootstrapToken{
		HostID:    "host-expired",
		IssuedAt:  time.Now().UTC().Add(-48 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(-24 * time.Hour),
		Token:     computeHMAC([]byte("test-secret"), "host-expired", time.Now().UTC().Add(-48*time.Hour)),
	}
	err := lm.OnboardHost(context.Background(), tok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "onboard rejected")
}

func TestOnboardHost_InvalidHMAC(t *testing.T) {
	_, lm := newTestLifecycleManager(nil)

	tok := &BootstrapToken{
		HostID:    "host-tampered",
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(bootstrapTokenTTL),
		Token:     "00000000",
	}
	err := lm.OnboardHost(context.Background(), tok)
	assert.Error(t, err)
}

func TestOffboardHost_Active(t *testing.T) {
	b, lm := newTestLifecycleManager(nil)

	tok, _ := b.BootstrapHost(context.Background(), HostDefinition{HostID: "host-B"})
	require.NoError(t, lm.OnboardHost(context.Background(), tok))

	err := lm.OffboardHost(context.Background(), "host-B")
	assert.NoError(t, err)

	h, ok := lm.registry.get("host-B")
	require.True(t, ok)
	assert.False(t, h.Active)
	assert.NotNil(t, h.OffboardAt)
}

func TestOffboardHost_NotFound(t *testing.T) {
	_, lm := newTestLifecycleManager(nil)
	err := lm.OffboardHost(context.Background(), "ghost-host")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestOffboardHost_RecorderError_NonFatal(t *testing.T) {
	rec := &mockStateRecorder{err: assert.AnError}
	b, lm := newTestLifecycleManager(rec)

	tok, _ := b.BootstrapHost(context.Background(), HostDefinition{HostID: "host-C"})
	require.NoError(t, lm.OnboardHost(context.Background(), tok))

	err := lm.OffboardHost(context.Background(), "host-C")
	assert.NoError(t, err, "recorder failure must not fail OffboardHost")
}

func TestHostRegistry_IsolatesHosts(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	reg := NewHostRegistry()
	lm := NewLifecycleManager(b, reg, nil, silentLogger())

	for _, id := range []string{"h1", "h2", "h3"} {
		tok, _ := b.BootstrapHost(context.Background(), HostDefinition{HostID: id})
		require.NoError(t, lm.OnboardHost(context.Background(), tok))
	}

	require.NoError(t, lm.OffboardHost(context.Background(), "h2"))

	h1, _ := reg.get("h1")
	h2, _ := reg.get("h2")
	h3, _ := reg.get("h3")
	assert.True(t, h1.Active)
	assert.False(t, h2.Active)
	assert.True(t, h3.Active)
}
