# customer-returns

harness-template: v4

Retail network, bounded context Customer Returns (Supporting, commerce tier). Owns the ReturnAuthorization aggregate (ret-<uuid>, disposition decided by the pure `decide`: KEEP returnless, NEAREST_RETURNS_CENTER or ORIGIN_NODE) and two event-fed read models (OrderGroupView from customer-ordering, ReturnsNetworkView from node-topology and order-sourcing, ADR 0003); publishes the four ReturnAuthorization events.
Design source of truth: IQVO/warehouse-docs `network/model/model.yaml` (context customer-returns) and the wave-4 network design. This service belongs to the retail network, not the warehouse-systems fleet.
This project is a domain-driven design study, not associated with any company: never name a real company, brand or product in code, comments, ADRs, specs, tests or fixtures (README `## Disclaimer`). No PII anywhere: no address, name, phone or free text may enter any event, table, log or tool answer; `reason` is an enum (`internal/architecture/pii_fitness_test.go`).

## Commands
`make check` (fmt, vet, build, lint, unit + handler + BDD; the pre-push gate), `make coverage` (scope + threshold live only in the Makefile), `make mutation` (gremlins per internal/domain/<pkg>; thresholds measured, never guessed), `make arch-test`, `make integration` (testcontainers Postgres + Kafka, needs Docker; build tag `integration`), `make vuln`. Lint: `golangci-lint run --allow-parallel-runners ./...`.
Guides: `python3 scripts/harness/repo_lint.py`, `python3 scripts/harness/guide_lint.py` must stay clean.

## Layout (hexagonal)
`internal/domain/{shared,returnauthorization,network}` pure Go; `internal/application/{ports,requests,views,usecases,outbox}`;
`internal/adapters/inbound/{http (chi),kafka (4 consumers),mcp (read-only),a2a (read-only),requestlog}`; `internal/adapters/outbound/{postgres,kafka,outbox,labels (the ONE in-org HTTP client, ADR 0005),telemetry,clock,ids,memory}`;
`cmd/api` (composition root), `cmd/mcp` (read-only MCP server, :8090), `cmd/a2a` (read-only A2A agent, :8093), `cmd/projector` (analytics projector, admin :8091) and `cmd/reports` (read-only reports, :8092; both ADR 0012, default off); `features/` (godog, never a .go file at the repo root); migrations embedded in the postgres adapter (0001 schema, 0002 fleet stem, 0003 the global Idempotency-Key, 0004 node status facts).
 Agent authority: every MCP tool and A2A skill declares a level (`com.warehouse/authority` _meta / `authority:L<n>` skill tag; L1 read, L2 recommend, L3 write - ADR 0011); the MCP Streamable HTTP handler is stateless and answers the 2026-07-28 revision.

## Runtime rule
Real PostgreSQL and Kafka only. `DATABASE_URL` and `KAFKA_BROKERS` are mandatory (boot error naming the variable), as are the four consumer groups (`ORDER/TOPOLOGY/SOURCING/VENDOR_CONSUMER_GROUP`). `/healthz` = liveness; `/readyz` = drain gate + dependency checks. No authentication on REST or MCP (the in-cluster ClusterIP boundary is the access control). See docs/docs/overview/runtime.md.

## REST and MCP
chi router implementing every operation of apis/openapi.yaml plus /healthz, /readyz. Every state-changing route takes `Idempotency-Key` (global key, migration 0003). Errors are RFC 9457 problem+json with kebab-case `type` slugs (incl. group-not-shipped, no-return-destination, label-unavailable, authorization-expired).
The MCP server (cmd/mcp, ADR 0006) exposes two read-only tools — one per REST GET (get_return_authorization, list_return_authorizations) — bound to the same read use cases; reads add zero outbox rows (testcontainers-proven), and the adapter touches no write seam (UnitOfWork/EventOutbox/Inbox).

## Events: CloudEvents 1.0 is MANDATORY
Envelope only via internal/adapters/kafka/cloudevents; type prefix `com.retailnetwork.commerce.customer-returns.returnauthorization.`, source `/retailnetwork/customer-returns`, topic `retailnetwork.customer-returns.events`; subject and Kafka key = authorizationId. Type catalogue in ADR 0004. Transactional outbox: aggregates + outbox rows in ONE Postgres transaction. A new event: domain struct + apis/asyncapi.yaml + ADR 0004 `type:` line in the SAME commit. No event ever carries a unit price (no-money rule) or PII.

## Domain
The aggregate, invariants and assumptions in docs/adr/0002 (A1-A10: prices are caller facts, lazy 30d expiry, first-fit outcome, unexpected lines stay facts); consumed contracts in 0003; the reverse-label route in 0005. `decide` is pure: KEEP under ReturnlessThresholdCents (1500) with every price known and DROP_OFF, else the region's ACTIVE returns centre, else the origin node, else 422 no-return-destination.

## Workflow
GitFlow: feature/* -> PR into develop, CI green before merge, never push develop or main. lefthook: pre-commit fmt/vet/lint, pre-push `make check`. Commit and push after each step.
Helm chart: charts/customer-returns (mcp and a2a both default OFF — ADRs 0006/0009; ci/all-components-values.yaml renders everything on; tests in charts/customer-returns/tests: render_test.sh + test_service_selectors.py + the python suites, run by the chart-tests job).

A2A: `cmd/a2a` (port 8093) is this context's read-only Agent2Agent agent, skill `get_return_authorization` (read), decided in `docs/adr/0009-a2a-inbound-adapter.md`; delegation only (facts stay CloudEvents on Kafka), unauthenticated like REST and MCP; rules in `.claude/rules/fleet/a2a.md`.
