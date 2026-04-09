package obs

import (
	"fmt"

	"github.com/sirupsen/logrus"
)

// LogrusSink bridges the obs layer into an existing logrus.Logger.
// It satisfies TraceSink, LogSink, and MetricSink.
//
// Every obs emission becomes a structured logrus entry so that the relay's
// JSON log stream carries full observability data without any additional
// infrastructure.
//
// Log levels:
//   - TraceFrame  → Info  (warn if Status is "error" or "timeout")
//   - LogRecord   → mapped from LogRecord.Level (info/warn/error; unknown → info)
//   - CounterPoint / GaugePoint / HistogramPoint → Debug ("relay.metric" event)
type LogrusSink struct {
	logger *logrus.Logger
}

// NewLogrusSink wraps logger in a LogrusSink. logger must not be nil.
func NewLogrusSink(logger *logrus.Logger) *LogrusSink {
	return &LogrusSink{logger: logger}
}

// RecordTrace emits a logrus entry for a TraceFrame.
func (s *LogrusSink) RecordTrace(f TraceFrame) {
	fields := logrus.Fields{
		"obs.type":        "trace",
		"obs.trace_id":    f.TraceID,
		"obs.span_id":     f.SpanID,
		"obs.name":        f.Name,
		"obs.start_time":  f.StartTime,
		"obs.end_time":    f.EndTime,
		"obs.component":   f.Resource.Component,
		"obs.relay_id":    f.Resource.RelayID,
	}
	for k, v := range f.Attrs {
		fields["obs.attr."+k] = v
	}
	if f.ExecCtx != nil {
		fields["obs.workflow_id"] = f.ExecCtx.WorkflowID
		if f.ExecCtx.SourceDigest != "" {
			fields["obs.source_digest"] = f.ExecCtx.SourceDigest
		}
	}

	entry := s.logger.WithFields(fields)
	status := ""
	if f.Attrs != nil {
		status = f.Attrs["status"]
	}
	switch status {
	case "error", "timeout":
		entry.Warn("obs.trace")
	default:
		entry.Info("obs.trace")
	}
}

// RecordLog emits a logrus entry for a LogRecord.
func (s *LogrusSink) RecordLog(r LogRecord) {
	fields := logrus.Fields{
		"obs.type":      "log",
		"obs.event":     r.Message,
		"obs.component": r.Resource.Component,
		"obs.relay_id":  r.Resource.RelayID,
	}
	for k, v := range r.Attrs {
		fields["obs.attr."+k] = v
	}
	if r.ExecCtx != nil {
		fields["obs.workflow_id"] = r.ExecCtx.WorkflowID
		if r.ExecCtx.SourceDigest != "" {
			fields["obs.source_digest"] = r.ExecCtx.SourceDigest
		}
	}

	entry := s.logger.WithFields(fields)
	switch r.Level {
	case "warn", "warning":
		entry.Warn(r.Message)
	case "error":
		entry.Error(r.Message)
	default:
		entry.Info(r.Message)
	}
}

// RecordCounter emits a logrus Debug entry for a CounterPoint.
func (s *LogrusSink) RecordCounter(p CounterPoint) {
	fields := logrus.Fields{
		"obs.type":      "metric",
		"obs.kind":      "counter",
		"obs.name":      p.Name,
		"obs.value":     p.Value,
		"obs.timestamp": p.Timestamp,
		"obs.component": p.Resource.Component,
	}
	for k, v := range p.Attrs {
		fields["obs.attr."+k] = v
	}
	s.logger.WithFields(fields).Debug(fmt.Sprintf("obs.counter %s=%d", p.Name, p.Value))
}

// RecordGauge emits a logrus Debug entry for a GaugePoint.
func (s *LogrusSink) RecordGauge(p GaugePoint) {
	fields := logrus.Fields{
		"obs.type":      "metric",
		"obs.kind":      "gauge",
		"obs.name":      p.Name,
		"obs.value":     p.Value,
		"obs.timestamp": p.Timestamp,
		"obs.component": p.Resource.Component,
	}
	for k, v := range p.Attrs {
		fields["obs.attr."+k] = v
	}
	s.logger.WithFields(fields).Debug(fmt.Sprintf("obs.gauge %s=%g", p.Name, p.Value))
}

// RecordHistogram emits a logrus Debug entry for a HistogramPoint.
func (s *LogrusSink) RecordHistogram(p HistogramPoint) {
	fields := logrus.Fields{
		"obs.type":      "metric",
		"obs.kind":      "histogram",
		"obs.name":      p.Name,
		"obs.count":     p.Count,
		"obs.sum":       p.Sum,
		"obs.timestamp": p.Timestamp,
		"obs.component": p.Resource.Component,
	}
	for k, v := range p.Attrs {
		fields["obs.attr."+k] = v
	}
	s.logger.WithFields(fields).Debug(fmt.Sprintf("obs.histogram %s count=%d sum=%g", p.Name, p.Count, p.Sum))
}