---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
description: "Run customer-returns locally against Postgres and Kafka and walk one return authorization through the API."
---

# Quickstart

This page gets `cmd/api` running on your machine and walks one return authorization through the API. The commands and
payloads come from the code and `apis/openapi.yaml`; **no run output is pasted here** because this page was written
from the source, not from a captured session.

## Prerequisites

- Go (version in `go.mod`), `make`, `curl`, `psql`.
- A reachable PostgreSQL. The service owns its own database and runs its embedded migrations at boot.
- A reachable Kafka broker. There is **one** broker platform-wide; from the kind cluster it is reachable at
  `localhost:9092`.
- Docker only for `make integration` (testcontainers boots its own Postgres and Kafka there).
- `golangci-lint` for `make lint`; `lefthook install` once per clone wires the git hooks.

There is no in-memory mode: `cmd/api` refuses to boot without `DATABASE_URL`, `KAFKA_BROKERS` and the four consumer
groups ([Configuration](/docs/operations/configuration)).

## Make targets

| Target | What it runs |
| --- | --- |
| `make build` | `go build ./...` |
| `make test` | `go test ./... -race` (unit, handler and godog scenarios, no database) |
| `make check` | `fmt-check vet build lint test` (the pre-push hook) |
| `make check-fast` | `fmt-check vet arch-test` plus the tests of the changed packages |
| `make check-all` | `check` plus `coverage arch-test bdd` |
| `make coverage` | tests with the 90% gate over `internal/domain` and `internal/application` |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | `go test ./internal/architecture/... -v` |
| `make integration` | `go test -tags=integration ./... -race -count=1` (Docker) |
| `make mutation` / `make mutation-full` | gremlins on `internal/domain/returnauthorization` / on `internal/domain` |
| `make vuln` | `govulncheck ./...` |

See [Testing](/docs/development/testing) for the full pyramid.

## Start the API

```sh
export DATABASE_URL="postgres://user:***@localhost:5432/customer_returns?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export ORDER_CONSUMER_GROUP=customer-returns-order
export TOPOLOGY_CONSUMER_GROUP=customer-returns-topology
export SOURCING_CONSUMER_GROUP=customer-returns-sourcing
export VENDOR_CONSUMER_GROUP=customer-returns-vendor
go run ./cmd/api                          # migrations run at boot; REST on :8080
```

Check the probes:

```sh
curl -s localhost:8080/healthz    # liveness
curl -s localhost:8080/readyz     # 200 only while Postgres and a Kafka broker answer
```

The read-only siblings need only the database:

```sh
DATABASE_URL="$DATABASE_URL" go run ./cmd/mcp    # MCP on :8090, path /mcp
DATABASE_URL="$DATABASE_URL" A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a   # A2A on :8093
```

## Authorize a return

An authorization needs the order group in `order_group_views` as SHIPPED or DELIVERED and a destination (an ACTIVE
returns centre in the group's region, or the origin node in `group_origins`), unless the return is a returnless `KEEP`.
In a real environment the consumers fill those tables from customer-ordering, node-topology and order-sourcing; without
them, `POST /return-authorizations` answers `422 group-not-shipped` (retry with the same key once the view caught up).

Every `POST` needs an `Idempotency-Key` header (1 to 128 printable ASCII characters). All calls below go to the service
directly (`localhost:8080`, routes at the root); through Kong the same calls go to
`http://localhost:8000/api/customer-returns/...` (the OpenAPI server URL).

1. Authorize (a cheap, fully priced drop-off line decides `KEEP` and closes at once):

   ```sh
   curl -s -X POST localhost:8080/return-authorizations -H 'Content-Type: application/json' -H 'Idempotency-Key: k-1' \
     -d '{"orderId":"ord-51a0c9","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":900}],"reason":"NOT_NEEDED","method":"DROP_OFF"}'
   ```

2. Read it and list the order's authorizations:

   ```sh
   curl -s localhost:8080/return-authorizations/<authorizationId>
   curl -s 'localhost:8080/return-authorizations?orderId=ord-51a0c9&limit=20'
   ```

3. For a physical return, ask for the reverse label (needs `SHIPPING_LABELS_URL`; `422 label-unavailable` otherwise), or
   cancel while still `AUTHORIZED`:

   ```sh
   curl -s -X POST localhost:8080/return-authorizations/<authorizationId>/labels -H 'Content-Type: application/json' \
     -H 'Idempotency-Key: k-2' -d '{"packageNo":1}'
   curl -s -X POST localhost:8080/return-authorizations/<authorizationId>/cancellations -H 'Idempotency-Key: k-3'
   ```

Each write appends its event to the outbox; the relay publishes it to `retailnetwork.customer-returns.events` within
`OUTBOX_RELAY_INTERVAL`. A physical return completes only when vendor-connectivity publishes
`VendorReturnOutcomeSubmitted` for it ([Integration](/docs/ecosystem/integration)).

## Where to go next

- [Architecture](/docs/overview/architecture) for the layout of `cmd/` and `internal/`.
- [Runbook](/docs/operations/runbook) and [Troubleshooting](/docs/operations/troubleshooting) when something does not boot.
- [API reference](/docs/api-reference) for every route and event.
