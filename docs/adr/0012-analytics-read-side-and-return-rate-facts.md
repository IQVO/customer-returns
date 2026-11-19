# ADR 0012: analytics read side and return-rate facts

## Status

Accepted (2026-11-17). Additive: no published payload, topic, OLTP table or existing REST route changes, and the
AsyncAPI contract is unchanged (no event is added). Adds two binaries (`cmd/projector`, `cmd/reports`), one
analytical-database migration set (`analytics/migrations`), one pure domain package
(`internal/domain/returnsperformance`), one read-only report route plus the shared freshness route, two default-off
chart components and a second, separate database. It copies the shape of the shipping-and-tracking pilot (its ADR
0012, `analytics-read-side`). ADR 0007 is not reused: this repo's chart and docs reserve that number for the web
remote, so the next free number is 0012.

## Context

Customer Returns owns the return facts of the retail network: a return is authorized (`ReturnAuthorized`, KEEP
included) and, when the returns centre grades it, completed (`ReturnCompleted`); this context therefore owns the
**return-rate KPI baseline**. Nothing answers it as a trend today: the facts leave as CloudEvents and the OLTP
tables hold the current state only (ADR 0002), with no history in a shape a report can aggregate. This context has no
projector and no reports.

Every event this context publishes is an integration event on `retailnetwork.customer-returns.events` (ADR 0004):
**nothing is published on `retailnetwork.customer-returns.analytics`**, and every fact the report below needs is
already on `.events`. As in the pilot, the projector therefore consumes this context's OWN `.events` topic only; the
analytics topic is not subscribed while nothing is published on it (an analytics-only event would add it in the ADR
that introduces the event). The task that produced this ADR said "do not add events", so no event, no fan-out and no
outbox change is made.

## Decision

### 1. What is projected

The projector consumes ONLY `retailnetwork.customer-returns.events` and never reads a sibling's topic (it does not
read customer-ordering's `CustomerOrderPlaced`, vendor-connectivity's outcomes or any payment topic). Two types are
projected, dispatched on the FULL CloudEvents `type` (prefix copied from `apis/asyncapi.yaml`:
`com.retailnetwork.commerce.customer-returns.returnauthorization.<EventName>`):

| type | payload fields read | fact |
| --- | --- | --- |
| `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnAuthorized` | `authorizationId` (= subject), `reason`, `authorizedAt` | `return_authorizations` (one row per authorization) |
| `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnCompleted` | `authorizationId` (= subject), `outcome` (RECEIVED), `receivedAt` | `return_receipts` (one row per authorization) |

The other two published types, `...returnauthorization.ReturnLabelIssued` and `...returnauthorization.ReturnCancelled`,
are acknowledged and not projected: no report in this ADR needs them. Anything else, and anything that is not a
CloudEvents 1.0 message, is skipped with a sampled WARN. No free text, address, name or price is read or stored (the
PII rule): the `reason` is the enum code.

Tables of the analytical database (`analytics/migrations/0001_return_facts`):

- `analytics_processed_events (event_id PK, event_type, occurred_at, applied_at)`: the dedupe set; `occurred_at` is
  the CloudEvents `time`, the basis of the freshness lag.
- `return_authorizations (authorization_id PK, reason, authorized_at, event_id)`.
- `return_receipts (authorization_id PK, received_at, event_id)`.

Both events of one authorization share the subject, hence the Kafka key and the partition, but a retained history can
be truncated (a receipt whose authorization is older than the retention) and the two facts are written by independent
statements. The model is therefore order-independent: it stores facts and the report joins them at query time.

### 2. KPI definition: `GET /reports/returns-performance?from=&to=`

Days are UTC calendar days of the **business instant in the payload** (`authorizedAt`, `receivedAt`), not of the
CloudEvents `time`. The response carries, per UTC day with any activity (days without activity are omitted):

- `returnsAuthorized` = authorizations whose `authorizedAt` falls on the day. **This is both "returns opened" and
  "returns approved"**: a `ReturnAuthorization` is born authorized (ADR 0002); a request that is refused answers a 422
  and publishes nothing, and no event marks a distinct "opened" moment. Two counters that are always equal would only
  mislead, so there is one.
- `returnsReceived` = receipts whose `receivedAt` falls on the day (`ReturnCompleted`).
- `timeToReceive` over the receipts of that day **whose authorization fact is also projected**: `timeToReceiveSamples`
  (their count), `timeToReceiveP50Seconds` and `timeToReceiveP95Seconds` = the continuous percentiles
  (`percentile_cont`, linear interpolation) 0.5 and 0.95 of `receivedAt - authorizedAt` in seconds, both `null` when
  the day has no sample. A negative duration (vendor and service clocks skewed) counts as zero.

Plus, once for the range:

- `reasonPareto`: the authorizations of the range grouped by `reason`, ordered by count descending then reason
  ascending, **bounded to 5 entries** (the enum has exactly five codes: DEFECTIVE, NOT_AS_DESCRIBED, WRONG_ITEM,
  NOT_NEEDED, OTHER; the bound holds if a code is ever added). Each entry has `reason`, `returns`, `share` (its count
  over ALL authorizations of the range, so a truncated Pareto does not sum to 1) and `cumulativeShare` (the running
  sum of the shares, in order).
- `freshness`: `asOf` = the newest CloudEvents `time` the projection applied, `lagSeconds` = now - `asOf` (never
  negative); both `null` until the first event. The same figure is served by `GET /reports/freshness`, as in the
  pilot.

Range rules (shared with the pilot): `from` and `to` are UTC dates (`YYYY-MM-DD`), both inclusive; both omitted = the
30 days ending today; only `to` = the 30 days ending on `to`; only `from` = `from` .. today; `from` after `to` or a
range over 366 days is a 400 `invalid-report-range` problem. An empty range answers `days: []`, `reasonPareto: []` and
the freshness. Wire fields are camelCase (this repo's REST guideline, ADR 0010, and the pilot); the route is a
collection-shaped report, `x-pagination-exempt: report`.

**Not delivered, and why (a design gap, not an oversight):**

- **Return rate.** A rate needs a denominator of delivered (or placed) orders or units. This context's own facts hold
  none: it publishes only return facts, and the orders it knows (`OrderGroupView`) come from customer-ordering's
  topic, which the projector must not read. The rate is therefore **left out**; the report publishes the numerator
  side (authorizations per day and the reason mix). Computing the rate is a join of this report with
  customer-ordering's own delivered-orders KPI at the consumer (the console), never a call or a read from here. A
  later ADR may add an analytics-only event carrying the delivered-group fact this context already holds in its
  `OrderGroupView`, if the owner wants the rate served from here.
- **Returns refunded and time to refund.** This context never refunds (money is customer-payments' fact) and
  publishes no refund event, so neither a count nor a timestamp exists in its own facts. Left out for the same reason;
  `ReturnCompleted` is the last fact this context knows.

SQL only counts, groups and takes `percentile_cont`. The percentile rank, the Pareto ordering and bound, the shares,
the day merge, the range rules, the freshness lag and the duration clamp live in `internal/domain/returnsperformance`,
a pure domain package that imports no other internal package (arch test), is in the coverage scope and in the
`mutation-fast` loop (`./internal/domain/*/`). ONE contract suite runs against the in-memory twin and real Postgres.

### 3. Delivery policy (the projector)

Identical to the pilot:

- Consumer group `ANALYTICS_CONSUMER_GROUP` (default `customer-returns-analytics`), fixed and stable; at-least-once,
  the offset is committed only after the message was handled. A brand-new group starts at the earliest offset, so the
  model rebuilds from retained history.
- **Dedupe and projection in ONE analytical-database transaction**: `INSERT ... ON CONFLICT DO NOTHING` into
  `analytics_processed_events`; the fact is upserted only if that insert claimed the id.
- Not a CloudEvents 1.0 message, or a CloudEvent of an unknown type: skipped and committed past with a **sampled
  WARN** (the first, then at most one per minute with the count suppressed). The two acknowledged types are silent.
- **Dead-letter ONLY deterministic poison**: an undecodable payload, a subject that is not the `authorizationId`, a
  missing required field or timestamp, or a store rejection in Postgres class 22 or 23. The raw message goes to
  `retailnetwork.customer-returns.analytics.dlq` (NOT `<events>.dlq`, which belongs to the sibling consumers of the
  same topic) with `x-dlq-*` headers. A failed DLQ write is retried.
- **Transient failures** are retried forever with capped exponential backoff and never dead-lettered.

### 4. The binaries

- `cmd/projector`: the ONLY writer of the analytical database. Env: `ANALYTICS_DATABASE_URL` (required, a direct DSN:
  it runs the embedded migrations), `KAFKA_BROKERS` (required), `ANALYTICS_CONSUMER_GROUP`, `ADMIN_ADDR` (default
  `:8091`, `/healthz` and `/readyz`), `LOG_LEVEL`, `OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_ENDPOINT`,
  `SERVICE_VERSION`, `ENVIRONMENT`. It never opens the OLTP database.
- `cmd/reports`: read-only HTTP on `HTTP_ADDR` (default `:8092`): `GET /reports/returns-performance`,
  `GET /reports/freshness`, `/healthz`, `/readyz`. It reads `ANALYTICS_READER_DATABASE_URL`, falling back to
  `ANALYTICS_DATABASE_URL` for single-role local runs, through a pool whose sessions default to read-only
  transactions with a 15s statement timeout. No Kafka, no OLTP database, no call to any sibling, **no auth** (fleet
  rule). Errors are problem+json. The routes live in `apis/openapi.yaml` under the tag `Reports` with a per-path
  server on `:8092`.
- Both ship in the one image (`/app/projector`, `/app/reports`; the Dockerfile already builds every `cmd/*`).

### 5. Why a separate analytical database

The projection is a derived copy that can be dropped and rebuilt from the topic: a heavy report cannot take OLTP
connections, the schemas evolve independently, the reports role can be read-only on a database that holds nothing
transactional, and the projector holds no OLTP credentials. Both DSNs are direct (PgBouncer is not involved).

### 6. Packaging and rollout

The chart gains an opt-in `analytics:` block, **`enabled: false` by default; the default render is byte-identical**:
components `analytics-projector` (Deployment, no Service) and `analytics-reports` (Deployment + ClusterIP Service, an
opt-in Kong HTTPRoute `/api/customer-returns/reports` rewritten to `/reports`, `konghq.com/regex-priority: "10"`
because it sits inside the API route's prefix), and an `analytics` Secret or `analytics.database.existingSecret`.
Render-time guards refuse `analytics.enabled` without a DSN source, without `kafka.brokers` or without a consumer
group. The projector pod never gets the OLTP `DATABASE_URL`; the reports pod gets neither Kafka nor any DSN but the
reader's. Nothing about the api, mcp or a2a workloads changes, so the legacy behaviour is untouched until an
operator turns the block on.

## Consequences

- The `.events` topic gains one service-owned consumer group (`customer-returns-analytics`); its contract is
  unchanged. The report is only as fresh as the projector, which `freshness` makes observable.
- The model starts at the earliest retained offset; returns authorized before that have no history, and a receipt
  whose authorization is older than the retention counts as received but has no time-to-receive sample.
- No return rate, no refund count, no time-to-refund (section 2): the KPI baseline this ADR delivers is the numerator
  and the cycle time to receipt.
- A deploy needs a second database, two roles (projector read-write, reports read-only), the DLQ topic and the
  consumer group above (`warehouse-infra`).

## Alternatives considered and rejected

- **Add an analytics topic and fan the facts out (`FanoutEncoder`)**: every fact needed is already on `.events`;
  doubling them needs an outbox `UNIQUE (event_id, topic)` change for no new information, and "do not add events".
- **Read customer-ordering's topic for the denominator**: breaks "a projector reads only its own context's topic".
- **Call a sibling's REST or MCP for the denominator**: forbidden, always.
- **Two counters (`returnsOpened`, `returnsApproved`)**: always equal in this model (section 2).
- **Day by CloudEvents `time`**: that is when the use case ran; the payload instants are the business facts.
- **Project into the OLTP database**: no isolation of load, schema or credentials.
