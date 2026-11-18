# customer-returns

[![CI](https://github.com/IQVO/customer-returns/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/IQVO/customer-returns/actions/workflows/ci.yml?query=branch%3Amain) [![coverage gate](https://img.shields.io/badge/coverage%20gate-90%25-brightgreen)](https://github.com/IQVO/customer-returns/blob/main/Makefile) [![Go](https://img.shields.io/badge/go-1.27.3-00ADD8?logo=go&logoColor=white)](https://github.com/IQVO/customer-returns/blob/main/go.mod) [![docs](https://img.shields.io/badge/docs-iqvo.github.io-blue)](https://iqvo.github.io/customer-returns/) [![license](https://img.shields.io/badge/license-MIT-blue)](https://github.com/IQVO/customer-returns/blob/main/LICENSE)

Retail network, bounded context **Customer Returns** (Supporting, commerce tier).

The shopper-facing returns front door of the network: after an order group has shipped or been delivered, the storefront asks this context to authorize its return. The context decides the disposition itself — `KEEP` (returnless, under the threshold and with every price known), `NEAREST_RETURNS_CENTER` (the region's ACTIVE returns centre) or `ORIGIN_NODE` (the node that fulfilled the group) — asks the shipping context for the shopper's reverse label, and closes the loop when the returns centre's graded outcome arrives. It holds no money, no address and no free text.

It owns the `ReturnAuthorization` aggregate and its two event-fed read models (OrderGroupView, ReturnsNetworkView). It does **not** own the order or its ship-to (customer-ordering's, consumed as facts), the nodes or the service areas (node-topology's, consumed), the fulfilling node or the per-item owners (order-sourcing's, consumed) or the returns-centre receipt (vendor-connectivity hands it over as `VendorReturnOutcomeSubmitted`). It is a downstream conformist of those four Open Host Services, with exactly ONE in-org synchronous call: the shipping context's reverse-label route.

Status: implemented (the packaging checklist is `docs/docs/operations/before-first-release.md`). The design is the wave-4 network design; `network/model/model.yaml` in IQVO/warehouse-docs is the single source of truth. This repository is a service of the retail network, not of the warehouse-systems fleet.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message (`com.retailnetwork.commerce.customer-returns.<entity>.<Event>`), transactional outbox.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

Full documentation is published at **<https://iqvo.github.io/customer-returns/>** (built from `docs/`; build locally with `cd docs && npm ci && npm run build`).

Page map (source under `docs/docs/`):

| Area | Pages |
|---|---|
| Overview | [Introduction](docs/docs/overview/introduction.md), [Architecture](docs/docs/overview/architecture.md), [Runtime](docs/docs/overview/runtime.md), [Quickstart](docs/docs/overview/quickstart.md) |
| Operations | [Configuration](docs/docs/operations/configuration.md), [Runbook](docs/docs/operations/runbook.md), [Observability](docs/docs/operations/observability.md), [Troubleshooting](docs/docs/operations/troubleshooting.md), [Before first release](docs/docs/operations/before-first-release.md) |
| Development | [Testing](docs/docs/development/testing.md) |
| Ecosystem | [Integration](docs/docs/ecosystem/integration.md), [MCP tools](docs/docs/mcp/tools.md) |
| API reference | [Overview](docs/docs/api-reference/overview.md) (REST pages generated from `apis/openapi.yaml`), [Events](docs/docs/api-reference/events.md) |
| Decisions | [ADR index](docs/adr/index.md), decisions 0001 to 0011 |

The design source is the retail-network pages on warehouse-docs:

| Area | Pages |
|---|---|
| Retail network | [Network overview](https://iqvo.github.io/warehouse-docs/network) |
| This context | [Index / service facts](https://iqvo.github.io/warehouse-docs/network/contexts/customer-returns) |
| Decisions | [Network ADRs](https://iqvo.github.io/warehouse-docs/network/adr) |

In-tree decisions: [docs/adr/](docs/adr/index.md).

## Architecture

Hexagonal / ports & adapters. Dependencies point inward only:

```
cmd/api/                     composition root: the returns REST API + outbox relay +
                             the four Kafka consumers, on real Postgres and real Kafka
                             (no in-memory mode, no log sink, no consumer off)
cmd/mcp/                     read-only MCP server (Streamable HTTP on /mcp)
cmd/a2a/                     read-only A2A agent (JSON-RPC over HTTP, Agent Card at
                             /.well-known/agent-card.json)
internal/
  domain/                    returnauthorization (the aggregate + decide), network
                             (the read-model value types), shared — pure business logic
  application/
    ports/                   OUT interfaces only (repos, LabelIssuer, Clock)
    usecases/                one method per command, query and consumer policy
    requests/ outbox/        request handling; outbox orchestration
    views/                   the read models
  adapters/
    inbound/http/            chi handlers, RFC 9457 problem mapping, idempotency middleware
    inbound/kafka/           customer-ordering, node-topology, order-sourcing,
                             vendor-connectivity consumers
    inbound/mcp/             the two read-only MCP tools
    inbound/a2a/             the one read-only A2A skill
    inbound/requestlog/      the request log line
    outbound/postgres/       pgxpool repos, embedded golang-migrate, UnitOfWork
    outbound/outbox/         transactional outbox writer + relay
    outbound/kafka/          the topic publisher behind the relay
    outbound/labels/         the ONE in-org HTTP client (the shipping context's
                             reverse-label route, config-gated, ADR 0005)
    outbound/telemetry/      OTel wiring + the business counter
    outbound/clock/ ids/     wall clock, id source
    outbound/memory/         test doubles only, never linked into cmd/** (fitness-tested)
  architecture/              the fitness tests below
  bootretry/ testing/        boot retry, shared test helpers
apis/                        openapi.yaml (REST) + asyncapi.yaml (events)
docs/adr/                    10 ADRs + index
features/                    godog feature files
charts/customer-returns/     the Helm chart
```

The layering is an executable test, not a convention: `internal/architecture/architecture_test.go` (`TestHexagonalArchitecture`) fails the build if the domain ever imports an outer layer or an adapter reaches sideways into a sibling. The same package pins CloudEvents-only envelopes and the event catalogue (`catalogue_fitness_test.go`, `events_fitness_test.go`), the Kafka DLQ/acks/topic rules (`kafka_*_fitness_test.go`), the read-only MCP/A2A surfaces (`mcp_optional_inputs_test.go`, `a2a_fitness_test.go`) and the PII rule (`pii_fitness_test.go`).

## Domain events

All facts leave as CloudEvents 1.0 (structured content mode, `application/cloudevents+json`); the envelope and type catalogue are [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md). The type convention is `com.retailnetwork.commerce.customer-returns.<entity>.<Event>`, for example:

```
com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnAuthorized
com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnCompleted
```

Specified in `apis/asyncapi.yaml` — four published and ten consumed events:

| Topic | Direction | Messages |
|---|---|---|
| `retailnetwork.customer-returns.events` | publishes (via the transactional outbox) | `ReturnAuthorized`, `ReturnLabelIssued`, `ReturnCompleted`, `ReturnCancelled` |
| `retailnetwork.customer-ordering.events` | consumes | `CustomerOrderPlaced`, `ShopperNoticeDecided` (the terminal families) |
| `retailnetwork.node-topology.events` | consumes | `NodeRegistered`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `ServiceAreaAssigned` |
| `retailnetwork.order-sourcing.events` | consumes | `FulfillmentReleasedToNode`, `FulfillmentLocked` |
| `retailnetwork.vendor-connectivity.events` | consumes | `VendorReturnOutcomeSubmitted` (the graded outcome that completes a return) |

No published event carries a unit price, an address, a person's name, a phone number or free text; `reason` is an enum.

## Running it

`cmd/api` needs Postgres and Kafka; there is no in-memory mode (`EVENT_PUBLISHER=log` is a boot error). A missing mandatory variable is a boot error naming it. `GET /readyz` is 200 only while Postgres answers a ping AND a Kafka broker answers a metadata request; the process exits non-zero when the relay, a consumer or a loop returns on its own.

```sh
export DATABASE_URL="postgres://user:pass@localhost:5432/customer_returns?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export ORDER_CONSUMER_GROUP=customer-returns-order
export TOPOLOGY_CONSUMER_GROUP=customer-returns-topology
export SOURCING_CONSUMER_GROUP=customer-returns-sourcing
export VENDOR_CONSUMER_GROUP=customer-returns-vendor
export SHIPPING_LABELS_URL=""              # optional: the shipping context's reverse-label route
go run ./cmd/api                           # migrations run at boot; REST on :8080
```

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Listen address |
| `DATABASE_URL` | **required** | Postgres DSN |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers (relay, consumers, `/readyz`) |
| `EVENT_PUBLISHER` | `kafka` | Only `kafka` is accepted; `log` is a boot error |
| `ORDER_CONSUMER_GROUP` | **required** | Stable group id of the customer-ordering consumer |
| `TOPOLOGY_CONSUMER_GROUP` | **required** | Stable group id of the node-topology consumer |
| `SOURCING_CONSUMER_GROUP` | **required** | Stable group id of the order-sourcing consumer |
| `VENDOR_CONSUMER_GROUP` | **required** | Stable group id of the vendor-connectivity consumer |
| `SHIPPING_LABELS_URL` | empty | Base URL of the shipping context's reverse-label route; empty keeps the label client disabled (422 `label-unavailable`, ADR 0005) |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OTEL_SERVICE_NAME` | `customer-returns` | `service.name` of spans and metrics |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | telemetry default | Collector OTLP/gRPC endpoint; export never blocks |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

`cmd/mcp` (read-only: Postgres only, no Kafka, no writes): `MCP_ADDR` (default `:8090`), `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL` (default `DATABASE_URL`), `LOG_LEVEL` (`info`), `OTEL_SERVICE_NAME` (`customer-returns-mcp`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION` (`dev`), `ENVIRONMENT`.

`cmd/a2a` (read-only, same rules as `cmd/mcp`): `A2A_ADDR` (default `:8093`), `A2A_AGENT_URL` (**required**, absolute http(s) URL advertised on the Agent Card), `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL`, `LOG_LEVEL`, `OTEL_SERVICE_NAME` (`customer-returns-a2a`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`.

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0008](docs/adr/0008-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id` and a `request_id`; `cmd/api`, `cmd/mcp` and `cmd/a2a` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`; export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter `customer_returns.return.authorized` (`outcome` = `authorized` with `disposition`, or `rejected` with the problem `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by the consumers. The chart's `otel.*` values render the env.

## API

REST contract: [`apis/openapi.yaml`](apis/openapi.yaml). Every `POST` takes `Idempotency-Key` (1 to 128 printable ASCII characters; missing is `400 idempotency-key-required`, malformed `400 idempotency-key-invalid`); a repeat replays the first response with `Idempotency-Replayed: true`, the same key on a different method, path or body is `409 idempotency-key-reused` ([ADR 0010](docs/adr/0010-fleet-rest-api-guideline.md)). Errors are RFC 9457 `application/problem+json`; the shared problem types are `not-found` 404, `illegal-transition` 409, `concurrent-update` 409 and `validation-failed` 422, plus `group-not-shipped`, `no-return-destination`, `label-unavailable` and `authorization-expired`. No authentication.

What it does, route by route:

- **Authorize**: `POST /return-authorizations` — body `{orderId, groupNo, lines[]{lineNo, unitPriceCents?}, reason, method?, pickupWindowStart?, pickupWindowEnd?}`. The group must be SHIPPED or DELIVERED (`group-not-shipped`, the view may lag — retry with the same key); the disposition is decided here (`KEEP`, `NEAREST_RETURNS_CENTER` or `ORIGIN_NODE`; `no-return-destination` when nothing fits). Prices are the storefront's facts: stored, echoed, never published; an absent price makes KEEP impossible and is returned as `pricesAbsent: true`.
- **Read**: `GET /return-authorizations/{authorizationId}` (an expired authorization is lazily cancelled by the read) and `GET /return-authorizations?orderId=&cursor=&limit=` (keyset page, newest first).
- **Cancel**: `POST /return-authorizations/{authorizationId}/cancellations` — the shopper's cancellation while AUTHORIZED.
- **Label**: `POST /return-authorizations/{authorizationId}/labels` — body `{packageNo}` (1..5); asks the shipping context's `POST /return-labels` route (the one in-org call, [ADR 0005](docs/adr/0005-the-reverse-label-route.md)), links the tracking number and publishes `ReturnLabelIssued`; the call names the return's destination by the destination node's label prefix (from node-topology's `NodeRegistered`), never by the order's own prefix; `422 label-unavailable` while the route is not configured or the destination node's prefix is not known here — tolerant, never a 500.
- `GET /return-authorizations/{id}/events` is deliberately NOT built: the audit trail IS the event stream.

`GET /healthz` (liveness) and `GET /readyz` (Postgres + Kafka gate) serve the probes.

## Helm chart

Deploy with `charts/customer-returns`. Values that matter: `config.*` (the env above, incl. the four consumer groups and `shippingLabelsUrl`), `database.url` (or `existingSecret`), `kafka.brokers`, `otel.enabled` (on by default; `false` renders no `OTEL_*` env at all) and `environment`. `mcp.enabled=true` (default off) adds the MCP Deployment + ClusterIP Service; `a2a.enabled=true` (default off) adds the A2A agent's. Linted and rendered in CI by the `helm-lint` (`ct lint`) and `chart-tests` jobs.

### Running the MCP server in Kubernetes

`cmd/mcp` ([ADR 0006](docs/adr/0006-read-only-mcp-server.md)) is the optional second binary: the read-only MCP server (`get_return_authorization`, `list_return_authorizations`) over the same reads as the REST GETs, Postgres only, Streamable HTTP on `/mcp` (`MCP_ADDR`, default `:8090`), `GET /healthz` for the probes, unauthenticated like REST (access control is the in-cluster ClusterIP boundary), `mcp.enabled` in the chart (default off). Every tool declares authority `L1` (`_meta` `com.warehouse/authority`, Agentic Standard v2), the transport is stateless (2026-07-28 `server/discover` plus legacy `initialize`), and the surface carries one resource (`warehouse://customer-returns/return-authorizations/{authorizationId}`) and one prompt (`review_return_authorization`) — [ADR 0011](docs/adr/0011-agent-authority-levels-and-complete-mcp-surface.md).

### Running the A2A agent in Kubernetes

`cmd/a2a` ([ADR 0009](docs/adr/0009-a2a-inbound-adapter.md)) is the optional third binary: a read-only A2A agent (Agent2Agent, JSON-RPC 2.0, Agent Card at `/.well-known/agent-card.json`, port 8093) with one goal-shaped skill, `get_return_authorization`, over the same reads; it declares `authority:L1` on the card (Agentic Standard v2, [ADR 0011](docs/adr/0011-agent-authority-levels-and-complete-mcp-surface.md)); Postgres only, `A2A_AGENT_URL` is mandatory (absolute http(s) URL the card advertises), `a2a.enabled` in the chart (default off), unauthenticated like REST and MCP.

## Local development / quality gate

`make check` (format, vet, lint, unit, handler and godog scenarios), `make check-fast`, `make check-all`, `make coverage`, `make mutation`, `make mutation-full`, `make integration` (testcontainers Postgres and Kafka, needs Docker), `make arch-test`, `make vuln`, `make guide-lint`, `make harness-test`; `make help` lists everything. lefthook wires the same targets as hooks (`lefthook install` once per clone): pre-commit runs `make fmt-check`, `make vet`, `make lint`; pre-push runs `make check`. `integration` and `mutation` are deliberately excluded from the hooks so committing and pushing stay fast and offline-friendly.

## Tests

- **Unit**: `make test` — domain, use cases, handlers, adapters.
- **BDD**: `make bdd` — godog scenarios in `features/`.
- **Integration**: `make integration` — testcontainers Postgres + Kafka: the repositories, the outbox relay, the consumers, the idempotency poison path, the full e2e of `cmd/api` against real Postgres/Kafka and the real reverse-label stub, and the mcp/a2a binaries reading what the API wrote.
- **Mutation**: `make mutation` (blocking subset) / `make mutation-full` — gremlins, config in `.gremlins.yaml`.
- **Architecture fitness**: `make arch-test` — `internal/architecture/` (hexagonal rule, CloudEvents-only, catalogue, Kafka rules, read-only MCP/A2A, the PII sensor).

CI (`.github/workflows/ci.yml`) runs `lint`, `guide-lint`, `complexity`, `test`, `bdd`, `integration`, `mutation-fast`, `api-lint`, `vuln`, `helm-lint`, `chart-tests` and `arch-test` on every push and PR; the exhaustive `mutation` job and the advisory `drift` sensor run on schedule; `docker-publish` pushes the image and `release` tags and publishes the chart.

## Architecture Decision Records

[docs/adr/](docs/adr/index.md) — index plus 10 ADRs (0001-0006 and 0008-0011: bounded context, aggregates and invariants, consumed contracts and local read models, CloudEvents envelope and type catalogue, the reverse-label route, read-only MCP server, fleet observability standard, A2A inbound adapter, fleet REST API guideline, agent authority levels).

## License

[MIT](LICENSE).
