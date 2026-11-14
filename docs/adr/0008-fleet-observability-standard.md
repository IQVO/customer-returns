# 8. Adopt the fleet observability standard (logs, metrics, traces)

## Status

Accepted (2026-10-26).

## Context

customer-returns logged JSON with `log/slog` and nothing else: no trace or span id on a log line, no request id, no request log, no metrics, no traces and no
OpenTelemetry settings in the Helm chart. (The outbox already copied the W3C trace context into the stored headers, see below, but nothing produced a
span to copy and no consumer read it.) The warehouse-systems services (inventory-storage, wes-work-planning) share one pipeline and one convention, recorded in
their "standard metrics convention" ADRs (inventory-storage ADR 0016, wes-work-planning ADR 0013), and customer-ordering adopted it in its ADR 0007:

- pods export OTLP/gRPC to the in-cluster Collector (`otel-collector.observability.svc.cluster.local:4317`), which feeds Jaeger (traces) and a Prometheus
  exporter (metrics);
- logs stay on stdout as JSON for Alloy and Loki.

Without the same wiring a return could not be followed from the REST call through the Kafka event to the consumer in another context, a log line could not
be joined to its trace and no dashboard could show ship rate, errors or latency. A service-specific setup was rejected (every repo should answer the same query
the same way), and so were the OTel Go auto-instrumentation agents (they cannot follow a trace across `segmentio/kafka-go` nor name spans by the chi route
pattern).

## Decision

Tier 1 (identical in every repo) and Tier 2 (this context's business metric):

1. **Telemetry adapter.** `internal/adapters/outbound/telemetry` holds `Setup` (OTLP/gRPC traces and metrics, W3C TraceContext + Baggage propagator, Go runtime
   metrics; exporters dial lazily, so no Collector means dropped telemetry, never a blocked boot or request; returns a shutdown func) and `WithTraceContext`
   (stamps `trace_id`/`span_id` on a log record written with a span-carrying context). Only the composition roots (`cmd/api`, `cmd/mcp`) and the other adapters
   import it; `internal/domain` and `internal/application` never import OpenTelemetry.
2. **Logger.** `slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, ...)))`, `LOG_LEVEL`, uppercase levels. Code on a request or message path
   uses the `*Context` slog variants (the Kafka consumers already did; the MCP tool error log now does).
3. **HTTP middleware order** on both chi routers (REST and MCP): `RequestID` -> `otelchi.Middleware(serviceName, WithChiRoutes(r))` ->
   `otelchimetric.NewServerRequestDuration` -> `RequestLogger` -> `Recoverer`; the existing `rejectNUL` and Idempotency-Key middleware stay after them.
   `telemetry.Setup` runs before the router is built (otelchimetric binds the global MeterProvider at construction) and is flushed last on shutdown. The request
   line carries `method`, `route` (the chi pattern, never the raw path, which holds ids), `status`, `duration_ms`, `bytes` and `request_id`; 5xx is ERROR, 4xx
   WARN, the rest INFO; `/healthz` and `/readyz` are not logged. `RequestLogger` lives in `internal/adapters/inbound/requestlog`, not in the REST adapter,
   because `cmd/mcp` may not import the REST adapter (`TestMCPServerIsReadOnly`) and both routers must log identically.
4. **Traces over Kafka.** `OutboxRepo.Insert` stamps `traceparent` (and `tracestate`/`baggage` when present) from the active span into the stored outbox
   headers, inside the transaction that writes the aggregate; this predates the ADR (gap fix D27) and is kept as is, now covered by a unit test and a
   testcontainers test. The relay publishes the headers unchanged as Kafka headers; each inbound consumer (`handleTraced`) extracts them and runs every handling
   attempt in a consumer span named `<topic> process`, so the use case's logs carry the publisher's trace id and an event enqueued while handling continues the
   trace. Only extra headers are added: the CloudEvents attributes and body, and the encoder's output, are unchanged (ADR 0004).
5. **Environment contract.** `cmd/api` and `cmd/mcp` read `OTEL_SERVICE_NAME` (defaults `customer-returns` and `customer-returns-mcp`), `OTEL_EXPORTER_OTLP_ENDPOINT`
   (default `telemetry.DefaultEndpoint`, a bare `host:port` or a URL), `SERVICE_VERSION`, `ENVIRONMENT` (read by `Setup`) and `LOG_LEVEL`.
6. **Chart.** `otel.enabled`, `otel.endpoint`, `otel.serviceName` render `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME` (`<name>-mcp` for the mcp
   component); `SERVICE_VERSION` is the image tag, or the chart appVersion when empty, for the api and the mcp component (`mcp.serviceVersion` now defaults to
   empty and only overrides); the api also renders `ENVIRONMENT` from `environment`. These variables are rendered straight into the container env, not into the
   api ConfigMap (the ConfigMap holds exactly the variables `cmd/api/main.go` reads), and `extraEnv` refuses all four. `charts/customer-returns/tests/render_test.sh`
   asserts it and scans `cmd/mcp` (main.go and telemetry.go) for the variables the binary reads.
7. **Tier 2 business metric.** One counter, `customer_returns.return.authorized`, unit `{return}`, attribute `outcome` (`authorized` | `rejected`); on
   `authorized` a `disposition` attribute (`KEEP` | `NEAREST_RETURNS_CENTER` | `ORIGIN_NODE`), on a rejection a `reason` attribute (the problem slug the REST
   adapter answers with: `group-not-shipped`, `no-return-destination`, `validation-failed`, `not-found`, `illegal-transition`, `concurrent-update`,
   `internal-error`). `ports.ReturnMetrics` is the application port; `AuthorizeReturn` calls it once per attempt, after the unit of work finished; the OTel
   implementation is in the telemetry adapter and is injected from `cmd/api`; a nil `Deps.Metrics` is a no-op. It follows the fleet convention: dotted lowercase
   name, one counter with an `outcome` attribute rather than two, UCUM unit, and a description that says what a human should conclude when it moves.

**Why this counter** (ADR 0001, `apis/openapi.yaml`): the context exists to decide a return's disposition from its own read models and hand the package to the
network. Authorizing is the one command that crosses every business gate at once (the group SHIPPED or DELIVERED, the lines resolved, a destination decided), so
its outcome is the headline. The slug in `reason` separates a lagging OrderGroupView (`group-not-shipped`) from regions lacking returns centres
(`no-return-destination`) and from a malformed request, which is what an operator needs to decide whom to call; the `disposition` split on the authorized side
shows the KEEP share against the physical share. Labels, cancellations and outcomes are already domain facts with their own events.

Not part of this ADR: dashboards and alert rules, which live in warehouse-infra and are generated per deployed context from the instrument names above.

## Assumptions

- A1. The existing outbox trace stamping is kept in `Insert` (where the aggregate and the outbox row are written) rather than moved to a separate `Append`
  wrapper as in customer-ordering; the behaviour (headers carry `traceparent` under a span, nothing without one) is the standard's.
- A2. An authorize attempt that fails on the first try with a concurrency conflict and succeeds on the retry counts once, as `authorized`.
- A3. `reason` values are the REST problem slugs; any error with no slug is `internal-error`.

## Consequences

- A request, its log lines, the events it enqueued and the consumers that handle them in other services share one trace id; `trace_id` in Loki jumps to Jaeger.
- Request rate, errors and duration come from `http.server.request.duration` (otelchimetric) with no hand-rolled counter; ship health from
  `customer_returns.return.authorized`.
- Every new log call on a request or message path must use a `*Context` variant, or its line carries no trace id.
- A consumer span is created per handling attempt, so a retried message shows as several spans under the same parent.
- The trace parent of a consumed event is the span active at the outbox write (the REST request or the upstream consumer span), not a separate publish span:
  the relay is asynchronous and adds no span of its own.
