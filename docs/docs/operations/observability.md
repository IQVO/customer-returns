---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
description: "OpenTelemetry metrics, traces and structured logs of the five customer-returns binaries, the exporter variables, and the dashboard and alerts that exist in warehouse-infra."
---

# Observability

All five binaries (`cmd/api`, `cmd/mcp`, `cmd/a2a`, `cmd/projector`, `cmd/reports`) follow the same fleet observability
standard ([ADR 0008](/docs/adr/0008-fleet-observability-standard)): OpenTelemetry traces and metrics pushed over
OTLP/gRPC to a Collector, JSON logs on stdout that carry `trace_id` and `span_id`, and no Prometheus scrape endpoint.
Export never blocks boot: the gRPC exporters connect lazily, so an absent Collector costs telemetry, never availability.

Source: `internal/adapters/outbound/telemetry/telemetry.go` (`Setup`), and one `telemetry.go` per binary
(`cmd/api/telemetry.go`, `cmd/mcp/telemetry.go`, `cmd/a2a/telemetry.go`, `cmd/projector/telemetry.go`,
`cmd/reports/telemetry.go`).

## Exporter configuration

| Variable | Default | Effect |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC endpoint (`host:port` or URL). Used for traces and metrics. |
| `OTEL_SERVICE_NAME` | `customer-returns` (`cmd/api`), `customer-returns-mcp`, `customer-returns-a2a`, `customer-returns-projector`, `customer-returns-reports` | `service.name` resource attribute and the otelchi server name. |
| `SERVICE_VERSION` | `dev` | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | `deployment.environment.name` resource attribute. |
| `LOG_LEVEL` | `info` | Log threshold; the OTel SDK's own diagnostics go through the same slog handler (exporter errors at `debug`, `opentelemetry error`). |

Metrics are pushed every 15 s by a periodic reader. Traces use a batch span processor. The W3C `traceparent` and
`baggage` propagators are installed globally. Full tables per binary: [Configuration](/docs/operations/configuration).

## Metrics

| Instrument | Type | Unit | Attributes | Emitted by | Source |
| --- | --- | --- | --- | --- | --- |
| `customer_returns.return.authorized` | counter (Int64) | `{return}` | `outcome` = `authorized` (with `disposition` = `KEEP`, `NEAREST_RETURNS_CENTER` or `ORIGIN_NODE`) or `rejected` (with `reason` = the problem slug, for example `group-not-shipped`, `no-return-destination`, `validation-failed`) | `cmd/api` | `internal/adapters/outbound/telemetry/return_metrics.go`, behind the `ports.ReturnMetrics` port the authorize use case records on |
| `http.server.request.duration` | histogram (Float64) | `s` | `http.method`, `http.scheme`, `http.route` (chi route pattern, never the raw path); no status code | all five binaries (the projector on its admin port) | otelchi `metric` package, wired in `internal/adapters/inbound/http/server.go`, `internal/adapters/inbound/reportshttp/server.go`, `cmd/mcp/router.go`, `cmd/a2a/router.go`, `cmd/projector/main.go` |
| Go runtime metrics (`go.goroutine.count`, `go.memory.used`, GC and heap series) | gauges / counters | per the OTel Go runtime instrumentation | runtime-defined | all five binaries | `runtime.Start` in `telemetry.Setup` (`go.opentelemetry.io/contrib/instrumentation/runtime`) |

The histogram uses the SDK's default buckets (no explicit bucket view is registered).

`customer_returns.return.authorized` counts every authorize attempt. A rising `group-not-shipped` means the
OrderGroupView lags the shopper; a rising `no-return-destination` means regions lack an ACTIVE returns centre; a rising
`KEEP` share near the 1500-cent threshold is expected returnless behaviour. Completions (`ReturnCompleted`) have no
instrument of their own in v1 (`ReturnMetrics.OutcomeApplied` is a no-op).

**What is not instrumented:** consumer lag, outbox backlog and age, dead-letter counts, analytics projection lag (the
reports' `GET /reports/freshness` answers `lagSeconds` instead). The [Runbook](/docs/operations/runbook) and
[Troubleshooting](/docs/operations/troubleshooting) give the SQL and Kafka reads that replace them.

## Traces

| Span | Created by | Notes |
| --- | --- | --- |
| Server span per HTTP request, named after the chi route pattern | `otelchi.Middleware` in every router (REST, reports, MCP, A2A, projector admin) | One span name per route, not per id. Mounted before the request logger so log lines carry its ids. |
| `<topic> process` (kind `CONSUMER`) | `processTraced` in `internal/adapters/inbound/kafka/tracing.go` | Used by the four `cmd/api` consumers and the analytics consumer of `cmd/projector`. Attributes `messaging.system=kafka`, `messaging.destination.name`, `messaging.operation.name=process`. Parented on the `traceparent` header of the message, a new trace when absent. A handler error is recorded on the span with status `Error`. |
| `a2a.skill <skill>` | `internal/adapters/inbound/a2a/skills.go` | Attribute `a2a.skill`. Status `Error` when the skill answers a problem. |

Trace propagation across the outbox: when a use case appends an event, `internal/adapters/outbound/postgres/outbox_repository.go`
injects the active span's `traceparent` into the row's headers, and the relay puts it on the Kafka message. Consumers of
this service extract it the same way, so a trace follows an event across services.

## Logs

JSON via `log/slog` on stdout; `time`, `level`, `msg` plus the fields below. The handler is wrapped by
`telemetry.WithTraceContext`, which stamps `trace_id` and `span_id` on every record written with a context that holds a
span (the `*Context` slog calls). Boot, shutdown and background-loop lines have none.

| Where | Level | `msg` | Fields |
| --- | --- | --- | --- |
| Request (every HTTP binary) | INFO / WARN (4xx) / ERROR (5xx) | `http request` | `method`, `route` (pattern or `unmatched`), `status`, `duration_ms`, `bytes`, `request_id`. `/healthz` and `/readyz` are not logged. |
| REST | ERROR | `request failed` (an unmapped error, answered 500 `internal-error`) | `error`, `method`, `path` (CR and LF removed by `sanitizeLog`, `internal/adapters/inbound/http/logsafe.go`) |
| MCP / A2A | ERROR | `mcp tool failed with an unexpected error`, `a2a skill failed` | `error` |
| Boot | INFO | `telemetry configured` | `service_name`, `otlp_endpoint` |
| Boot | INFO | `postgres adapters configured` (`cmd/api`), `postgres repositories configured` (`cmd/mcp`, `cmd/a2a`), `http server listening` | `addr` |
| Boot | INFO | `mcp server starting`, `a2a server starting`, `projector starting`, `reports starting` and the matching `... listening` lines | `version`, `environment`, addresses (`agent_url` for A2A) |
| Boot retry | WARN / INFO | `retrying` / `succeeded after retry` | `op`, `attempt`, `in`, `err` |
| Boot | WARN | `invalid OUTBOX_RELAY_INTERVAL; using the default`, `invalid OUTBOX_MAX_ATTEMPTS; using the default` | `value`, `default` |
| Exit | ERROR | `service exited with error` / `mcp server exited with error` / `a2a server exited with error` / `projector exited with error` / `reports exited with error` (exit 1) | `error` |
| Consumers | INFO | `consumer running` | `consumer` (`customer-ordering`, `node-topology`, `order-sourcing`, `vendor-connectivity`), `topic`, `brokers` |
| Consumers | WARN | `skipping a message that is not a valid CloudEvent`, `skipping an event that can never be applied` | `consumer`, `topic` or `type`/`event_id`, `error` |
| Consumers | WARN | `dead-lettering an event that can never be applied` | `consumer`, `type`, `event_id`, `error` |
| Consumers | WARN | `<consumer> broker connection stalled or lost; fetching again after the backoff` | `error`, `attempt`, `backoff` |
| Consumers | ERROR | `<consumer> handling failed; retrying the same message`, `<consumer> handling failed after the maximum attempts; dead-lettering the message` | `error`, `partition`, `offset`, attempt fields |
| Consumers | ERROR | `consumer stopped` (the worker ends; the process then exits) | `consumer`, `error` |
| Analytics consumer | WARN / ERROR | `analytics: skipping a message that is not a projected CloudEvent ...`, `analytics: dead-lettering a message that can never be projected` | `error`, `topic`/`dlq_topic`, `partition`, `offset` |
| Relay | INFO | `outbox relay running` | `publisher`, `interval`, `max_attempts`, `topic` |
| Relay | ERROR | `outbox relay pass failed` | `error` |
| Relay | ERROR | `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `max_attempts`, `error` |
| Relay | ERROR | `outbox relay stopped`, `kafka relay sink close failed` | `error` |
| Retention sweeper | INFO / ERROR | `loop running`, `loop pass done` / `loop pass failed` | `loop` (`retention sweeper`), `interval`, `count` / `error` |
| Readiness | WARN | `readiness: dependency is not ready` (once per check round, cached 3 s) | `dependency` (`postgres` or `kafka`), `error` |
| Shutdown | INFO / WARN | `shutdown: readiness flipped to not-ready; waiting for traffic to drain` / `ignoring invalid SHUTDOWN_DRAIN_DELAY` / `worker did not stop before the shutdown drain deadline` / `telemetry flush failed on shutdown` | `drain_delay` / `value`, `default` / `worker` / `error` |

REST errors are `application/problem+json` documents whose `type` is the bare problem slug; the slug and status table is
in [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).

## Probes

`GET /healthz` (liveness, always 200) on every HTTP binary. `GET /readyz` on `cmd/api` is 200 only while the drain gate
is open, Postgres answers a ping and a Kafka broker answers a metadata request; 503 `not_ready` while draining, 503
`dependency-unavailable` naming the failing dependency otherwise. `cmd/projector` (admin port) and `cmd/reports` also
serve `/readyz`; `cmd/mcp` and `cmd/a2a` serve `/healthz` only. Details in the [Runbook](/docs/operations/runbook) and
[Runtime](/docs/overview/runtime).

## Dashboards and alerts that exist

Read from `warehouse-infra` on `origin/develop`:

- **Grafana dashboard** `terraform/dashboards/contexts/customer-returns.json`: Kong request rate, 5xx ratio and p95
  latency for the context's routes; service RED panels from `http_server_request_duration_seconds_*` (request rate by
  route, p95 by process, 5xx per Kong route for API, MCP and reports); the business panel
  `sum by (outcome) (rate(customer_returns_return_authorized_total[5m]))`; Go goroutines and memory; and Loki panels
  (live log stream, log volume by level, errors and warnings).
- **Alert rules** (`terraform/alerting/warehouse-alert-rules.json`), groups `warehouse-customer-returns`
  (`WarehouseKong5xxRatioHigh`, `WarehousePodRestarting`) and `warehouse-customer-returns-slo`
  (`WarehouseApiAvailabilityBurnFast`, `WarehouseApiLatencyBurnFast`, `WarehouseApiAvailabilityBurnSlow`,
  `WarehouseApiLatencyBurnSlow`), all from Kong metrics and kube-state.
- **Not covered by any alert or panel:** outbox backlog, consumer lag, dead letters. There is no outbox lag gauge in
  this service.
- Logs reach Loki through Alloy, which maps the pod label `app.kubernetes.io/name` to the Loki label `app`.

## Suggested checks

Not shipped as rules; they are the reads that replace the missing instruments.

| Signal | Read | Worrying when |
| --- | --- | --- |
| Outbox backlog | `SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL` | oldest row older than a few minutes |
| Rows parked at the cap | `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20` (use your `OUTBOX_MAX_ATTEMPTS`) | any row; also the ERROR line `row reached the attempt cap` |
| Consumer lag | Kafka consumer-group lag of the four `cmd/api` groups and `customer-returns-analytics` | growing, or the same `partition`/`offset` repeating in `handling failed; retrying` |
| Dead letters | depth of `retailnetwork.customer-ordering.events.dlq`, `retailnetwork.node-topology.events.dlq`, `retailnetwork.order-sourcing.events.dlq`, `retailnetwork.vendor-connectivity.events.dlq`, `retailnetwork.customer-returns.analytics.dlq` | any message; headers `x-dlq-error`, `x-dlq-source-topic`, `x-dlq-source-offset` say why |
| Rejected authorizations | `customer_returns_return_authorized_total{outcome="rejected"}` by `reason` | a rising `group-not-shipped` or `no-return-destination` |
| Analytics freshness | `GET /reports/freshness` `lagSeconds` | growing while `ReturnAuthorized` events are being published |
| Boot loop | `... exited with error` and the `WarehousePodRestarting` alert | any |
| Table growth | row counts of `outbox_events`, `processed_events`, `return_outcomes` (nothing deletes them) and `idempotency_keys` (purged after 24 h) | capacity planning |
