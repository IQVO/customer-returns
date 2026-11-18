---
id: configuration
title: Configuration
sidebar_label: Configuration
sidebar_position: 1
description: "Every environment variable read by cmd/api, cmd/mcp and cmd/a2a: default, required or not, and meaning."
---

# Configuration

`customer-returns` ships **three binaries** (`cmd/api`, `cmd/mcp`, `cmd/a2a`) and each is configured only through
environment variables. There is no config file, no flag and no secret-manager integration in the repository. REST,
MCP and A2A are unauthenticated fleet-wide, so there is no token or key to configure; access control is the
in-cluster ClusterIP boundary.

Every binary runs on real Postgres. `cmd/api` also needs real Kafka. A missing mandatory variable is a boot error that
names it, raised before anything is opened or bound (`cmd/api/config.go` `loadRuntime`, `cmd/a2a/main.go`
`validateBootConfig`). Test-only variables are not listed.

A variable read by several binaries has a row in each table with that binary's own default.

## `cmd/api`

Composition root of the REST API, the three Kafka consumers, the outbox relay and the retention sweeper
(`cmd/api/main.go`, `config.go`, `wire_adapters.go`, `wire_background.go`, `telemetry.go`).

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server (also serves `/healthz` and `/readyz`). |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service pool. Missing = boot error `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled) DSN used only for the boot migrations: golang-migrate holds a session advisory lock that does not survive a transaction-pooling proxy. An empty value falls back to `DATABASE_URL`. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list (entries trimmed, blanks dropped). Used by the relay sink, the three consumers and the `/readyz` metadata check. Missing = boot error `KAFKA_BROKERS is required`. |
| `EVENT_PUBLISHER` | `kafka` | no | Only `kafka` (or empty) is accepted. `log` is a boot error (`EVENT_PUBLISHER=log is not supported`), any other value is a boot error. The variable stays because deployment manifests set it. |
| `NODE_CONSUMER_GROUP` | none | **yes** | Stable consumer group id of the node-topology consumer. Missing = boot error naming the variable. |
| `CATALOG_CONSUMER_GROUP` | none | **yes** | Stable consumer group id of the offer-availability (packaging) consumer. |
| `RECEIPT_CONSUMER_GROUP` | none | **yes** | Stable consumer group id of the vendor-connectivity (receipts) consumer. |
| `SSCC_COMPANY_PREFIX` | none | **yes** | The network's GS1 company prefix, 6 to 11 digits. Missing or invalid = boot error. SSCCs issued by the network are built from it. |
| `SSCC_EXTENSION_DIGIT` | `0` | no | Extension digit of issued SSCCs, one digit 0..9. A non-numeric value or one outside 0..9 is a boot error. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Pause between relay passes that did not fill a batch. A Go duration (`500ms`, `2s`) or plain seconds (`2`, `0.5`). Unparsable or non-positive values log a WARN and use the default. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures after which the relay stops claiming an outbox row. `0` = unlimited. Negative or unparsable values log a WARN and use `20`, so a typo never removes the cap. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | After SIGINT/SIGTERM, `/readyz` flips to 503 and the process waits this long before closing the listener. `0` disables it. Negative or unparsable values log a WARN and use `5s`. Read when the signal arrives. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive. Any other value falls back to `info`. Logs are JSON on stdout. |
| `OTEL_SERVICE_NAME` | `customer-returns` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint, `host:port` or URL. A bare `host:port` is turned into `http://host:port`. Export never blocks boot. |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute (read by `internal/adapters/outbound/telemetry`). |

## `cmd/mcp`

Read-only MCP server over Streamable HTTP (`cmd/mcp/main.go`, `cmd/mcp/telemetry.go`). It reads **no** Kafka, outbox
or consumer-group variable.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address of the MCP endpoint. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the database `cmd/api` owns. Missing = boot error `DATABASE_URL is required: this context runs on real Postgres`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the idempotent embedded migrations this binary also runs on start. |
| `LOG_LEVEL` | `info` | no | As in `cmd/api`. |
| `OTEL_SERVICE_NAME` | `customer-returns-mcp` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. |
| `SERVICE_VERSION` | `dev` | no | `service.version`; also logged on the `mcp server starting` line. |
| `ENVIRONMENT` | `local` in telemetry, `dev` on the boot log line | no | `deployment.environment.name`. The boot line logs `dev` when unset while the resource attribute says `local`: a code inconsistency, not two settings. |

## `cmd/a2a`

Read-only A2A agent, JSON-RPC 2.0 over HTTP (`cmd/a2a/main.go`, `cmd/a2a/telemetry.go`). No Kafka, no
`EVENT_PUBLISHER`, no consumer groups.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address. |
| `A2A_AGENT_URL` | none | **yes** | Absolute `http(s)` URL advertised on the Agent Card, for example `http://customer-returns-a2a.warehouse-systems.svc.cluster.local:8093/`. A missing, relative or non-http(s) value is a boot error naming the variable, raised before anything is dialled. |
| `DATABASE_URL` | none | **yes** | Postgres DSN. Missing = boot error. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. |
| `LOG_LEVEL` | `info` | no | As in `cmd/api`. |
| `OTEL_SERVICE_NAME` | `customer-returns-a2a` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. |
| `SERVICE_VERSION` | `dev` | no | `service.version`; also logged on the `a2a server starting` line. |
| `ENVIRONMENT` | `local` in telemetry, `dev` on the boot log line | no | `deployment.environment.name`. |

## `cmd/projector` (analytics projector, ADR 0012)

Optional (chart `analytics.enabled`, default off). Writes ONLY the separate analytical database; never opens the OLTP
database. Reads `retailnetwork.customer-returns.events` (its own topic), dead-letters poison to
`retailnetwork.customer-returns.analytics.dlq`. Serves `/healthz` and `/readyz` on the admin port.

| Variable | Default | Required? | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `ANALYTICS_DATABASE_URL` | none | **yes** | Direct DSN of the analytical database (read-write; runs the embedded analytical migrations). | `analytics.database.projectorUrl` or `analytics.database.existingSecret` |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated brokers. | `kafka.brokers` |
| `ANALYTICS_CONSUMER_GROUP` | `customer-returns-analytics` | no | The projector's fixed consumer group. | `analytics.projector.consumerGroup` |
| `ADMIN_ADDR` | `:8091` | no | `/healthz` and `/readyz` listen address. | `analytics.projector.adminAddr` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning`, `error`. | `analytics.logLevel` |
| `OTEL_SERVICE_NAME` | `customer-returns-projector` | no | `service.name`. | `otel.serviceName` + `-projector` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. | `otel.endpoint` |
| `SERVICE_VERSION`, `ENVIRONMENT` | `dev`, `local` | no | `service.version`, `deployment.environment.name`. | image tag, `environment` |

## `cmd/reports` (read-only analytics reports, ADR 0012)

Optional (chart `analytics.enabled`, default off). `GET /reports/returns-performance`, `/reports/freshness`,
`/healthz`, `/readyz` over a read-only pool (15 s statement timeout). No Kafka, no OLTP database, no auth.

| Variable | Default | Required? | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `ANALYTICS_READER_DATABASE_URL` | value of `ANALYTICS_DATABASE_URL` | **yes** (one of the two) | DSN of a read-only role on the analytical database. | `analytics.database.reportsUrl` (falls back to `projectorUrl`) |
| `ANALYTICS_DATABASE_URL` | none | no | Single-role local fallback; the chart never renders it for the reports pod. | not rendered |
| `HTTP_ADDR` | `:8092` | no | Listen address. | `analytics.reports.httpAddr` |
| `LOG_LEVEL` | `info` | no | As in `cmd/projector`. | `analytics.logLevel` |
| `OTEL_SERVICE_NAME` | `customer-returns-reports` | no | `service.name`. | `otel.serviceName` + `-reports` |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT` | as above | no | Telemetry labels. | as above |

## Compiled-in settings (not configurable)

| Setting | Value | Source |
| --- | --- | --- |
| HTTP `ReadHeaderTimeout` | 5 s (all three binaries) | `cmd/*/main.go` |
| Graceful shutdown | HTTP drain 10 s (`shutdownTimeout`), each worker drain 10 s (`workerDrainTimeout`, api only) | `cmd/api/main.go` |
| Postgres pool | `MaxConns` 10, `statement_timeout` set per connection | `internal/adapters/outbound/postgres/pool.go` |
| Boot retry (migrations, first ping) | backoff retry, then the process refuses to boot | `internal/bootretry` |
| Outbox relay batch size | 100 rows per pass | `internal/adapters/outbound/outbox/relay.go` (`DefaultBatchSize`) |
| `/readyz` dependency cache and timeout | 3 s verdict cache, 2 s per round | `internal/adapters/inbound/http/readiness.go` |
| Idempotency-Key retention | 24 h; purged by a 24 h sweep | `internal/adapters/inbound/http/idempotency.go`, `cmd/api/main.go` (`retentionSweep`) |
| Metric push interval | 15 s | `internal/adapters/outbound/telemetry/telemetry.go` |
| Topics produced | `retailnetwork.customer-returns.events`, `retailnetwork.customer-returns.analytics` | `internal/adapters/outbound/kafka/encoder.go` |

## Readiness

`GET /readyz` on `cmd/api` is 200 only when the drain gate is open **and** Postgres answers a ping **and** a Kafka
broker answers a topic-less metadata request. It never creates topics. See the
[Runbook](/docs/operations/runbook) for what a 503 means and [Runtime](/docs/overview/runtime) for the boot and stop
sequence.

## What is deliberately absent

- **No auth variables.** REST, MCP and A2A are unauthenticated fleet-wide.
- **No Kafka variable on `cmd/mcp` and `cmd/a2a`.** They never dial Kafka and can never write.
- **No scorecard threshold variables.** Tier thresholds are constants in `internal/domain/scorecard/scorecard.go`
  ([ADR 0005](/docs/adr/0005-the-reverse-label-route)).
- **Chart values.** `charts/customer-returns` maps these variables onto the pods (`database.*`, `kafka.brokers`,
  `config.*`, `mcp.*`, `a2a.*`); see [Runtime](/docs/overview/runtime#deployment-helm-chart).
