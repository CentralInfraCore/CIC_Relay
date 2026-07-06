package obs_test

import (
	"testing"

	"centralrelay/pkg/obs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiMetricSink_FansOutToEverySink(t *testing.T) {
	a := &obs.CaptureSink{}
	b := &obs.CaptureSink{}
	multi := obs.NewMultiMetricSink(a, b)

	multi.RecordCounter(obs.CounterPoint{Name: "relay.x", Value: 1})
	multi.RecordGauge(obs.GaugePoint{Name: "relay.y", Value: 2})
	multi.RecordHistogram(obs.HistogramPoint{Name: "relay.z", Sum: 3, Count: 1})

	for _, sink := range []*obs.CaptureSink{a, b} {
		require.Len(t, sink.Counters, 1)
		assert.Equal(t, "relay.x", sink.Counters[0].Name)
		require.Len(t, sink.Gauges, 1)
		assert.Equal(t, "relay.y", sink.Gauges[0].Name)
		require.Len(t, sink.Histos, 1)
		assert.Equal(t, "relay.z", sink.Histos[0].Name)
	}
}

func TestMultiMetricSink_NoSinksIsNoop(t *testing.T) {
	multi := obs.NewMultiMetricSink()
	assert.NotPanics(t, func() {
		multi.RecordCounter(obs.CounterPoint{Name: "relay.x", Value: 1})
		multi.RecordGauge(obs.GaugePoint{Name: "relay.y", Value: 2})
		multi.RecordHistogram(obs.HistogramPoint{Name: "relay.z", Sum: 3, Count: 1})
	})
}
