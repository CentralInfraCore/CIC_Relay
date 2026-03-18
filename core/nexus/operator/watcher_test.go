package operator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockWatcher is a mock implementation of the Watcher interface.
type MockWatcher struct {
	mock.Mock
}

func (m *MockWatcher) Watch(ctx context.Context) (<-chan Event, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan Event), args.Error(1)
}

func TestOperator_Run(t *testing.T) {
	t.Run("successful run with event", func(t *testing.T) {
		mockWatcher := new(MockWatcher)
		op := NewOperator(mockWatcher)

		events := make(chan Event, 1)
		mockWatcher.On("Watch", mock.Anything).Return((<-chan Event)(events), nil)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		events <- Event{Type: EventCreate, FilePath: "test.yaml"}

		go func() {
			err := op.Run(ctx)
			assert.NoError(t, err)
		}()

		time.Sleep(50 * time.Millisecond)
		close(events)
	})

	t.Run("watcher returns error", func(t *testing.T) {
		mockWatcher := new(MockWatcher)
		op := NewOperator(mockWatcher)

		mockWatcher.On("Watch", mock.Anything).Return(nil, errors.New("watcher failed"))

		err := op.Run(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "watcher failed")
	})
}

func TestOperator_handleEvent(t *testing.T) {
	op := NewOperator(nil)

	t.Run("create event — no lifecycle warns and continues", func(t *testing.T) {
		err := op.handleEvent(context.Background(), Event{Type: EventCreate})
		assert.NoError(t, err)
	})

	t.Run("delete event — no lifecycle warns and continues", func(t *testing.T) {
		err := op.handleEvent(context.Background(), Event{Type: EventDelete})
		assert.NoError(t, err)
	})

	t.Run("unknown event", func(t *testing.T) {
		err := op.handleEvent(context.Background(), Event{Type: "UNKNOWN"})
		assert.Error(t, err)
	})
}

func TestOperatorWithLifecycle_OnApply_Host(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	reg := NewHostRegistry()
	lm := NewLifecycleManager(b, reg, nil, silentLogger())
	op := NewOperatorWithLifecycle(nil, b, lm, silentLogger())

	hostYAML := []byte("kind: Host\nmetadata:\n  name: node-01\n")
	err := op.handleEvent(context.Background(), Event{
		Type:     EventCreate,
		FilePath: "hosts/node-01.yaml",
		Content:  hostYAML,
	})
	assert.NoError(t, err)

	h, ok := reg.get("node-01")
	require.True(t, ok, "host should be registered")
	assert.True(t, h.Active)
}

func TestOperatorWithLifecycle_OnApply_NonHost_Skipped(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	reg := NewHostRegistry()
	lm := NewLifecycleManager(b, reg, nil, silentLogger())
	op := NewOperatorWithLifecycle(nil, b, lm, silentLogger())

	moduleYAML := []byte("kind: Module\nmetadata:\n  name: my-module\n")
	err := op.handleEvent(context.Background(), Event{
		Type:     EventCreate,
		FilePath: "modules/my-module.yaml",
		Content:  moduleYAML,
	})
	assert.NoError(t, err)
	_, ok := reg.get("my-module")
	assert.False(t, ok, "non-Host events should not be registered")
}

func TestOperatorWithLifecycle_OnDelete(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	reg := NewHostRegistry()
	lm := NewLifecycleManager(b, reg, nil, silentLogger())
	op := NewOperatorWithLifecycle(nil, b, lm, silentLogger())

	// Onboard first
	tok, _ := b.BootstrapHost(context.Background(), HostDefinition{HostID: "node-02"})
	require.NoError(t, lm.OnboardHost(context.Background(), tok))

	// Delete event — hostID derived from FilePath stem
	err := op.handleEvent(context.Background(), Event{
		Type:     EventDelete,
		FilePath: "hosts/node-02.yaml",
	})
	assert.NoError(t, err)

	h, ok := reg.get("node-02")
	require.True(t, ok)
	assert.False(t, h.Active)
}

func TestOperatorWithLifecycle_OnDelete_NotFound(t *testing.T) {
	b := NewBootstrapper([]byte("secret"), nil, silentLogger())
	reg := NewHostRegistry()
	lm := NewLifecycleManager(b, reg, nil, silentLogger())
	op := NewOperatorWithLifecycle(nil, b, lm, silentLogger())

	err := op.handleEvent(context.Background(), Event{
		Type:     EventDelete,
		FilePath: "hosts/ghost.yaml",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPollingWatcher_Watch(t *testing.T) {
	tmpDir := t.TempDir()
	watcher := NewPollingWatcher(tmpDir, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	events, err := watcher.Watch(ctx)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)

	select {
	case _, ok := <-events:
		if !ok {
			// Channel closed, which is fine if context is done
		}
	default:
		// No event yet
	}
}

func collectEvents(t *testing.T, ch <-chan Event, timeout time.Duration) []Event {
	t.Helper()
	var got []Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-deadline:
			return got
		}
	}
}

func TestPollingWatcher_StateDiff_Create(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a file before starting the watcher.
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "host-a.yaml"), []byte("kind: Host\nmetadata:\n  name: a\n"), 0644))

	watcher := NewPollingWatcher(tmpDir, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := watcher.Watch(ctx)
	require.NoError(t, err)

	got := collectEvents(t, events, 200*time.Millisecond)
	require.Len(t, got, 1)
	assert.Equal(t, EventCreate, got[0].Type)
	assert.Equal(t, "host-a.yaml", got[0].FilePath)
	assert.NotEmpty(t, got[0].Content)
}

func TestPollingWatcher_StateDiff_Update(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "host-b.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte("version: 1"), 0644))

	watcher := NewPollingWatcher(tmpDir, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := watcher.Watch(ctx)
	require.NoError(t, err)

	// Drain CREATE event.
	got := collectEvents(t, events, 100*time.Millisecond)
	require.Len(t, got, 1, "expected one CREATE")

	// Modify the file — should trigger UPDATE on next tick.
	require.NoError(t, os.WriteFile(filePath, []byte("version: 2"), 0644))

	got2 := collectEvents(t, events, 200*time.Millisecond)
	require.Len(t, got2, 1)
	assert.Equal(t, EventUpdate, got2[0].Type)
	assert.Equal(t, "host-b.yaml", got2[0].FilePath)
	assert.Equal(t, []byte("version: 2"), got2[0].Content)
}

func TestPollingWatcher_StateDiff_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "host-c.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte("kind: Host"), 0644))

	watcher := NewPollingWatcher(tmpDir, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := watcher.Watch(ctx)
	require.NoError(t, err)

	// Drain CREATE.
	got := collectEvents(t, events, 100*time.Millisecond)
	require.Len(t, got, 1, "expected CREATE")

	// Delete the file.
	require.NoError(t, os.Remove(filePath))

	got2 := collectEvents(t, events, 200*time.Millisecond)
	require.Len(t, got2, 1)
	assert.Equal(t, EventDelete, got2[0].Type)
	assert.Equal(t, "host-c.yaml", got2[0].FilePath)
	assert.Nil(t, got2[0].Content)
}

func TestPollingWatcher_EmptyDir_NoEvents(t *testing.T) {
	tmpDir := t.TempDir()
	watcher := NewPollingWatcher(tmpDir, 20*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	events, err := watcher.Watch(ctx)
	require.NoError(t, err)

	got := collectEvents(t, events, 150*time.Millisecond)
	assert.Empty(t, got, "empty dir should produce no events")
}
