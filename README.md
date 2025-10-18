# Minishop Observability Playbook

This guide turns Minishop into a sensibly observable service by layering **Logs → Traces → Metrics**, then wiring correlations across all three. It’s aligned with community standards and tooling best practices.

---

## Objectives

1. **Ship structured logs first** so you can investigate incidents today.
2. **Add distributed tracing** with W3C Trace Context and OpenTelemetry so requests are followable across services. ([W3C][1])
3. **Expose Prometheus metrics** with sane names/labels for dashboards and SLOs. ([Prometheus][2])
4. **Correlate everything** via `trace_id`/`span_id` and shared resource attributes. ([OpenTelemetry][3])

---

## Functional Requirements (Minishop)

Scope: Create Order and Process Payment over HTTP. Requirements reflect current service code and domain states.

- API Endpoints
  - POST `/order`
    - Request: `{ "customer_id": string, "product_id": string, "quantity": int, "amount": int64 }`
    - Responses:
      - `201 Created`: `{ "order_id": string, "status": "pending" | "inventory_reserved" | "inventory_failed" | "completed" | "payment_failed" }`
      - `400 Bad Request`: invalid input (missing IDs, quantity <= 0, amount < 0)
      - `500 Internal Server Error`: persistence or unexpected errors
    - Behavior:
      - Validate `customer_id` and `product_id` are non-empty.
      - Create order with `status = pending`; persist to repository.
      - Publish `OrderCreated` event; inventory reservation proceeds asynchronously.
  - POST `/payment/pay`
    - Request: `{ "order_id": string, "amount": int64 }` (amount optional; if > 0 overrides stored amount)
    - Responses:
      - `200 OK`: `{ "order_id": string, "status": "success" | "failed" }`
      - `400 Bad Request`: invalid state (not inventory reserved), negative amount, already completed
      - `404 Not Found`: order does not exist
      - `500 Internal Server Error`: update or unexpected errors
    - Behavior:
      - Require existing order. Reject if already `completed`.
      - Allow only when order is `inventory_reserved` or `payment_failed`.
      - Simulate payment with ~70% success; on success, order -> `completed`; on failure, order -> `payment_failed`.

- Order Domain and States
  - States: `pending`, `inventory_reserved`, `inventory_failed`, `completed`, `payment_failed`.
  - Transitions:
    - `pending` -> `inventory_reserved` on successful reservation.
    - `pending` -> `inventory_failed` on reservation failure.
    - `inventory_reserved` -> `completed` on payment success.
    - `inventory_reserved` -> `payment_failed` on payment failure.
    - `payment_failed` -> `completed` on subsequent payment success.
  - Validation: `quantity > 0`; `amount >= 0`.

- Inventory Reservation (async)
  - On `OrderCreated`, inventory service attempts `Reserve(product_id, quantity)`.
  - Emits `InventoryReserved` or `InventoryReservationFailed` events.
  - Order worker updates the order state accordingly.

- Error Mapping (HTTP)
  - Not Found -> `404`.
  - Invalid Quantity/Amount and similar domain validation -> `400`.
  - All other errors -> `500`.

- Health
  - GET `/health` responds `200 OK` with body `ok`.

---

## Reference Architecture

* **Logs:** App emits one-line JSON → ingested by **OpenTelemetry Collector** (filelog or OTLP) → stored in **Loki** → queried in **Grafana**. ([Grafana Labs][9])

* **Traces:** App uses **OpenTelemetry Go** → OTLP/HTTP to **Tempo** → Explore in Grafana. Tempo quick start exposes OTLP on 4318. ([Grafana Labs][5])

* **Metrics:** App exposes `/metrics` → scraped by **Prometheus** → visualized in Grafana. Use Prometheus naming and label discipline. ([Prometheus][2])

OpenTelemetry is the industry-standard, vendor-neutral framework we’ll use for instrumentation and export. ([OpenTelemetry][6])

---

## Prerequisites

* Go 1.21+ (OTel Go docs currently target 1.23+, but examples below work on 1.21+). ([OpenTelemetry][7])
* Docker + docker-compose for Loki/Tempo/Prometheus/Grafana.

---

## Step 1 — Logging (do this first)

### Logging rules

* **Structured JSON**, one event per line.
* **Required fields:** `time`, `level`, `msg`, `service`, `env`, `trace_id`, `span_id`, `use_case`, plus business fields like `order_id`, `product_id`, `amount`.
* **Never** put unbounded, sensitive, or PII in fields.
* Emit to **stdout** (container-friendly). If you also write to a file that’s fine for local dev.

Use a high-performance structured logger like **zap** for consistent, low-overhead JSON. ([Go Packages][8])

#### Go: production logger

```go
import "go.uber.org/zap"

func NewLogger(service, env string) *zap.Logger {
	l := zap.Must(zap.NewProduction())
	return l.With(
		zap.String("service", service),
		zap.String("env", env),
	)
}
```

#### Log ingestion via OTel Collector

Use the OpenTelemetry Collector to ingest logs and forward to Loki. This repo includes a ready-to-use config that tails local JSON log files and maps fields to Loki labels.

Flow: App (JSON logs) → file → OTel Collector (filelog) → Loki. ([Grafana Labs][9])

Loki indexes **labels** not log content. Keep labels small-cardinality and searchable; parse JSON content in queries with `| json` when needed. ([Grafana Labs][11])

---

## Step 2 — Tracing (OpenTelemetry → Tempo)

### Standards

* **W3C Trace Context**: propagate `traceparent` and `tracestate` headers so traces don’t break between services and vendors. ([W3C][1])

### OTel Go setup

Get the deps:

```
go get go.opentelemetry.io/otel \
       go.opentelemetry.io/otel/sdk/trace \
       go.opentelemetry.io/otel/sdk/resource \
       go.opentelemetry.io/otel/semconv/v1.24.0 \
       go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp \
       go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp
```

Create a `TracerProvider` with resource attributes (service name, version, environment) and an OTLP/HTTP exporter to Tempo (4318):

```go
import (
	"context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

func SetupTracing(ctx context.Context, service, version, endpoint string) (func(context.Context) error, error) {
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(endpoint), otlptracehttp.WithInsecure())
	if err != nil { return nil, err }

	res, _ := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(service),
			semconv.ServiceVersion(version),
		),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
```

Instrument inbound HTTP with `otelhttp` so a **root span** is created per request and W3C headers are automatically handled. ([OpenTelemetry][7])

```go
mux := http.NewServeMux()
mux.Handle("/orders", otelhttp.NewHandler(ordersHandler, "HTTP.Orders"))
```

Within your use case, create **child spans** and set meaningful attributes; on error, **record** and **set span status**:

```go
tr := otel.Tracer("minishop")
ctx, span := tr.Start(ctx, "UC.CreateOrder",
    attribute.String("tenant_id", tenantID),
    attribute.String("use_case", "CreateOrder"),
)
defer span.End()

orderID, err := svc.CreateOrder(ctx, req)
if err != nil {
    span.RecordError(err)
    span.SetStatus(codes.Error, "create_order_failed")
    return err
}
span.SetAttributes(attribute.String("order.id", orderID))
```

Tempo: run via Docker Compose and add as a Grafana data source; view traces in **Explore**. ([Grafana Labs][5])

---

## Step 3 — Metrics (Prometheus)

### Naming and labels

* Follow Prometheus conventions: lowercase, underscores, include the **unit** in the metric name (e.g., `_seconds`, `_bytes`, `_total`). ([Prometheus][2])
* Keep label cardinality **bounded**; never use UUIDs or free-text as labels. ([Prometheus][12])

### What to expose

* **Use case RED:**

  * `usecase_requests_total{use_case, outcome}` (counter)
  * `usecase_duration_seconds{use_case}` (histogram)

* **Outbound dependencies:**

  * `external_requests_total{service,endpoint,outcome}`
  * `external_request_duration_seconds{service,endpoint}`

These map to the SRE “Golden Signals” (latency, traffic, errors, saturation). ([Google SRE][13])

### Go: Prometheus handler and instruments

```go
import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	usecaseReqs = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "usecase_requests_total", Help: "Use case requests"},
		[]string{"use_case","outcome"},
	)
	usecaseDur = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "usecase_duration_seconds",
			Help:    "Use case duration",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"use_case"},
	)
)

func init() {
	prometheus.MustRegister(usecaseReqs, usecaseDur)
}

func MetricsServer() {
	http.Handle("/metrics", promhttp.Handler())
	_ = http.ListenAndServe(":9090", nil)
}
```

---

## Cross-cutting Conventions

### Correlate logs, traces, and metrics

* Put `trace_id`/`span_id` into **logs** so you can jump between logs and traces. OpenTelemetry’s log spec highlights carrying the same **Resource context** across signals for correlation. ([OpenTelemetry][3])
* Use the same stable keys across signals: `use_case`, `endpoint`, `tenant_id`.

### Error propagation and single-point logging

Do not log in deep layers. Wrap errors with typed context (operation, kind, code, fields) and **log once** at the boundary (use case or inbound adapter). This keeps logs sparse and consistent with trace attributes (OTel Go encourages recording rich context on spans). ([OpenTelemetry][7])

```go
// collect fields from wrapped errors, then:
logger.Error("use_case_failed",
    zap.String("use_case","CreateOrder"),
    zap.String("trace_id", traceIDFrom(ctx)),
    zap.String("err.kind", string(kind)),
    zap.String("err.code", code),
    zap.Error(err),
)
```

### Sampling and retention

* Start with **head sampling** 1–5% for traces; add **tail sampling** for errors/slow requests with Grafana Agent/OTel Collector when volumes grow. ([Grafana Labs][14])
* Keep logs hot for 7–14 days; traces 3–7 days; metrics long-term.

---

## Minimal docker-compose (platform layer)

> Snippets only; adapt to your environment.

```yaml
services:
  loki:
    image: grafana/loki:3.0.0
    ports: ["3100:3100"]
    command: -config.file=/etc/loki/local-config.yaml

  tempo:
    image: grafana/tempo:latest
    command: ["-config.file=/etc/tempo.yaml"]
    ports: ["3200:3200", "4317:4317", "4318:4318"]
    volumes:
      - ./observability/tempo.yaml:/etc/tempo.yaml:ro

  otel-collector:
    image: otel/opentelemetry-collector:latest
    command: ["--config=/etc/otelcol-config.yaml"]
    ports: ["4317:4317", "4318:4318"]
    volumes:
      - ./observability/otelcol-config.yaml:/etc/otelcol-config.yaml:ro
      - ./logs:/var/log/minishop
    depends_on: [loki, tempo]

  grafana:
    image: grafana/grafana:11.0.0
    ports: ["3000:3000"]
    environment:
      - GF_SECURITY_ADMIN_USER=admin
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_USERS_ALLOW_SIGN_UP=false
    volumes:
      - grafana-storage:/var/lib/grafana
      - ./observability/provisioning:/etc/grafana/provisioning
      - ./observability/dashboards:/var/lib/grafana/dashboards

volumes:
  grafana-storage:
```

Local run tips

- Create a local log folder and run the app with file logging enabled so the Collector can ingest logs:
  - `mkdir -p logs`
  - `LOG_FILE=./logs/app.log go run ./app`
- Start the platform: `docker compose up -d`
- Grafana has Loki and Tempo data sources pre-provisioned.
- Verify Tempo ingestion in Grafana Explore. ([Grafana Labs][15])

---

## Verification Checklist

1. **Logs** show up in Loki; query `{app="minishop"} | json` and filter by `order_id` or `level="error"`. Loki groups streams by **labels**; keep label sets small. ([Grafana Labs][11])
2. **Traces** appear in Tempo; you can search by service, then open a trace and spans. ([Grafana Labs][5])
3. **Metrics** exposed on `/metrics` and visible in Prometheus; graph `rate(usecase_requests_total[5m])` and `histogram_quantile(0.95, sum by (le,use_case) (rate(usecase_duration_seconds_bucket[5m])))`. Conventions match Prometheus guidelines. ([Prometheus][2])

---

## Go Reference Patterns

### A) Unified observability “ports” for loose coupling

OpenTelemetry Go docs recommend using the API/SDK and exporters; hide the concrete exporter behind your own interfaces so the app isn’t vendor-bound. ([OpenTelemetry][7])

```go
type TraceCtx interface {
	Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span)
}

type Telemetry interface {
	Counter(name string) prometheus.Counter
	Histogram(name string) prometheus.Observer
	Logger() *zap.Logger
}
```

### B) Use case template (RED + span status)

```go
func (uc *CreateOrderUC) Execute(ctx context.Context, cmd CreateOrderCmd) (err error) {
	tr := otel.Tracer("minishop")
	ctx, span := tr.Start(ctx, "UC.CreateOrder",
		attribute.String("tenant_id", cmd.TenantID),
		attribute.String("use_case", "CreateOrder"),
	)
	start := time.Now()
	defer func() {
		outcome := "ok"
		status := codes.Ok
		code := "OK"
		if err != nil {
			outcome, status, code = "error", codes.Error, "CREATE_ORDER_FAILED"
			span.RecordError(err)
		}
		span.SetStatus(status, code)
		span.End()

		usecaseReqs.WithLabelValues("CreateOrder", outcome).Inc()
		usecaseDur.WithLabelValues("CreateOrder").Observe(time.Since(start).Seconds())

		uc.log.Info("use_case_done",
			zap.String("use_case","CreateOrder"),
			zap.String("outcome", outcome),
			zap.Int64("latency_ms", time.Since(start).Milliseconds()),
			zap.String("trace_id", traceIDFrom(ctx)),
			zap.String("tenant_id", cmd.TenantID),
		)
	}()

	// business…
	return uc.repo.Save(ctx, cmd.toOrder())
}
```

### C) Outbound adapter (child span + error wrapping)

```go
func (c *PaymentClient) Authorize(ctx context.Context, amount int64) error {
	tr := otel.Tracer("minishop")
	ctx, span := tr.Start(ctx, "http.client Payment.Authorize",
		attribute.String("peer.service","payments"),
		attribute.Int64("payment.amount", amount),
	)
	defer span.End()

	req, _ := http.NewRequestWithContext(ctx, "POST", c.endpoint, nil)
	// otelhttp client would auto-instrument; shown explicitly for clarity
	resp, err := c.client.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "network_error")
		return fmt.Errorf("payment authorize: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
		span.SetStatus(codes.Error, "rejected")
		return fmt.Errorf("payment rejected: status=%d", resp.StatusCode)
	}
	return nil
}
```

### D) HTTP middleware (access log + trace propagation)

```go
func Access(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			h := otelhttp.NewHandler(next, "HTTP.Inbound") // propagates W3C headers
			h.ServeHTTP(w, r)

			log.Info("http_access",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int64("latency_ms", time.Since(start).Milliseconds()),
			)
		})
	}
}
```

---

## Governance and Guardrails

* **Naming & units:** Always include the unit in metric names and use underscores and lowercase. ([Prometheus][2])
* **Label hygiene:** Avoid high-cardinality labels (UUIDs, timestamps, free-text). Plan label schema ahead. ([Chronosphere Documentation][16])
* **Golden Signals + RED/USE:** Start with the Four Golden Signals; RED for request paths, USE for shared resources like DB pools. ([Google SRE][13])
* **Trace sampling:** Head sample by default; consider tail sampling by duration/error for cost control. ([Grafana Labs][14])
* **Log pipeline:** Use OTel Collector → Loki via filelog/OTLP; avoid Promtail for new setups. ([Grafana Labs][9])

---

## What “Good” Looks Like

* A single `trace_id` lets you pivot between **Grafana Explore** for Tempo and Loki, because your logs include `trace_id` and your spans carry business attributes (`order.id`, `payment.amount`). ([Grafana Labs][15])
* Your Prometheus metrics are queryable and predictable because they follow the naming and label rules, not “whatever someone typed last sprint.” ([Prometheus][2])

---

### Appendix: Why these choices match community consensus

* OTel is the vendor-neutral standard across traces, metrics, and logs, with broad vendor support. ([OpenTelemetry][6])
* W3C Trace Context guarantees header compatibility across tools so traces don’t break between services. ([W3C][1])
* Prometheus prescribes explicit units and disciplined label usage; that’s why this guide insists on `_seconds/_bytes/_total` suffixes and bounded labels. ([Prometheus][2])


[1]: https://www.w3.org/TR/trace-context/?utm_source=chatgpt.com "Trace Context"
[2]: https://prometheus.io/docs/practices/naming/?utm_source=chatgpt.com "Metric and label naming"
[3]: https://opentelemetry.io/docs/specs/otel/logs/?utm_source=chatgpt.com "OpenTelemetry Logging"
[4]: https://grafana.com/docs/loki/latest/send-data/promtail/stages/labels/?utm_source=chatgpt.com "labels | Grafana Loki documentation"
[5]: https://grafana.com/docs/tempo/latest/getting-started/docker-example/?utm_source=chatgpt.com "Quick start for Tempo"
[6]: https://opentelemetry.io/docs/?utm_source=chatgpt.com "Documentation"
[7]: https://opentelemetry.io/docs/languages/go/?utm_source=chatgpt.com "Go"
[8]: https://pkg.go.dev/go.uber.org/zap?utm_source=chatgpt.com "zap package - go.uber.org/zap - Go ..."
[9]: https://grafana.com/docs/loki/latest/send-data/otel/otel-collector-getting-started/?utm_source=chatgpt.com "Getting started with the OpenTelemetry Collector and Loki ..."
[10]: https://grafana.com/docs/loki/latest/send-data/promtail/stages/json/?utm_source=chatgpt.com "json | Grafana Loki documentation"  
(Reference only; this repo uses OTel Collector instead of Promtail.)
[11]: https://grafana.com/docs/loki/latest/get-started/labels/?utm_source=chatgpt.com "Understand labels | Grafana Loki documentation"
[12]: https://prometheus.io/docs/concepts/data_model/?utm_source=chatgpt.com "Data model"
[13]: https://sre.google/sre-book/monitoring-distributed-systems/?utm_source=chatgpt.com "sre golden signals"
[14]: https://grafana.com/blog/2022/05/11/an-introduction-to-trace-sampling-with-grafana-tempo-and-grafana-agent/?utm_source=chatgpt.com "An introduction to trace sampling with Grafana Tempo and ..."
[15]: https://grafana.com/docs/tempo/latest/api_docs/pushing-spans-with-http/?utm_source=chatgpt.com "Push spans with HTTP | Grafana Tempo documentation"
[16]: https://docs.chronosphere.io/ingest/metrics-traces/collector/mappings/prometheus/prometheus-recommendations?utm_source=chatgpt.com "Prometheus metric naming recommendations"
