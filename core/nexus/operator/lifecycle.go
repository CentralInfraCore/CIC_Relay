package operator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"centralrelay/core/nexus/recorder"
	"centralrelay/core/nexus/types"
)

// HostState holds the lifecycle state of a registered host.
type HostState struct {
	HostID     string
	Active     bool
	Token      *BootstrapToken
	OnboardAt  time.Time
	OffboardAt *time.Time // nil while active
}

// HostRegistry is a thread-safe in-memory store of host lifecycle states.
// Production deployments should replace this with a persistent store.
type HostRegistry struct {
	mu    sync.Mutex
	hosts map[string]*HostState
}

// NewHostRegistry returns an empty HostRegistry.
func NewHostRegistry() *HostRegistry {
	return &HostRegistry{hosts: make(map[string]*HostState)}
}

func (r *HostRegistry) get(hostID string) (*HostState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hosts[hostID]
	return h, ok
}

func (r *HostRegistry) set(h *HostState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts[h.HostID] = h
}

// LifecycleManager handles host onboard and offboard operations.
type LifecycleManager struct {
	bootstrapper *Bootstrapper
	registry     *HostRegistry
	recorder     recorder.StateRecorder // nil = recording disabled
	logger       *logrus.Logger
}

// NewLifecycleManager creates a LifecycleManager.
// rec may be nil — recording is non-fatal when absent.
func NewLifecycleManager(b *Bootstrapper, reg *HostRegistry, rec recorder.StateRecorder, logger *logrus.Logger) *LifecycleManager {
	return &LifecycleManager{bootstrapper: b, registry: reg, recorder: rec, logger: logger}
}

// OnboardHost validates the bootstrap token and marks the host as active.
// Returns an error if the token is invalid or expired.
func (lm *LifecycleManager) OnboardHost(ctx context.Context, token *BootstrapToken) error {
	if err := lm.bootstrapper.ValidateToken(token); err != nil {
		lm.logger.WithFields(logrus.Fields{
			"host_id": token.HostID,
			"outcome": "rejected",
		}).WithError(err).Warn("OnboardHost: invalid token")
		return fmt.Errorf("onboard rejected: %w", err)
	}

	now := time.Now().UTC()
	lm.registry.set(&HostState{
		HostID:    token.HostID,
		Active:    true,
		Token:     token,
		OnboardAt: now,
	})

	lm.logger.WithFields(logrus.Fields{
		"host_id":    token.HostID,
		"outcome":    "onboarded",
		"onboard_at": now,
	}).Info("host onboarded")

	lm.record(ctx, "onboard-"+token.HostID, now, map[string]interface{}{
		"event":      "onboard",
		"host_id":    token.HostID,
		"onboard_at": now,
	})
	return nil
}

// OffboardHost deactivates the host and commits the revocation to the audit log.
// Returns an error if the host is not found.
func (lm *LifecycleManager) OffboardHost(ctx context.Context, hostID string) error {
	h, ok := lm.registry.get(hostID)
	if !ok {
		return fmt.Errorf("offboard: host %q not found", hostID)
	}
	now := time.Now().UTC()
	h.Active = false
	h.OffboardAt = &now
	lm.registry.set(h)

	lm.logger.WithFields(logrus.Fields{
		"host_id":      hostID,
		"outcome":      "offboarded",
		"offboard_at":  now,
	}).Info("host offboarded")

	lm.record(ctx, "offboard-"+hostID, now, map[string]interface{}{
		"event":       "offboard",
		"host_id":     hostID,
		"offboard_at": now,
	})
	return nil
}

// record commits a lifecycle event to the audit recorder.
// Non-fatal: logs WARN on failure or absent recorder.
func (lm *LifecycleManager) record(ctx context.Context, runID string, ts time.Time, actual interface{}) {
	if lm.recorder == nil {
		lm.logger.Warn("lifecycle recorder not configured — event not audited")
		return
	}
	expected := types.ExpectedState{RunID: runID, Timestamp: ts}
	if _, err := lm.recorder.RecordState(ctx, expected, actual); err != nil {
		lm.logger.WithError(err).Warn("failed to record lifecycle event")
	}
}