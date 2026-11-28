package cabinet

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scopeKey struct{}

// scopingSource is a fakeSource that scopes every step: the step's context carries the step name, and the three
// per-step questions are asked with that same context.
type scopingSource struct {
	fakeSource
	seenHI, seenAuth, seenExec []string
}

func (s *scopingSource) ScopeStep(ctx context.Context, step ExecutionGraphStep) context.Context {
	return context.WithValue(ctx, scopeKey{}, "scope:"+step.Raw)
}

func scopeOf(ctx context.Context) string { v, _ := ctx.Value(scopeKey{}).(string); return v }

func (s *scopingSource) HostInteractions(ctx context.Context, _ ExecutionGraphStep) (string, int) {
	s.seenHI = append(s.seenHI, scopeOf(ctx))
	return "sha256:" + strings.Repeat("9a", 32), 2
}
func (s *scopingSource) Authorization(ctx context.Context, st ExecutionGraphStep) (string, string) {
	s.seenAuth = append(s.seenAuth, scopeOf(ctx))
	return s.fakeSource.Authorization(ctx, st)
}
func (s *scopingSource) ExecutionContext(ctx context.Context, w string) string {
	s.seenExec = append(s.seenExec, scopeOf(ctx))
	return s.fakeSource.ExecutionContext(ctx, w)
}

// INT-08: a step that is scoped by its source runs under the scoped context, and is sealed with the same context.
func TestV2StepScopeReachesTheDispatchAndTheSealing(t *testing.T) {
	var dispatched []string
	src := &scopingSource{}
	r := newV2Run(t, src, func(ctx context.Context, in interface{}) (interface{}, error) {
		dispatched = append(dispatched, scopeOf(ctx))
		return in, nil
	})
	_, err := r.run(t, "echo@1.0.echo(input) -> mid", "echo@1.0.echo(mid) -> out")
	require.NoError(t, err)
	want := []string{"scope:echo@1.0.echo(input) -> mid", "scope:echo@1.0.echo(mid) -> out"}
	assert.Equal(t, want, dispatched, "each step dispatches under its own scope, not under the previous step's")
	assert.Equal(t, want, src.seenHI, "host interactions are asked with the step's scope")
	assert.Equal(t, want, src.seenAuth)
	assert.Equal(t, want, src.seenExec)
	require.Len(t, r.arts, 1)
	assert.Equal(t, 2, r.arts[0].Steps[0].References.HostInteractions.Count)
}

// a source that is not a StepScoper changes nothing: the dispatch context is the caller's and sealing uses the run's
func TestV2WithoutScopingTheContextIsUnchanged(t *testing.T) {
	var dispatched []string
	r := newV2Run(t, fakeSource{}, func(ctx context.Context, in interface{}) (interface{}, error) {
		dispatched = append(dispatched, scopeOf(ctx))
		return in, nil
	})
	_, err := r.run(t, "echo@1.0.echo(input) -> out")
	require.NoError(t, err)
	assert.Equal(t, []string{""}, dispatched)
}
