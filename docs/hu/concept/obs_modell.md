# Observability-réteg a CIC Relay-ben

A CIC Relay-ben az megfigyelhetőség (observability) két, egymástól szándékosan elkülönített rétegen valósul meg.
Ez a doc a második réteget — az operatív observability-réteget — írja le, annak modelljét, integritási mechanizmusát és a `ProofTrace`-hez való viszonyát.

---

## A két réteg

### Truth layer — `ProofTrace` (megvalósítva)

A `ProofTrace` a rendszer kanonikus, kriptográfiailag verifikálható végrehajtási lánca.

Tulajdonságai:
- determinisztikus (nincs clock, nincs RNG, nincs oldalhatás)
- SHA-256 hash-lánc minden workflow step input/output-jára
- canonical JSON formátum (`pkg/canonicaljson`)
- forráshorgony: `SourceDigest` az első elem a lánc-hash számításában

**Ez a réteg a bizonyítási infrastruktúra — nem helyettesíthető és nem váltható ki.**

### Observability layer — (tervezett)

Az operatív megfigyelhetőségi réteg: strukturált log, metrika, végrehajtási keret.

Célja: üzemeltetési láthatóság, latency, hibák, korrelálható események.

**Nem bizonyítási réteg** — nem verifikálható utólag kriptográfiai értelemben (de integritásvédett).

---

## Miért nem OpenTelemetry SDK?

Az OTel SDK nem kerül a rendszerbe közvetlen függőségként. Okok:

| Ütközési pont | OTel viselkedés | Relay elvárás |
|---|---|---|
| Idő | wall-clock alapú | ProofTrace-ben tiltott |
| Sampling | nem determinisztikus | nincs mintavételezés a truth layerben |
| Context propagation | `context.Context`-be injektált span | explicit, oldalhatás-mentes struct |
| Teljesség garancia | best-effort | auditban kötelező |

Az OTel-t **specifikációs inspirációként** használjuk, nem runtime-függőségként.
Az adat-modell (log/metric/trace fogalmi struktúra) OTel-kompatibilis — a jövőbeli export lehetséges adapter rétegen keresztül.

---

## A három jeltípus

### LogRecord

Strukturált eseményrekord — nem szöveges napló, hanem típusos adat, amelynek a szöveg csak nézete.

Kötelező mezők:
- `timestamp_unix_ns` — megfigyelt wall-clock idő (observability layer)
- `severity_number` / `severity_text` — OTel-kompatibilis numerikus skála
- `event_name` — kanonikus, pontozott névtér (pl. `relay.validation.failed`)
- `category` — kontrollált enum (lifecycle, transport, workflow, validation, proof, security, config, performance)
- `execution_ref` — `ExecutionContext` hivatkozás
- `attributes` — `map[string]string`, allowlistelt kulcsok

Opcionális:
- `trace_ref` — `TraceFrame` hivatkozás
- `proof_ref` — `ProofTrace` lépés hivatkozása (hash, index)
- `error` — strukturált hibarekord

**A logrekordok hash-láncoltak** (lásd: Integritásmodell).

### TraceFrame

Egy végrehajtási szakasz operatív reprezentációja — az OTel span konceptuális megfelelője, de saját névterű.

Kötelező mezők:
- `frame_id`, `trace_id`, `parent_frame_id` (opcionális)
- `name` — kanonikus, pontozott névtér (pl. `relay.workflow.step.validate`)
- `kind` — enum: server, client, internal, validation, proof, storage, transport, adapter
- `status` — enum: ok, error, timeout, rejected, cancelled, partial
- `started_at_unix_ns`, `ended_at_unix_ns`, `duration_ns` — kizárólag az observability rétegben

A `started_at` / `ended_at` wall-clock mezők **nem kerülnek a ProofTrace számításba**.
A TraceFrame sosem a truth layer inputja.

### MetricPoint

Három instrumenttípus:

- **Counter**: monoton növekvő számláló (`relay.requests.total`, `relay.validation.failures.total`)
- **Gauge**: pillanatnyi érték (`relay.queue.depth`, `relay.active.workflows`)
- **Histogram**: eloszlás és latency (`relay.request.duration`, `relay.validation.duration`)

Minden MetricPoint tartalmaz:
- `name` — kontrollált névtér, `MetricInstrument` regiszterben definiált
- `timestamp_unix_ns`
- `attributes` — alacsony kardinalitású, allowlistelt kulcsok (nem kerülhet be: `request_id`, teljes proof hash, nyers user input)

---

## ExecutionContext

Az observability réteg korrelációs egysége. **Explicit struct** — nem `context.Context`-be injektált baggage.

```
ExecutionContext:
  execution_id        — egyedi végrehajtási azonosító
  request_id          — HTTP request szintű azonosító
  correlation_id      — cross-service korreláció
  proof_trace_id      — ProofTrace hivatkozás
  workflow_id         — workflow szintű azonosító
  workflow_step       — aktuális lépés neve
  parent_execution_id — szülő végrehajtás (opcionális)
  start_kind          — http | timer | internal | callback | replay
```

Ez az explicit struct kerül átadásra minden log/trace/metric emisszióhoz — nincs implicit runtime state.

---

## Integritásmodell

Az observability réteg nem csak operatív adat — **integritásvédett**.

### LogRecord hash-lánc

Minden logrekord kap saját kanonikus hash-t:
```
record_hash = SHA256(canonical(record_without_envelope))
```

A rekordok globális, append-only láncba fűződnek:
```
global_chain_hash[n] = SHA256(global_chain_hash[n-1] || record_hash[n])
```

Emellett típusonkénti lánc (szelektív törlés elleni védelem):
```
type_chain_hash[type][n] = SHA256(type_chain_hash[type][n-1] || record_hash[n])
```

A típus a `category` + `event_name` kombináció — kanonikus taxonomy, nem szabad szöveg.

### Digitális aláírás

Minden rekord kap egy `IntegrityEnvelope`-ot:
```
IntegrityEnvelope:
  record_hash
  global_chain_hash
  type_chain_hash
  sequence_no
  type_sequence_no
  signer_id
  key_id
  signature = Sign(record_hash || global_chain_hash || type_chain_hash || sequence_no)
```

Az aláírás kulcsát a Vault transit engine biztosítja (a meglévő `VaultCryptoService` infrastruktúra).

### Metrika integritása

A metrikánál a **loglánc a gerinc**, nem önálló metrikalánc.

- Minden metrika-emisszió és metrika-válasz kap saját hash-t és aláírást
- A `metric.response.issued` log event hivatkozza a metrikaválasz hash-ét és aláírását
- Így a bizonyítás a logláncban él: `"ezt a metrikaválaszt ez a komponens adta, ez a logba rögzítve van"`

Külön metrikalánc csak akkor szükséges, ha a `MeasurementRecord` szintű teljesség bizonyítása is cél.

---

## Policy réteg

Minden log/trace/metric emisszió átmegy egy policy rétegen mielőtt a sinkbe kerül.

Feladatai:
- attribútum allowlist érvényesítése
- PII/secret redaction
- kardinalitás cap (metrikáknál)
- rate limiting
- severity threshold
- tenant izoláció

A policy `ObservabilityPolicy` struktúrában definiált, konfiguráció-vezérelt.

---

## Sink interfészek

A belső modell nem kötődik közvetlenül backendhez:

```go
type TraceSink interface { EmitFrame(TraceFrame) error }
type LogSink   interface { EmitLog(LogRecord) error }
type MetricSink interface {
    EmitCounter(CounterPoint) error
    EmitGauge(GaugePoint) error
    EmitHistogram(HistogramPoint) error
}
```

Első implementáció: in-memory + stderr/JSONL.
Jövőbeli: OTLP exporter (OTel projection adapter), Prometheus bridge.

---

## Projekciós réteg (jövőbeli)

Két irány:

**A) ProofTrace → TraceFrame projection**
A domain chain operatív nézetre fordítása (read-only, one-way).

**B) Internal observability → OTel/OTLP export projection**
A belső log/metric/trace modell külső OTel-kompatibilis formára fordítása.

Invariáns: a projekció sosem ír vissza a domain modellbe. Az OTel export derivált nézet, nem igazságforrás.

---

## Időmodell

A rendszerben kétféle idő él:

| Idő típus | Hol él | Mire való |
|---|---|---|
| `logical_time` | ProofTrace (step index, sorrend) | bizonyítás, kauzalitás |
| `observed_time` | TraceFrame, LogRecord (wall-clock) | latency, monitoring |

A `observed_time` sosem kerül a ProofTrace lánc-hash számításba.

---

## Naming convention

**TraceFrame nevek:**
`relay.http.request`, `relay.workflow.execute`, `relay.workflow.step.validate`,
`relay.proof.append`, `relay.storage.read`, `relay.external.call`, `relay.security.ca_check`

**LogRecord event nevek:**
`relay.request.accepted`, `relay.request.rejected`, `relay.validation.failed`,
`relay.workflow.step.started`, `relay.workflow.step.completed`,
`relay.proof.chain.appended`, `relay.config.invalid`, `relay.dependency.timeout`,
`relay.metric.response.issued`

**Metric nevek:**
`relay.requests.total`, `relay.request.duration`, `relay.validation.failures.total`,
`relay.validation.duration`, `relay.proof.append.duration`, `relay.queue.depth`,
`relay.retries.total`, `relay.external.errors.total`

---

## Kapcsolódó dokumentumok

- [`proof_trace.go`](../../../core/cabinet/proof_trace.go): a truth layer implementációja
- [`allapotmodell.md`](./allapotmodell.md): hogyan értelmezi a relay az objektumok állapotát
- [`dependency_modell.md`](./dependency_modell.md): végrehajtási feltételrendszer