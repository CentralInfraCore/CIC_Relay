package obs_test

import (
	"errors"
	"strings"
	"testing"

	"centralrelay/pkg/obs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusSink_EmptyProducesNoOutput(t *testing.T) {
	s := obs.NewPrometheusSink()
	var b strings.Builder
	require.NoError(t, s.WritePrometheus(&b))
	assert.Empty(t, b.String())
}

func TestPrometheusSink_CounterAccumulatesPerSeries(t *testing.T) {
	s := obs.NewPrometheusSink()
	s.RecordCounter(obs.CounterPoint{Name: "relay.requests.total", Value: 1, Resource: obs.Resource{Component: "cmd/relay"}})
	s.RecordCounter(obs.CounterPoint{Name: "relay.requests.total", Value: 2, Resource: obs.Resource{Component: "cmd/relay"}})

	var b strings.Builder
	require.NoError(t, s.WritePrometheus(&b))
	out := b.String()
	assert.Contains(t, out, "# TYPE relay_requests_total counter\n")
	assert.Contains(t, out, `relay_requests_total{component="cmd/relay"} 3`)
}

func TestPrometheusSink_CounterKeepsDistinctSeriesSeparate(t *testing.T) {
	s := obs.NewPrometheusSink()
	s.RecordCounter(obs.CounterPoint{Name: "relay.requests.total", Value: 1, Attrs: map[string]string{"path": "/set"}})
	s.RecordCounter(obs.CounterPoint{Name: "relay.requests.total", Value: 5, Attrs: map[string]string{"path": "/healthz"}})

	var b strings.Builder
	require.NoError(t, s.WritePrometheus(&b))
	out := b.String()
	assert.Contains(t, out, `relay_requests_total{path="/set"} 1`)
	assert.Contains(t, out, `relay_requests_total{path="/healthz"} 5`)
	// One TYPE header per metric name, not per series.
	assert.Equal(t, 1, strings.Count(out, "# TYPE relay_requests_total counter"))
}

func TestPrometheusSink_GaugeKeepsLastValue(t *testing.T) {
	s := obs.NewPrometheusSink()
	s.RecordGauge(obs.GaugePoint{Name: "relay.pool.size", Value: 3})
	s.RecordGauge(obs.GaugePoint{Name: "relay.pool.size", Value: 7})

	var b strings.Builder
	require.NoError(t, s.WritePrometheus(&b))
	out := b.String()
	assert.Contains(t, out, "# TYPE relay_pool_size gauge\n")
	assert.Contains(t, out, "relay_pool_size 7\n")
	assert.NotContains(t, out, " 3\n")
}

func TestPrometheusSink_HistogramAccumulatesSumAndCount(t *testing.T) {
	s := obs.NewPrometheusSink()
	s.RecordHistogram(obs.HistogramPoint{Name: "relay.wasm.compile_duration_ms", Sum: 10, Count: 1, Attrs: map[string]string{"module_id": "m1"}})
	s.RecordHistogram(obs.HistogramPoint{Name: "relay.wasm.compile_duration_ms", Sum: 15, Count: 1, Attrs: map[string]string{"module_id": "m1"}})

	var b strings.Builder
	require.NoError(t, s.WritePrometheus(&b))
	out := b.String()
	assert.Contains(t, out, "# TYPE relay_wasm_compile_duration_ms histogram\n")
	assert.Contains(t, out, `relay_wasm_compile_duration_ms_bucket{le="+Inf",module_id="m1"} 2`)
	assert.Contains(t, out, `relay_wasm_compile_duration_ms_sum{module_id="m1"} 25`)
	assert.Contains(t, out, `relay_wasm_compile_duration_ms_count{module_id="m1"} 2`)
}

func TestPrometheusSink_LabelValuesAreEscaped(t *testing.T) {
	s := obs.NewPrometheusSink()
	s.RecordCounter(obs.CounterPoint{Name: "relay.x", Value: 1, Attrs: map[string]string{"msg": `has "quotes" and \backslash`}})

	var b strings.Builder
	require.NoError(t, s.WritePrometheus(&b))
	assert.Contains(t, b.String(), `msg="has \"quotes\" and \\backslash"`)
}

func TestPrometheusSink_OutputIsDeterministicallyOrdered(t *testing.T) {
	s := obs.NewPrometheusSink()
	s.RecordCounter(obs.CounterPoint{Name: "relay.zzz", Value: 1})
	s.RecordCounter(obs.CounterPoint{Name: "relay.aaa", Value: 1})

	var b1, b2 strings.Builder
	require.NoError(t, s.WritePrometheus(&b1))
	require.NoError(t, s.WritePrometheus(&b2))
	assert.Equal(t, b1.String(), b2.String())
	assert.Less(t, strings.Index(b1.String(), "relay_aaa"), strings.Index(b1.String(), "relay_zzz"))
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func TestPrometheusSink_WritePrometheus_PropagatesWriteError(t *testing.T) {
	s := obs.NewPrometheusSink()
	s.RecordCounter(obs.CounterPoint{Name: "relay.x", Value: 1})
	err := s.WritePrometheus(failingWriter{})
	assert.Error(t, err)
}
