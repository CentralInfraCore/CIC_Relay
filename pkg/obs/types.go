package obs

// Resource identifies the origin of an observation — which relay component emitted it.
type Resource struct {
	RelayID   string            `json:"relay_id"`
	Component string            `json:"component"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// ExecutionContext ties an observation to a specific workflow execution step.
type ExecutionContext struct {
	WorkflowID   string `json:"workflow_id"`
	StepName     string `json:"step_name,omitempty"`
	SourceDigest string `json:"source_digest,omitempty"`
}

// LogRecord is a structured log entry emitted by the relay.
type LogRecord struct {
	Timestamp string            `json:"timestamp"`
	Level     string            `json:"level"`
	Message   string            `json:"message"`
	Resource  Resource          `json:"resource"`
	ExecCtx   *ExecutionContext `json:"exec_ctx,omitempty"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// TraceFrame is a lightweight execution span — not OTel-compatible,
// but structurally similar for operational visibility.
type TraceFrame struct {
	TraceID   string            `json:"trace_id"`
	SpanID    string            `json:"span_id"`
	Name      string            `json:"name"`
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Resource  Resource          `json:"resource"`
	ExecCtx   *ExecutionContext `json:"exec_ctx,omitempty"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// CounterPoint is a monotonically increasing metric.
type CounterPoint struct {
	Name      string            `json:"name"`
	Value     int64             `json:"value"`
	Timestamp string            `json:"timestamp"`
	Resource  Resource          `json:"resource"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// GaugePoint is an instantaneous numeric reading.
type GaugePoint struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Timestamp string            `json:"timestamp"`
	Resource  Resource          `json:"resource"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// HistogramPoint summarises a distribution of values.
type HistogramPoint struct {
	Name      string            `json:"name"`
	Sum       float64           `json:"sum"`
	Count     int64             `json:"count"`
	Buckets   []float64         `json:"buckets,omitempty"`
	Timestamp string            `json:"timestamp"`
	Resource  Resource          `json:"resource"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// IntegrityEnvelope links an observation record to the ProofTrace audit chain.
// RecordHash is the canonical SHA-256 of the record itself.
// ChainAnchor is the ProofTrace.ChainHash of the execution that emitted it — optional.
type IntegrityEnvelope struct {
	RecordHash  string `json:"record_hash"`
	ChainAnchor string `json:"chain_anchor,omitempty"`
}
