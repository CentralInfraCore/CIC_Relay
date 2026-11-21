package obs

import "sync"

// CaptureSink is a thread-safe, in-memory sink for use in tests.
// It satisfies TraceSink, LogSink, and MetricSink.
type CaptureSink struct {
	mu       sync.Mutex
	Traces   []TraceFrame
	Logs     []LogRecord
	Counters []CounterPoint
	Gauges   []GaugePoint
	Histos   []HistogramPoint
}

// RecordTrace appends f to Traces.
func (c *CaptureSink) RecordTrace(f TraceFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Traces = append(c.Traces, f)
}

// RecordLog appends r to Logs.
func (c *CaptureSink) RecordLog(r LogRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Logs = append(c.Logs, r)
}

// RecordCounter appends p to Counters.
func (c *CaptureSink) RecordCounter(p CounterPoint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Counters = append(c.Counters, p)
}

// RecordGauge appends p to Gauges.
func (c *CaptureSink) RecordGauge(p GaugePoint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Gauges = append(c.Gauges, p)
}

// RecordHistogram appends p to Histos.
func (c *CaptureSink) RecordHistogram(p HistogramPoint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Histos = append(c.Histos, p)
}
