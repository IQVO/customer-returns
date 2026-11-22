---
id: runtime
title: Runtime
sidebar_label: Runtime
sidebar_position: 2
description: "Boot and stop sequence, probes, the outbox and consumers in operation, and the Helm chart of customer-returns."
---

# Runtime: configuration, probes, shutdown and the outbox

The authoritative per-binary variable list is [Configuration](/docs/operations/configuration); the table below is the
`cmd/api` summary kept next to the boot sequence.

`cmd/api` is the main binary. It runs on real PostgreSQL and real Kafka; there is no
alternative runtime and no log sink. `cmd/mcp` (ADR 0006) is the second, optional
binary: the read-only MCP server, which needs `DATABASE_URL` only (it never dials
Kafka and can never write), serves Streamable HTTP on `MCP_ADDR` (default `:8090`)
plus an open `GET /healthz`, and is deployed by the same chart behind
`mcp.enabled` (default off). `cmd/a2a` (ADR 0009) is the third, optional binary:
the read-only A2A agent, `DATABASE_URL` only (no Kafka, can never write), JSON-RPC on
`A2A_ADDR` (default `:8093`) at `/` and `/a2a`, the Agent Card at
`/.well-known/agent-card.json` and an open `GET /healthz`. `A2A_AGENT_URL` (an
absolute http(s) URL the card advertises) is mandatory; the chart deploys it behind
`a2a.enabled` (default off). `cmd/projector` and `cmd/reports` ([ADR 0012](/docs/adr/0012-analytics-read-side-and-return-rate-facts))
are the fourth and fifth, both optional (chart `analytics.enabled`, default off): the projector folds this context's own
events topic into a separate analytical database (admin `/healthz` and `/readyz` on `ADMIN_ADDR`, default `:8091`), and
reports serves `GET /reports/*` read-only over it (`HTTP_ADDR`, default `:8092`). Neither opens the OLTP database.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN of the service's pool. A boot error names it when unset. |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations (golang-migrate holds a session advisory lock, which breaks behind a transaction-pooling proxy). |
| `KAFKA_BROKERS` | required | Comma-separated `host:port` list (relay, consumers and `/readyz`). A boot error names it when unset or blank. |
| `ORDER_CONSUMER_GROUP`, `TOPOLOGY_CONSUMER_GROUP`, `SOURCING_CONSUMER_GROUP`, `VENDOR_CONSUMER_GROUP` | required | Stable consumer group ids of the customer-ordering, node-topology, order-sourcing and vendor-connectivity consumers. A boot error names the missing one. |
| `SHIPPING_LABELS_URL` | empty | Base URL of the shipping context's reverse-label route ([ADR 0005](/docs/adr/0005-the-reverse-label-route)); empty keeps the label client off (`422 label-unavailable`). |
| `EVENT_PUBLISHER` | `kafka` | Only `kafka` is accepted; any other value is a boot error. |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Poll interval of the relay (Go duration or seconds). |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row; `0` = unlimited. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait between flipping `/readyz` to 503 and closing the listener; `0` disables. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `OTEL_SERVICE_NAME` | `customer-returns` | `service.name` of spans and metrics (ADR 0008). |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC, `host:port` or URL; export never blocks. |
| `SERVICE_VERSION` / `ENVIRONMENT` | `dev` / `local` | `service.version` / `deployment.environment.name`. |

Configuration errors are raised before anything is opened or bound.

## Boot and stop

1. Validate the environment. 2. Run the embedded migrations and ping Postgres, both with
backoff. 3. Start the outbox relay, the four consumers and the daily retention sweeper. 4. Serve.

On SIGINT/SIGTERM: `/readyz` flips to 503 `not_ready`, wait `SHUTDOWN_DRAIN_DELAY`, drain
HTTP (10 s), stop and await the consumers and the loop (10 s), then the relay (10 s), close the
pool. If a consumer, a loop or the relay returns on its own, the process exits non-zero so
the orchestrator restarts it.

## Probes

- `GET /healthz` is liveness: 200 `{"status":"ok"}` while the process serves, even while
  draining or while a dependency is down.
- `GET /readyz` is 200 only if the drain gate is open, Postgres answers a ping and a Kafka
  broker answers a metadata request. The request names no topics (an empty, non-nil list), so
  it never creates a topic; a fresh connection is dialled on every check. While draining it
  answers 503 `not_ready`; while a dependency is down, 503 `application/problem+json` of type
  `dependency-unavailable` naming it. Verdicts are cached 3 s.

## The outbox and the consumers in operation

- Events are written in the same transaction as the aggregates and published later, so a write
  is accepted while Kafka is down; `outbox_events.published_at IS NULL` rows wait and are
  published, in id order, when the broker is back. Delivery is at-least-once with a stable
  CloudEvents `id`.
- A row that failed `OUTBOX_MAX_ATTEMPTS` times is skipped (logged once at ERROR with id,
  topic, type and key) and stays in the table with `attempts` and `last_error`. Fix the cause,
  then re-arm it: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`.
- Consumers claim each CloudEvents id in `processed_events` in the same transaction as their
  writes. A transient failure is retried, then published to `<topic>.dlq`; a payload that can
  never decode goes there at once. A `VendorReturnOutcomeSubmitted` for an unknown or already-closed authorization is
  recorded and acknowledged, never dead-lettered (unexpected lines stay facts, ADR 0002).
- Idempotency keys older than 24 h are purged by the daily retention sweep (an expired key is also
  replaced on its next use).

## Deployment (Helm chart)

`charts/customer-returns/` runs `cmd/api` as one Deployment (`component=api`); the relay, the consumers and the loop live in that process. `config.*` values map 1:1 to the variables above and are rendered both into the chart ConfigMap (`envFrom`) and into the container env from one helper; `tests/render_test.sh` fails when `cmd/api` reads a variable the chart does not render, or the chart renders one the binary does not read. The optional `mcp` component (`component=mcp`, `mcp.enabled`, default off) runs `cmd/mcp` with its OWN env — exactly `MCP_ADDR`, `LOG_LEVEL`, `SERVICE_VERSION`, `ENVIRONMENT`, `OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `DATABASE_URL` and `MIGRATIONS_DATABASE_URL` — and never mounts the api ConfigMap, so no Kafka, relay or consumer variable can reach it; `tests/render_test.sh` checks that contract against `cmd/mcp/main.go` and `telemetry.go` the same way. The observability variables (`OTEL_*`, `SERVICE_VERSION`, `ENVIRONMENT`) come from `otel.endpoint`, `otel.serviceName` (`<name>-mcp` for mcp), `image.tag` or appVersion and `environment`, go straight into the container env (not the ConfigMap) and are refused in `extraEnv`.

| Value | Variable | Rule |
| --- | --- | --- |
| `database.url` or `database.existingSecret` (+ `existingSecretKey`) | `DATABASE_URL` | `secretKeyRef`, never in the ConfigMap; render fails when neither is set. |
| `database.migrationsExistingSecretKey` | `MIGRATIONS_DATABASE_URL` | `secretKeyRef` with `optional: true`; the binary falls back to `DATABASE_URL`. |
| `kafka.brokers` | `KAFKA_BROKERS` | Required. |
| `config.orderConsumerGroup`, `config.topologyConsumerGroup`, `config.sourcingConsumerGroup`, `config.vendorConsumerGroup` | `ORDER_`/`TOPOLOGY_`/`SOURCING_`/`VENDOR_CONSUMER_GROUP` | Required, stable and service-prefixed, never the pod name. |
| `config.shippingLabelsUrl` | `SHIPPING_LABELS_URL` | Optional; empty keeps the reverse-label client off. |
| `config.eventPublisher` | `EVENT_PUBLISHER` | Only `kafka`; `*_MODE` and a non-kafka publisher in `extraEnv` are refused. |
| `config.outboxRelayInterval`, `shutdownDrainDelay` | the relay and drain variables | Optional; not rendered while empty so the binary default applies. `shutdownDrainDelay` takes whole seconds. |
| `config.outboxMaxAttempts` | `OUTBOX_MAX_ATTEMPTS` | Default 20, `0` = unlimited. |
| `terminationGracePeriodSeconds` | n/a | Default 40; refused under 40 and under drain delay + 30 s (drain delay 5 s + HTTP 10 s + consumers and the loop 10 s + relay 10 s = 35 s). |

The pod runs as uid 1000 with a read-only root filesystem, no privilege escalation, all capabilities dropped and the `RuntimeDefault` seccomp profile; liveness is `/healthz`, readiness `/readyz`, plus a startup probe covering the boot retry. The `release` job packages `customer-returns-<version>.tgz` and pushes it to `oci://ghcr.io/iqvo`.
