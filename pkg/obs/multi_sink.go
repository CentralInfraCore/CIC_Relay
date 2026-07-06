package obs

// MultiMetricSink fans out every metric point to a fixed list of MetricSinks
// — e.g. a LogrusSink (structured-log visibility) and a PrometheusSink
// (aggregated exposition) can both receive the same emission.
type MultiMetricSink struct {
	sinks []MetricSink
}

// NewMultiMetricSink returns a MetricSink that forwards every call to each of sinks in order.
func NewMultiMetricSink(sinks ...MetricSink) *MultiMetricSink {
	return &MultiMetricSink{sinks: sinks}
}

// RecordCounter forwards p to every wrapped sink.
func (m *MultiMetricSink) RecordCounter(p CounterPoint) {
	for _, s := range m.sinks {
		s.RecordCounter(p)
	}
}

// RecordGauge forwards p to every wrapped sink.
func (m *MultiMetricSink) RecordGauge(p GaugePoint) {
	for _, s := range m.sinks {
		s.RecordGauge(p)
	}
}

// RecordHistogram forwards p to every wrapped sink.
func (m *MultiMetricSink) RecordHistogram(p HistogramPoint) {
	for _, s := range m.sinks {
		s.RecordHistogram(p)
	}
}
