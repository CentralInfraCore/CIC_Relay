package obs_test

import (
	"testing"

	"centralrelay/pkg/obs"
)

// --- chain tests ---

func TestRecordHash_Deterministic(t *testing.T) {
	r := obs.LogRecord{
		Timestamp: "2026-04-05T00:00:00Z",
		Level:     "info",
		Message:   "test",
		Resource:  obs.Resource{RelayID: "r1", Component: "c1"},
	}
	h1, err := obs.RecordHash(r)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := obs.RecordHash(r)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("RecordHash not deterministic: %q != %q", h1, h2)
	}
}

func TestRecordHash_ChangeSensitive(t *testing.T) {
	r1 := obs.LogRecord{Timestamp: "2026-04-05T00:00:00Z", Level: "info", Message: "a",
		Resource: obs.Resource{RelayID: "r1", Component: "c"}}
	r2 := obs.LogRecord{Timestamp: "2026-04-05T00:00:00Z", Level: "info", Message: "b",
		Resource: obs.Resource{RelayID: "r1", Component: "c"}}
	h1, _ := obs.RecordHash(r1)
	h2, _ := obs.RecordHash(r2)
	if h1 == h2 {
		t.Error("different records produced the same hash")
	}
}

func TestGlobalChainHash_Stable(t *testing.T) {
	records := []any{
		obs.LogRecord{Timestamp: "t1", Level: "info", Message: "m1",
			Resource: obs.Resource{RelayID: "r", Component: "c"}},
		obs.CounterPoint{Name: "relay.req.count", Value: 1, Timestamp: "t2",
			Resource: obs.Resource{RelayID: "r", Component: "c"}},
	}
	h1, err := obs.GlobalChainHash(records)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := obs.GlobalChainHash(records)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Error("GlobalChainHash not stable")
	}
}

func TestGlobalChainHash_OrderSensitive(t *testing.T) {
	a := obs.LogRecord{Timestamp: "t1", Level: "info", Message: "a",
		Resource: obs.Resource{RelayID: "r", Component: "c"}}
	b := obs.LogRecord{Timestamp: "t2", Level: "info", Message: "b",
		Resource: obs.Resource{RelayID: "r", Component: "c"}}

	h1, _ := obs.GlobalChainHash([]any{a, b})
	h2, _ := obs.GlobalChainHash([]any{b, a})
	if h1 == h2 {
		t.Error("GlobalChainHash should differ for different record order")
	}
}

func TestTypeChainHash_NamespaceIsolation(t *testing.T) {
	records := []any{
		obs.LogRecord{Timestamp: "t1", Level: "info", Message: "x",
			Resource: obs.Resource{RelayID: "r", Component: "c"}},
	}
	h1, _ := obs.TypeChainHash("log", records)
	h2, _ := obs.TypeChainHash("trace", records)
	if h1 == h2 {
		t.Error("TypeChainHash should differ across type names")
	}
}

// --- registry tests ---

func TestRegistry_Register_ValidPrefix(t *testing.T) {
	reg := obs.NewRegistry()
	err := reg.Register(obs.MetricInstrument{Name: "relay.req.count", Description: "total requests"})
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestRegistry_Register_InvalidPrefix(t *testing.T) {
	reg := obs.NewRegistry()
	err := reg.Register(obs.MetricInstrument{Name: "http.req.count", Description: "x"})
	if err == nil {
		t.Error("expected error for missing relay. prefix")
	}
}

func TestRegistry_Register_DuplicateSame(t *testing.T) {
	reg := obs.NewRegistry()
	inst := obs.MetricInstrument{Name: "relay.x", Description: "d"}
	if err := reg.Register(inst); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(inst); err != nil {
		t.Errorf("re-registering identical instrument should succeed, got: %v", err)
	}
}

func TestRegistry_Register_DuplicateDifferent(t *testing.T) {
	reg := obs.NewRegistry()
	if err := reg.Register(obs.MetricInstrument{Name: "relay.x", Description: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(obs.MetricInstrument{Name: "relay.x", Description: "b"}); err == nil {
		t.Error("expected error when re-registering with different description")
	}
}

func TestRegistry_Get(t *testing.T) {
	reg := obs.NewRegistry()
	inst := obs.MetricInstrument{Name: "relay.latency", Description: "request latency"}
	_ = reg.Register(inst)

	got, ok := reg.Get("relay.latency")
	if !ok {
		t.Fatal("expected instrument to be found")
	}
	if got != inst {
		t.Errorf("got %+v, want %+v", got, inst)
	}

	_, ok = reg.Get("relay.missing")
	if ok {
		t.Error("expected missing instrument not to be found")
	}
}

// --- sink compile check ---

func TestNoopSink_ImplementsAllSinks(t *testing.T) {
	var n obs.NoopSink
	var _ obs.TraceSink = n
	var _ obs.LogSink = n
	var _ obs.MetricSink = n
}
