// Package obs is the relay's observability layer: components emit traces, log records and metric points
// (counters, gauges, histograms) through the TraceSink, LogSink and MetricSink interfaces, and sinks deliver
// them to logrus, a Prometheus text exposition, several sinks at once, an in-memory capture for tests, or
// nowhere. Metric instruments are registered under the "relay." prefix.
package obs

// TraceSink receives TraceFrame records.
type TraceSink interface {
	RecordTrace(TraceFrame)
}

// LogSink receives LogRecord entries.
type LogSink interface {
	RecordLog(LogRecord)
}

// MetricSink receives metric data points.
type MetricSink interface {
	RecordCounter(CounterPoint)
	RecordGauge(GaugePoint)
	RecordHistogram(HistogramPoint)
}

// NoopSink silently discards all records.
// It satisfies TraceSink, LogSink, and MetricSink.
type NoopSink struct{}

// RecordTrace discards the frame.
func (NoopSink) RecordTrace(TraceFrame) {}

// RecordLog discards the record.
func (NoopSink) RecordLog(LogRecord) {}

// RecordCounter discards the point.
func (NoopSink) RecordCounter(CounterPoint) {}

// RecordGauge discards the point.
func (NoopSink) RecordGauge(GaugePoint) {}

// RecordHistogram discards the point.
func (NoopSink) RecordHistogram(HistogramPoint) {}
