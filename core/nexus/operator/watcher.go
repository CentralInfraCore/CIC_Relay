package operator

import (
	"context"
	"fmt"
	"time"
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
	Watcher Watcher
	// TODO: Add dependencies for Bootstrap/Vault interaction
}

// NewOperator creates a new Operator.
func NewOperator(watcher Watcher) *Operator {
	return &Operator{
		Watcher: watcher,
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
		case event := <-events:
			if err := o.handleEvent(ctx, event); err != nil {
				// Log error but continue processing other events
				fmt.Printf("Error handling event %s %s: %v\n", event.Type, event.FilePath, err)
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

// onApply handles creation or update of an entity (Onboarding).
func (o *Operator) onApply(ctx context.Context, event Event) error {
	// TODO:
	// 1. Parse the content (YAML) to identify Kind (Host/Module).
	// 2. Generate Bootstrap Token / Certificate via Vault.
	// 3. Configure Policy.
	fmt.Printf("Applying %s\n", event.FilePath)
	return nil
}

// onDelete handles deletion of an entity (Offboarding).
func (o *Operator) onDelete(ctx context.Context, event Event) error {
	// TODO:
	// 1. Identify the entity from the FilePath.
	// 2. Revoke Certificate.
	// 3. Delete Policy.
	fmt.Printf("Deleting %s\n", event.FilePath)
	return nil
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
