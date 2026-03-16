package operator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"centralrelay/core/nexus/types"
)

// EventType defines the type of change detected in the infrastructure repo.
type EventType string

const (
	EventCreate EventType = "CREATE"
	EventUpdate EventType = "UPDATE"
	EventDelete EventType = "DELETE"
)

// Event represents a change in the infrastructure definition.
type Event struct {
	Type     EventType
	FilePath string
	Content  []byte // Content is nil for DELETE events
}

// Watcher defines the interface for monitoring infrastructure changes.
type Watcher interface {
	// Watch returns a channel of events. It runs until the context is canceled.
	Watch(ctx context.Context) (<-chan Event, error)
}

// Operator manages the lifecycle of infrastructure entities based on Watcher events.
type Operator struct {
	Watcher      Watcher
	bootstrapper *Bootstrapper
	lifecycle    *LifecycleManager
	logger       *logrus.Logger
}

// NewOperator creates a basic Operator without lifecycle support.
// For full bootstrap/lifecycle wiring use NewOperatorWithLifecycle.
func NewOperator(watcher Watcher) *Operator {
	return &Operator{
		Watcher: watcher,
		logger:  logrus.New(),
	}
}

// NewOperatorWithLifecycle creates an Operator with full bootstrap and lifecycle support.
// b and lm may be nil — operations are skipped with a WARN log.
func NewOperatorWithLifecycle(watcher Watcher, b *Bootstrapper, lm *LifecycleManager, logger *logrus.Logger) *Operator {
	return &Operator{
		Watcher:      watcher,
		bootstrapper: b,
		lifecycle:    lm,
		logger:       logger,
	}
}

// Run starts the operator loop.
func (o *Operator) Run(ctx context.Context) error {
	events, err := o.Watcher.Watch(ctx)
	if err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := o.handleEvent(ctx, event); err != nil {
				o.logger.WithError(err).WithFields(logrus.Fields{
					"event_type": event.Type,
					"file":       event.FilePath,
				}).Warn("error handling operator event")
			}
		}
	}
}

// handleEvent processes a single infrastructure change event.
func (o *Operator) handleEvent(ctx context.Context, event Event) error {
	switch event.Type {
	case EventCreate, EventUpdate:
		return o.onApply(ctx, event)
	case EventDelete:
		return o.onDelete(ctx, event)
	default:
		return fmt.Errorf("unknown event type: %s", event.Type)
	}
}

// onApply handles creation or update of a Host entity.
// Parses the YAML content for Kind=Host, generates a bootstrap token,
// and onboards the host via LifecycleManager.
func (o *Operator) onApply(ctx context.Context, event Event) error {
	if o.bootstrapper == nil || o.lifecycle == nil {
		o.logger.WithField("file", event.FilePath).Warn("operator lifecycle not configured — skipping apply")
		return nil
	}

	var base types.BaseComponent
	if err := yaml.Unmarshal(event.Content, &base); err != nil {
		return fmt.Errorf("parse component YAML %s: %w", event.FilePath, err)
	}
	if base.Kind != "Host" {
		o.logger.WithFields(logrus.Fields{
			"kind": base.Kind,
			"file": event.FilePath,
		}).Debug("skipping non-Host apply event")
		return nil
	}

	hostID := base.Metadata.Name
	tok, err := o.bootstrapper.BootstrapHost(ctx, HostDefinition{
		HostID:   hostID,
		Hostname: hostID,
	})
	if err != nil {
		return fmt.Errorf("bootstrap host %q: %w", hostID, err)
	}
	return o.lifecycle.OnboardHost(ctx, tok)
}

// onDelete handles deletion of a Host entity.
// Derives the hostID from the FilePath stem and offboards the host.
func (o *Operator) onDelete(ctx context.Context, event Event) error {
	if o.lifecycle == nil {
		o.logger.WithField("file", event.FilePath).Warn("operator lifecycle not configured — skipping delete")
		return nil
	}

	hostID := strings.TrimSuffix(filepath.Base(event.FilePath), filepath.Ext(event.FilePath))
	return o.lifecycle.OffboardHost(ctx, hostID)
}

// PollingWatcher implements Watcher by periodically scanning a directory.
// A simple implementation for now; production usage might prefer fsnotify.
type PollingWatcher struct {
	RootPath string
	Interval time.Duration
}

// NewPollingWatcher creates a new watcher that scans the directory every interval.
func NewPollingWatcher(rootPath string, interval time.Duration) *PollingWatcher {
	return &PollingWatcher{
		RootPath: rootPath,
		Interval: interval,
	}
}

func (pw *PollingWatcher) Watch(ctx context.Context) (<-chan Event, error) {
	events := make(chan Event)

	go func() {
		defer close(events)
		ticker := time.NewTicker(pw.Interval)
		defer ticker.Stop()

		// TODO: Implement state tracking to detect diffs (new/modified/deleted files).
		// For now, this is just a skeleton.

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Scan directory and emit events...
			}
		}
	}()

	return events, nil
}