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

	"centralrelay/core/nexus/types"
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

	// Atomic update: stage file outside the watched directory then rename in.
	// A plain os.WriteFile truncates first, which can cause a spurious second
	// UPDATE event if the ticker fires between truncate and write.
	stageDir := t.TempDir()
	stageFile := filepath.Join(stageDir, "host-b.yaml")
	require.NoError(t, os.WriteFile(stageFile, []byte("version: 2"), 0644))
	require.NoError(t, os.Rename(stageFile, filePath))

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

// mockValidator is a stub ResourceValidator for testing WithValidator.
type mockValidator struct{ err error }

func (m *mockValidator) ValidateResource(_ context.Context, _ types.ResourceSpec) error {
	return m.err
}

// mockActivator records Activate calls for assertion in tests.
type mockActivator struct {
	calls []struct {
		svcName string
		resName string
	}
	err error
}

func (m *mockActivator) Activate(_ context.Context, svcName string, res types.ResourceSpec) error {
	m.calls = append(m.calls, struct {
		svcName string
		resName string
	}{svcName, res.Meta.Name})
	return m.err
}

func makeServiceYAML(svcName, resName, desired string, deps []string) []byte {
	depStr := ""
	for _, d := range deps {
		depStr += "\n        - " + d
	}
	return []byte(`apiVersion: cic/v1
kind: Service
metadata:
  name: ` + svcName + `
spec:
  module: test-mod
  resources:
    - meta:
        name: ` + resName + `
      schemaRef:
        name: core.test
        version: "1.0"
      state:
        desired: ` + desired + `
      dependencies:` + depStr + `
`)
}

func TestOnApplyService_DesiredPresent_Activated(t *testing.T) {
	act := &mockActivator{}
	op := NewOperator(nil).WithActivator(act)

	err := op.handleEvent(context.Background(), Event{
		Type:    EventCreate,
		Content: makeServiceYAML("svc-a", "res-01", "present", nil),
	})
	require.NoError(t, err)
	require.Len(t, act.calls, 1)
	assert.Equal(t, "svc-a", act.calls[0].svcName)
	assert.Equal(t, "res-01", act.calls[0].resName)
}

func TestOnApplyService_DesiredAbsent_NotActivated(t *testing.T) {
	act := &mockActivator{}
	op := NewOperator(nil).WithActivator(act)

	err := op.handleEvent(context.Background(), Event{
		Type:    EventCreate,
		Content: makeServiceYAML("svc-b", "res-02", "absent", nil),
	})
	require.NoError(t, err)
	assert.Empty(t, act.calls)
}

func TestOnApplyService_DepsNotInGraph_Skipped(t *testing.T) {
	act := &mockActivator{}
	graph := &types.Graph{Services: map[string]*types.Service{}}
	op := NewOperator(nil).WithActivator(act).WithGraph(graph)

	err := op.handleEvent(context.Background(), Event{
		Type:    EventCreate,
		Content: makeServiceYAML("svc-c", "res-03", "present", []string{"missing-dep"}),
	})
	require.NoError(t, err)
	assert.Empty(t, act.calls, "activation must be skipped when dep is absent from graph")
}

func TestOnApplyService_DepsReady_Activated(t *testing.T) {
	act := &mockActivator{}
	graph := &types.Graph{
		Services: map[string]*types.Service{
			"dep-svc": {
				Spec: types.ServiceSpec{
					Resources: []types.ResourceSpec{
						{State: types.ResourceState{Desired: "present"}},
					},
				},
			},
		},
	}
	op := NewOperator(nil).WithActivator(act).WithGraph(graph)

	err := op.handleEvent(context.Background(), Event{
		Type:    EventCreate,
		Content: makeServiceYAML("svc-d", "res-04", "present", []string{"dep-svc"}),
	})
	require.NoError(t, err)
	require.Len(t, act.calls, 1)
	assert.Equal(t, "res-04", act.calls[0].resName)
}

func TestOnApplyService_ActivatorError_ContinuesNextResource(t *testing.T) {
	act := &mockActivator{err: errors.New("activation failed")}
	op := NewOperator(nil).WithActivator(act)

	// Two resources in one service; activator fails on first → second also attempted
	content := []byte(`apiVersion: cic/v1
kind: Service
metadata:
  name: svc-e
spec:
  module: test-mod
  resources:
    - meta:
        name: res-05
      schemaRef:
        name: core.test
        version: "1.0"
      state:
        desired: present
    - meta:
        name: res-06
      schemaRef:
        name: core.test
        version: "1.0"
      state:
        desired: present
`)
	err := op.handleEvent(context.Background(), Event{Type: EventCreate, Content: content})
	require.NoError(t, err, "handleEvent must not propagate activator errors")
	assert.Len(t, act.calls, 2, "both resources must be attempted despite first failure")
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

func TestWithValidator_SetsValidator(t *testing.T) {
	op := NewOperator(nil)
	v := &mockValidator{err: nil}
	result := op.WithValidator(v)
	assert.Same(t, op, result, "WithValidator must return the same operator")
}

func TestOnApplyService_ValidatorError_SkipsActivation(t *testing.T) {
	act := &mockActivator{}
	val := &mockValidator{err: errors.New("schema not found")}
	op := NewOperator(nil).WithValidator(val).WithActivator(act)

	err := op.handleEvent(context.Background(), Event{
		Type:    EventCreate,
		Content: makeServiceYAML("svc-v", "res-v01", "present", nil),
	})
	require.NoError(t, err)
	assert.Empty(t, act.calls, "activation must be skipped when validator returns error")
}

func TestOnApplyService_ValidatorPass_Activated(t *testing.T) {
	act := &mockActivator{}
	val := &mockValidator{err: nil}
	op := NewOperator(nil).WithValidator(val).WithActivator(act)

	err := op.handleEvent(context.Background(), Event{
		Type:    EventCreate,
		Content: makeServiceYAML("svc-vp", "res-vp01", "present", nil),
	})
	require.NoError(t, err)
	require.Len(t, act.calls, 1, "activation must proceed when validator passes")
}
