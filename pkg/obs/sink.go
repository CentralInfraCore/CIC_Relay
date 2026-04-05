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

func (NoopSink) RecordTrace(TraceFrame)         {}
func (NoopSink) RecordLog(LogRecord)             {}
func (NoopSink) RecordCounter(CounterPoint)      {}
func (NoopSink) RecordGauge(GaugePoint)          {}
func (NoopSink) RecordHistogram(HistogramPoint)  {}
