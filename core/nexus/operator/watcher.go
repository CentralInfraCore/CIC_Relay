package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
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

// ResourceValidator validates a single resource against an external schema registry.
type ResourceValidator interface {
	ValidateResource(ctx context.Context, res types.ResourceSpec) error
}

// ResourceActivator executes the lifecycle action for a validated, desired-present resource.
// The implementation bridges nexus/operator to core/cabinet without a direct import.
type ResourceActivator interface {
	Activate(ctx context.Context, svcName string, res types.ResourceSpec) error
}

// Operator manages the lifecycle of infrastructure entities based on Watcher events.
type Operator struct {
	Watcher      Watcher
	bootstrapper *Bootstrapper
	lifecycle    *LifecycleManager
	validator    ResourceValidator  // nil = schema validation skipped
	activator    ResourceActivator  // nil = activation logged but not executed
	graph        *types.Graph       // nil = dependency gating skipped (lenient)
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

// WithValidator attaches a ResourceValidator to the Operator.
// When set, every Service resource is validated against the schema registry before activation.
func (o *Operator) WithValidator(v ResourceValidator) *Operator {
	o.validator = v
	return o
}

// WithActivator attaches a ResourceActivator to the Operator.
// When set, resources with state.desired=present are passed to the activator after validation.
func (o *Operator) WithActivator(a ResourceActivator) *Operator {
	o.activator = a
	return o
}

// WithGraph provides the full infrastructure graph for dependency resolution.
// When set, a resource is only activated if all its declared dependencies exist in the graph
// with at least one resource declaring state.desired=present.
func (o *Operator) WithGraph(g *types.Graph) *Operator {
	o.graph = g
	return o
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

// onApply handles creation or update of a Host or Service entity.
func (o *Operator) onApply(ctx context.Context, event Event) error {
	var base types.BaseComponent
	if err := yaml.Unmarshal(event.Content, &base); err != nil {
		return fmt.Errorf("parse component YAML %s: %w", event.FilePath, err)
	}

	switch base.Kind {
	case "Host":
		return o.onApplyHost(ctx, event, base)
	case "Service":
		return o.onApplyService(ctx, event)
	default:
		o.logger.WithFields(logrus.Fields{
			"kind": base.Kind,
			"file": event.FilePath,
		}).Debug("skipping unknown kind in apply event")
		return nil
	}
}

// onApplyHost generates a bootstrap token and onboards the host via LifecycleManager.
func (o *Operator) onApplyHost(ctx context.Context, event Event, base types.BaseComponent) error {
	if o.bootstrapper == nil || o.lifecycle == nil {
		o.logger.WithField("file", event.FilePath).Warn("operator lifecycle not configured — skipping host apply")
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

// onApplyService validates each resource in the service against the schema registry
// and logs resources that are ready for activation.
func (o *Operator) onApplyService(ctx context.Context, event Event) error {
	var svc types.Service
	if err := yaml.Unmarshal(event.Content, &svc); err != nil {
		return fmt.Errorf("parse Service YAML %s: %w", event.FilePath, err)
	}

	for _, res := range svc.Spec.Resources {
		log := o.logger.WithFields(logrus.Fields{
			"service":  svc.Metadata.Name,
			"resource": res.Meta.Name,
			"schema":   res.SchemaRef.Name + "@" + res.SchemaRef.Version,
		})

		if o.validator != nil {
			if err := o.validator.ValidateResource(ctx, res); err != nil {
				log.WithError(err).Warn("resource validation failed — skipping activation")
				continue
			}
		}

		if res.State.Desired != "present" {
			log.WithField("desired", res.State.Desired).Debug("resource not desired present — skipping activation")
			continue
		}

		if !o.depsReady(res.Dependencies) {
			log.WithField("dependencies", res.Dependencies).Warn("resource dependencies not ready — skipping activation")
			continue
		}

		if o.activator != nil {
			if err := o.activator.Activate(ctx, svc.Metadata.Name, res); err != nil {
				log.WithError(err).Warn("resource activation failed")
				continue
			}
			log.Info("resource activated")
		} else {
			log.Info("resource ready for activation (no activator configured)")
		}
	}
	return nil
}

// depsReady returns true if all named service dependencies exist in the graph
// and have at least one resource declaring state.desired=present.
// When no graph is set, the check is lenient (returns true).
func (o *Operator) depsReady(deps []string) bool {
	if len(deps) == 0 {
		return true
	}
	if o.graph == nil {
		return true
	}
	for _, dep := range deps {
		svc, ok := o.graph.Services[dep]
		if !ok {
			return false
		}
		hasPresent := false
		for _, r := range svc.Spec.Resources {
			if r.State.Desired == "present" {
				hasPresent = true
				break
			}
		}
		if !hasPresent {
			return false
		}
	}
	return true
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

// scanDir walks rootPath and returns a map of relative path → SHA-256 content hash.
func scanDir(rootPath string) (map[string]string, error) {
	snapshot := make(map[string]string)
	err := filepath.WalkDir(rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		rel, _ := filepath.Rel(rootPath, path)
		snapshot[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	return snapshot, err
}

// emitDiff computes CREATE/UPDATE/DELETE events between prev and curr snapshots
// and sends them on ch. First call should pass prev=nil to emit CREATE for all files.
func emitDiff(ctx context.Context, ch chan<- Event, prev, curr map[string]string, rootPath string) {
	// New or changed files → CREATE / UPDATE
	for relPath, currHash := range curr {
		var evType EventType
		if prevHash, existed := prev[relPath]; !existed {
			evType = EventCreate
		} else if prevHash != currHash {
			evType = EventUpdate
		} else {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rootPath, relPath))
		if err != nil {
			logrus.WithField("path", relPath).WithError(err).Warn("watcher: failed to read file for event")
			continue
		}
		select {
		case <-ctx.Done():
			return
		case ch <- Event{Type: evType, FilePath: relPath, Content: data}:
		}
	}
	// Removed files → DELETE
	for relPath := range prev {
		if _, exists := curr[relPath]; !exists {
			select {
			case <-ctx.Done():
				return
			case ch <- Event{Type: EventDelete, FilePath: relPath, Content: nil}:
			}
		}
	}
}

func (pw *PollingWatcher) Watch(ctx context.Context) (<-chan Event, error) {
	events := make(chan Event, 16)

	go func() {
		defer close(events)
		ticker := time.NewTicker(pw.Interval)
		defer ticker.Stop()

		var prev map[string]string // nil on first tick → all files are CREATE

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				curr, err := scanDir(pw.RootPath)
				if err != nil {
					logrus.WithField("root", pw.RootPath).WithError(err).Warn("watcher: scan failed")
					continue
				}
				emitDiff(ctx, events, prev, curr, pw.RootPath)
				prev = curr
			}
		}
	}()

	return events, nil
}