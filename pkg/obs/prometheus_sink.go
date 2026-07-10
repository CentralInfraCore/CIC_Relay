package obs

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// PrometheusSink aggregates obs metric points in-process and renders them in
// the Prometheus text exposition format (https://prometheus.io/docs/instrumenting/exposition_formats/).
// It satisfies MetricSink.
//
// Counters sum monotonically per (name, labels) series; gauges keep the
// latest value; histograms track only count+sum, exposed as a single
// "+Inf" bucket — no call site today supplies distribution buckets, so
// percentile buckets are not fabricated.
type PrometheusSink struct {
	mu         sync.Mutex
	counters   map[string]*counterEntry
	gauges     map[string]*gaugeEntry
	histograms map[string]*histogramEntry
}

// NewPrometheusSink returns an empty PrometheusSink.
func NewPrometheusSink() *PrometheusSink {
	return &PrometheusSink{
		counters:   make(map[string]*counterEntry),
		gauges:     make(map[string]*gaugeEntry),
		histograms: make(map[string]*histogramEntry),
	}
}

type label struct{ key, value string }

// series identifies one labeled time series (a metric name plus its sorted labels).
type series struct {
	name   string
	labels []label
	key    string // precomputed seriesKey(name, labels), used for map lookup and sorting
}

type counterEntry struct {
	series
	value float64
}

type gaugeEntry struct {
	series
	value float64
}

type histogramEntry struct {
	series
	sum   float64
	count int64
}

// labelsFor builds the sorted label set for a point: Resource.Component (if
// set) plus every Attrs entry.
func labelsFor(res Resource, attrs map[string]string) []label {
	labels := make([]label, 0, len(attrs)+1)
	if res.Component != "" {
		labels = append(labels, label{"component", res.Component})
	}
	for k, v := range attrs {
		labels = append(labels, label{k, v})
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].key < labels[j].key })
	return labels
}

func seriesKey(name string, labels []label) string {
	var b strings.Builder
	b.WriteString(name)
	for _, l := range labels {
		b.WriteByte(0)
		b.WriteString(l.key)
		b.WriteByte('=')
		b.WriteString(l.value)
	}
	return b.String()
}

func newSeries(name string, res Resource, attrs map[string]string) series {
	labels := labelsFor(res, attrs)
	return series{name: name, labels: labels, key: seriesKey(name, labels)}
}

// RecordCounter adds p.Value to the running total for its series.
func (s *PrometheusSink) RecordCounter(p CounterPoint) {
	sr := newSeries(p.Name, p.Resource, p.Attrs)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.counters[sr.key]
	if !ok {
		e = &counterEntry{series: sr}
		s.counters[sr.key] = e
	}
	e.value += float64(p.Value)
}

// RecordGauge overwrites the last-known value for its series.
func (s *PrometheusSink) RecordGauge(p GaugePoint) {
	sr := newSeries(p.Name, p.Resource, p.Attrs)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.gauges[sr.key]
	if !ok {
		e = &gaugeEntry{series: sr}
		s.gauges[sr.key] = e
	}
	e.value = p.Value
}

// RecordHistogram accumulates p.Sum/p.Count into the running total for its series.
func (s *PrometheusSink) RecordHistogram(p HistogramPoint) {
	sr := newSeries(p.Name, p.Resource, p.Attrs)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.histograms[sr.key]
	if !ok {
		e = &histogramEntry{series: sr}
		s.histograms[sr.key] = e
	}
	e.sum += p.Sum
	e.count += p.Count
}

// sanitizeMetricName maps an obs metric name (dot-separated, e.g.
// "relay.requests.total") onto the Prometheus metric-name charset
// ([a-zA-Z_:][a-zA-Z0-9_:]*).
func sanitizeMetricName(name string) string {
	return strings.NewReplacer(".", "_", "-", "_").Replace(name)
}

// formatFloat renders v the way Prometheus expects: the shortest
// round-trippable decimal representation.
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// formatLabels renders labels as a Prometheus label-set suffix, e.g.
// `{component="cicwasm",module_id="x"}`, or "" when there are none.
func formatLabels(labels []label) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, l := range labels {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", l.key, l.value)
	}
	b.WriteByte('}')
	return b.String()
}

// errWriter accumulates the first write error so callers can chain writes
// without checking after every line.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}

// WritePrometheus renders every aggregated series to w in Prometheus text
// exposition format, in a deterministic order (sorted by metric name, then
// by label set) so repeated scrapes are diffable.
func (s *PrometheusSink) WritePrometheus(w io.Writer) error {
	// Snapshot by VALUE (dereferencing *counterEntry etc.) while still holding
	// the lock. Copying just the pointers would leave the render loop below
	// reading e.value/e.sum/e.count with no synchronization at all — a data
	// race against a concurrent RecordCounter/RecordGauge/RecordHistogram
	// mutating that same entry through its pointer. The `series` field
	// (name/labels/key) is set once at creation and never mutated again, so
	// sharing its label slice across the snapshot is safe.
	s.mu.Lock()
	counters := make([]counterEntry, 0, len(s.counters))
	for _, e := range s.counters {
		counters = append(counters, *e)
	}
	gauges := make([]gaugeEntry, 0, len(s.gauges))
	for _, e := range s.gauges {
		gauges = append(gauges, *e)
	}
	histograms := make([]histogramEntry, 0, len(s.histograms))
	for _, e := range s.histograms {
		histograms = append(histograms, *e)
	}
	s.mu.Unlock()

	sort.Slice(counters, func(i, j int) bool { return counters[i].key < counters[j].key })
	sort.Slice(gauges, func(i, j int) bool { return gauges[i].key < gauges[j].key })
	sort.Slice(histograms, func(i, j int) bool { return histograms[i].key < histograms[j].key })

	ew := &errWriter{w: w}

	lastName := ""
	for _, e := range counters {
		metricName := sanitizeMetricName(e.name)
		if e.name != lastName {
			ew.printf("# TYPE %s counter\n", metricName)
			lastName = e.name
		}
		ew.printf("%s%s %s\n", metricName, formatLabels(e.labels), formatFloat(e.value))
	}

	lastName = ""
	for _, e := range gauges {
		metricName := sanitizeMetricName(e.name)
		if e.name != lastName {
			ew.printf("# TYPE %s gauge\n", metricName)
			lastName = e.name
		}
		ew.printf("%s%s %s\n", metricName, formatLabels(e.labels), formatFloat(e.value))
	}

	lastName = ""
	for _, e := range histograms {
		metricName := sanitizeMetricName(e.name)
		if e.name != lastName {
			ew.printf("# TYPE %s histogram\n", metricName)
			lastName = e.name
		}
		bucketLabels := make([]label, len(e.labels), len(e.labels)+1)
		copy(bucketLabels, e.labels)
		bucketLabels = append(bucketLabels, label{"le", "+Inf"})
		sort.Slice(bucketLabels, func(i, j int) bool { return bucketLabels[i].key < bucketLabels[j].key })
		ew.printf("%s_bucket%s %d\n", metricName, formatLabels(bucketLabels), e.count)
		ew.printf("%s_sum%s %s\n", metricName, formatLabels(e.labels), formatFloat(e.sum))
		ew.printf("%s_count%s %d\n", metricName, formatLabels(e.labels), e.count)
	}

	return ew.err
}
