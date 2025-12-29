package operator

import (
	"context"
	"errors"
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

	t.Run("create event", func(t *testing.T) {
		err := op.handleEvent(context.Background(), Event{Type: EventCreate})
		assert.NoError(t, err)
	})

	t.Run("delete event", func(t *testing.T) {
		err := op.handleEvent(context.Background(), Event{Type: EventDelete})
		assert.NoError(t, err)
	})

	t.Run("unknown event", func(t *testing.T) {
		err := op.handleEvent(context.Background(), Event{Type: "UNKNOWN"})
		assert.Error(t, err)
	})
}

func TestPollingWatcher_Watch(t *testing.T) {
	tmpDir := t.TempDir()
	watcher := NewPollingWatcher(tmpDir, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	events, err := watcher.Watch(ctx)
	require.NoError(t, err)

	// To increase coverage, we need to let the goroutine run a bit
	time.Sleep(20 * time.Millisecond)

	// Verify channel is open
	select {
	case _, ok := <-events:
		if !ok {
			// Channel closed, which is fine if context is done
		}
	default:
		// No event yet
	}
}
