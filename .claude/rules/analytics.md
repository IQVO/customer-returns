---
paths:
  - "cmd/projector/**"
  - "cmd/reports/**"
  - "analytics/**"
  - "internal/domain/returnsperformance/**"
  - "internal/adapters/outbound/analyticsstore/**"
  - "internal/adapters/inbound/kafka/analytics_*.go"
  - "internal/adapters/inbound/reportshttp/**"
---

# Analytics read side (projector + reports)

Record: `docs/adr/0012-analytics-read-side-and-return-rate-facts.md`. The read side consumes the facts this context
already publishes; do not change the encoder, the outbox or the `.events` contract from here, and add no event for it.

- Source: ONLY `retailnetwork.customer-returns.events` (never a sibling's topic, never a REST/MCP call to one). Two
  projected types, dispatched on the FULL CloudEvents type
  (`com.retailnetwork.commerce.customer-returns.returnauthorization.<Event>`,
  `internal/adapters/inbound/kafka/analytics_decode.go`): `ReturnAuthorized`, `ReturnCompleted`. `ReturnLabelIssued` and
  `ReturnCancelled` are acknowledged silently; anything else (and non-CloudEvents) is skipped with a sampled WARN.
- `cmd/projector` is the ONLY writer of the SEPARATE analytical database (`ANALYTICS_DATABASE_URL`, `KAFKA_BROKERS`,
  `ANALYTICS_CONSUMER_GROUP` default `customer-returns-analytics`, `ADMIN_ADDR` `:8091`). It never opens the OLTP database.
  `cmd/reports` (`HTTP_ADDR` `:8092`) reads through a read-only pool (`ANALYTICS_READER_DATABASE_URL`, falling back to
  `ANALYTICS_DATABASE_URL`).
- Dedupe on the CloudEvents `id` and the fact write are ONE analytical-database transaction (`Projection.Apply`); test
  atomicity with a trigger that RAISEs (`postgres_integration_test.go`).
- Delivery policy: dead-letter ONLY deterministic poison (bad payload, subject mismatch, outcome other than RECEIVED,
  missing field, Postgres class 22/23) to `retailnetwork.customer-returns.analytics.dlq`; retry transient failures forever
  with capped backoff.
- `internal/domain/returnsperformance` is pure and imports no other internal package (arch test). SQL only counts, groups
  and takes `percentile_cont`; the percentile ranks, the Pareto ordering and its bound of 5, the shares, the day merge,
  range rules, the freshness lag and the duration clamp live there, in the `mutation-fast` loop (`internal/domain/*/`) and
  in the coverage scope. One contract suite (`analyticsstore/contract_test.go`) runs against `MemoryStore` and Postgres:
  change both twins together.
- NOT in the report, by design (ADR 0012): the return rate (no denominator in our own facts) and anything about refunds.
  Do not add them without an analytics-only event decided in a new ADR.
- `analytics/migrations` is embedded (`analytics/migrations.go`), additive, with down files; never edit an applied
  migration, add the next number.
- Reports are unauthenticated, read-only GETs under `/reports/...` (plus `/healthz`, `/readyz`), camelCase, UTC days,
  `from`/`to` inclusive dates. Any route change edits `apis/openapi.yaml` (tag `Reports`) and regenerates the REST
  reference in the same commit.
- Chart: the `analytics:` block is opt-in (`enabled: false`); keep `tests/render_test.sh` and
  `tests/test_service_selectors.py` green and the default render byte-identical.
