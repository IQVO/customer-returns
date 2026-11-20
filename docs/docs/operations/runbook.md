---
id: runbook
title: Runbook
sidebar_label: Runbook
sidebar_position: 2
description: "Deploying and operating customer-returns: components, probes, migrations, Kafka topics and consumer groups, outbox relay, DLQ, sweepers and failure procedures."
---

# Runbook

**customer-returns** is the retail network's shopper-facing returns front door: it authorizes the
return of a shipped or delivered order group, decides its disposition (`KEEP`, `NEAREST_RETURNS_CENTER`
or `ORIGIN_NODE`), links the reverse label from the shipping context and completes the return from the
returns centre's graded outcome.
Sources: [ADR 0002](/docs/adr/0002-aggregates-and-invariants) (aggregates),
[0003](/docs/adr/0003-consumed-contracts-and-local-read-models) (consumed contracts),
[0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) (event catalogue),
[0005](/docs/adr/0005-the-reverse-label-route) (the reverse-label route) and
[Runtime](/docs/overview/runtime) (probes, shutdown, outbox).

## Runtime shape

The chart `charts/customer-returns` deploys up to five components. The `api` Deployment runs `cmd/api` and
is always on (`replicaCount: 1`, HPA off by default). Four are optional and off by default: `mcp`
(`cmd/mcp`, `mcp.enabled`), `a2a` (`cmd/a2a`, `a2a.enabled`), and `analytics-projector` and `analytics-reports`
(`cmd/projector`, `cmd/reports`, both behind `analytics.enabled`; see
[Analytics read side](#analytics-read-side-adr-0012-default-off)). There is no web or frontend component. Inside the `api` process (`cmd/api/main.go`, `wire_adapters.go`,
`wire_background.go`):

- REST returns API on `:8080` (`internal/adapters/inbound/http/server.go`): authorize, read,
  list, cancel and label return authorizations; every write behind an `Idempotency-Key`
  header (24h replay, [ADR 0013](/docs/adr/0013-idempotency-replay-classes)).
- Transactional outbox relay draining `outbox_events` to Kafka
  (`internal/adapters/outbound/outbox/relay.go`).
- Four Kafka consumers, one per producer topic (ADR 0003): order
  (`retailnetwork.customer-ordering.events`), topology (`retailnetwork.node-topology.events`),
  sourcing (`retailnetwork.order-sourcing.events`) and vendor
  (`retailnetwork.vendor-connectivity.events`) - `internal/adapters/inbound/kafka/consumers*.go`.
- One loop: a 24h retention sweeper for expired idempotency keys (`startBackground` in
  `cmd/api/wire_background.go`).

If the relay, a consumer or the loop returns on its own, the process exits non-zero so
the orchestrator restarts it (`watchWorkers`); it never sits "alive" with a dead worker.

## Dependencies

- **Postgres** - `DATABASE_URL` (pooled DSN, PgBouncer in-cluster) and
  `MIGRATIONS_DATABASE_URL` (direct DSN for the boot migrations only: golang-migrate
  holds a session advisory lock, which breaks behind a transaction-pooling proxy).
  Both from the Secret `customer-returns-db` (chart `database.existingSecret`; the
  migrations key is `optional: true`, the binary falls back to `DATABASE_URL`).
  Embedded migrations run at boot with backoff (`internal/bootretry`).
- **Kafka** - `KAFKA_BROKERS` (required): relay sink, the four consumers, `/readyz`.
  Topics are auto-created by the first writer (`AllowAutoTopicCreation: true`,
  `internal/adapters/outbound/kafka/writer.go`). Publishes
  `retailnetwork.customer-returns.events` (`.../outbound/kafka/encoder.go`; the analytics
  topic is declared there and no v1 event is published on it); dead letters go to
  `<topic>.dlq` (`.../inbound/kafka/deadletter.go`).
- **One sibling REST route**: the reverse-label route of the shipping context
  (ADR 0005), called only when `SHIPPING_LABELS_URL` is set. It is unset in every
  deployment today; with it unset a label request answers `422 label-unavailable`.
  What must hold before it is set:
  [Before the reverse-label client is enabled](#before-the-reverse-label-client-is-enabled).
  Nothing else is called synchronously (ADR 0003).

## Health

`internal/adapters/inbound/http/readiness.go`, wired in `cmd/api/wire_adapters.go` (`newReadiness`):

- `GET /healthz` - liveness only: `200 {"status":"ok"}` while the process serves, even
  while draining or with every dependency down.
- `GET /readyz` - `200 {"status":"ready"}` only while the drain gate is open, Postgres
  answers `PING` and a Kafka broker answers a topic-less metadata request (probing
  never creates topics). Verdict cached 3s, one round bounded 2s. Draining: `503 {"status":"not_ready"}`. Dependency down: `503 application/problem+json`
  `dependency-unavailable` naming `postgres` and/or `kafka`.

Writes with a dependency down, per code:

- **Postgres down** - writes fail: aggregate, idempotency row and outbox rows commit in
  ONE Postgres transaction, so nothing durable can happen (`errors.go` falls through
  to the internal-error problem). `/readyz` 503 pulls the pod from Kong first.
- **Kafka down** - writes are STILL accepted: events are committed to `outbox_events`
  and the relay retries every `OUTBOX_RELAY_INTERVAL`; only `/readyz` goes 503, and the
  process exits when a consumer notices (`watchWorkers`) so it restarts into a healthy broker.

## Configuration

The full variable list, with defaults, is [Configuration](/docs/operations/configuration). In the chart
each `cmd/api` variable maps 1:1 to a `values.yaml` `config.*` key rendered into the ConfigMap/container env
(checksum-annotated so a change rolls the pods); secret keys come from the `customer-returns-db` Secret.

| Env var | Chart value | Default / meaning |
| --- | --- | --- |
| `DATABASE_URL` | `database.existingSecret` | required, pooled Postgres DSN |
| `MIGRATIONS_DATABASE_URL` | `database.migrationsExistingSecretKey` | optional, direct DSN for boot migrations |
| `KAFKA_BROKERS` | `kafka.brokers` | required, comma-separated `host:port` |
| `ORDER_CONSUMER_GROUP` | `config.orderConsumerGroup` | required, stable group id (customer-ordering events) |
| `TOPOLOGY_CONSUMER_GROUP` | `config.topologyConsumerGroup` | required, stable group id (node-topology events) |
| `SOURCING_CONSUMER_GROUP` | `config.sourcingConsumerGroup` | required, stable group id (order-sourcing events) |
| `VENDOR_CONSUMER_GROUP` | `config.vendorConsumerGroup` | required, stable group id (vendor-connectivity outcome events) |
| `SHIPPING_LABELS_URL` | `config.shippingLabelsUrl` | empty; the base URL of the reverse-label route. Empty keeps the label client off (ADR 0005) |
| `EVENT_PUBLISHER` | `config.eventPublisher` | `kafka` only; anything else is a boot error |
| `HTTP_ADDR` | `config.httpAddr` | `:8080` |
| `LOG_LEVEL` | `config.logLevel` | `info` (`debug`, `info`, `warn`, `error`) |
| `OUTBOX_RELAY_INTERVAL` | `config.outboxRelayInterval` | `1s` |
| `OUTBOX_MAX_ATTEMPTS` | `config.outboxMaxAttempts` | `20`; `0` = unlimited |
| `SHUTDOWN_DRAIN_DELAY` | `config.shutdownDrainDelay` | `5s`; `0` disables |

## Migrations

Both `cmd/api` and, independently, `cmd/mcp` and `cmd/a2a` run the embedded golang-migrate migrations
(`internal/adapters/outbound/postgres/migrations/`) at boot, with backoff, before opening the pool
(`internal/bootretry`): `0001_customer_returns_schema`, `0002_customer_returns_stem` (outbox, inbox, idempotency keys),
`0003_idempotency_key_global` and `0004_node_status_facts` (`returns_network_nodes.lifecycle_at`, and the node type
stored as node-topology publishes it). golang-migrate's advisory lock makes concurrent starts safe, but it is a
session lock: where `DATABASE_URL` points at a transaction-pooling proxy set `MIGRATIONS_DATABASE_URL` to a direct
DSN. There is no manual migration step and no separate migration job. Every migration is forward-only and additive
for existing rows; none rewrites a read model. Rows a fix cannot derive from what is stored are rebuilt from the
topic ([Rebuilding a read model from its topic](#rebuilding-a-read-model-from-its-topic)).

## Kafka topics, consumer groups and the DLQ

| Topic | Direction | Handled by | Consumer group (env var) |
| --- | --- | --- | --- |
| `retailnetwork.customer-ordering.events` | consumed | order consumer (`CustomerOrderPlaced`, `ShopperNoticeDecided`) | `ORDER_CONSUMER_GROUP` |
| `retailnetwork.node-topology.events` | consumed | topology consumer (`NodeRegistered`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `ServiceAreaAssigned`) | `TOPOLOGY_CONSUMER_GROUP` |
| `retailnetwork.order-sourcing.events` | consumed | sourcing consumer (`FulfillmentReleasedToNode`, `FulfillmentLocked`) | `SOURCING_CONSUMER_GROUP` |
| `retailnetwork.vendor-connectivity.events` | consumed | vendor consumer (`VendorReturnOutcomeSubmitted`) | `VENDOR_CONSUMER_GROUP` |
| `retailnetwork.customer-returns.events` | published by the relay | outbox relay | n/a |
| `<topic>.dlq` | published on poison messages | each consumer | n/a |

Group ids are stable and service-prefixed, never the pod name. The ids the cluster uses are set in `warehouse-infra`
`helm-values/customer-returns.yaml` (`config.orderConsumerGroup` and its three siblings): `customer-returns-order`,
`customer-returns-topology`, `customer-returns-sourcing` and `customer-returns-vendor`. Topics are auto-created by the
first writer; the service creates none through an admin API. A consumer group that has no committed offset starts at
the EARLIEST retained offset of its topic (kafka-go's default, which `readerConfig` keeps:
`TestANewConsumerGroupReadsTheTopicFromItsStart`), so a new group id re-reads the whole topic. The inbox makes that
safe: an event id already in `processed_events` is skipped.

## Alerts and dashboards

- Grafana dashboard: uid `warehouse-customer-returns` (fleet convention, configmap
  `grafana-dashboards-warehouse` in namespace `observability`).
- Pod-restart alerting covers every pod in `warehouse-systems`
  (`WarehousePodRestarting` on `kube_pod_container_status_restarts_total`,
  warehouse-infra `terraform/alerting/warehouse-alert-rules.json`).
- Logs are JSON on stdout; Alloy maps the pod label `app.kubernetes.io/name` to the
  Loki label `app`, so query Loki with `{namespace="warehouse-systems",
  app="customer-returns"}`.
- Metrics are OpenTelemetry pushes to the Collector ([Observability](/docs/operations/observability)): HTTP
  request duration, Go runtime metrics and the business counter `customer_returns.return.authorized`. There is no
  instrument for consumer lag or outbox depth; read those from Kafka and from `outbox_events`
  (`published_at IS NULL`) directly.

## Failure modes

- **Pod crash-loops.** Diagnosis: `kubectl -n warehouse-systems logs deploy/customer-returns`
  - a missing/invalid env var is a boot error naming the variable; migrations and the first ping retry
  with backoff before the process gives up. The pod also restarts by design when the
  relay, a consumer or the loop dies. Action: fix the ConfigMap/Secret (or the
  dependency), then `rollout restart`.
- **Postgres unreachable.** Symptom: `/readyz` 503 naming `postgres`; REST writes fail
  (see Health). Action: check PgBouncer and the `customer-returns-db` Secret; recovery is
  automatic.
- **Kafka unreachable.** Symptom: `/readyz` 503 naming `kafka`; REST writes are STILL
  accepted and queue in `outbox_events` (`published_at IS NULL`); the relay retries each
  pass. Action: restore brokers; the backlog drains itself.
- **Outbox relay stuck / a row parked at the attempt cap.** A row whose `attempts`
  reached `OUTBOX_MAX_ATTEMPTS` (default 20) is EXCLUDED from future claims
  (`AND attempts < $2` in `internal/adapters/outbound/postgres/outbox_repository.go`)
  and stays in `outbox_events` with `attempts`/`last_error` for inspection; the relay
  logs `outbox relay: row reached the attempt cap and is excluded from future claims;
  re-arm it with attempts = 0 after fixing the cause` once at ERROR (`relay.go`,
  `AttemptCapReachedMsg`). Later rows are NOT blocked. Re-arm after fixing the cause:
  `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`
- **Consumer lag or retry logs.** The four consumed topics belong to their producers;
  if a producer has not written yet, its topic does not exist and the consumer only logs
  reader retries - benign. A transient handler failure retries the SAME message with
  capped exponential backoff up to 5 attempts, then dead-letters it to `<topic>.dlq`; a
  payload that can never be applied (undecodable, or without a field the handler needs) is
  dead-lettered at once (`internal/adapters/inbound/kafka/consumer.go`, `deadletter.go`).
  Effects are idempotent on the CloudEvents id (`processed_events`). A dead-lettered fact
  was never claimed there: the claim is made inside the unit of work that applies the fact,
  a refused payload never reaches it and a failed unit rolls it back
  (`TestADeadLetteredFactWasNeverClaimedAndAppliesWhenRedelivered`, the inbox contract in
  `internal/testing/repocontract`). So a dead letter is applied by delivering it again once
  the cause is fixed: [Replaying the node-topology dead letters](#replaying-the-node-topology-dead-letters).
- **Authorizations refused `group-not-shipped` for a shipped group.** `order_group_views` is fed
  by customer-ordering events: check the group's `status` there and that the order consumer is
  not behind. The caller retries with the same key (the refusal is RETRYABLE, never stored).
- **Authorizations refused `no-return-destination`.** No ACTIVE `RETURNS_CENTER` serves the
  group's region in `returns_network_nodes`/`service_area_prefixes` and no origin node is in
  `group_origins`: check the topology and sourcing consumers, and the dead-letter replay below.

## Replaying the node-topology dead letters

**When.** Once, after the release that carries the node status fix (ADR 0003, addendum 2026-11-17) is deployed. Before
it, the topology consumer refused every `NodeActivated`, `NodeSuspended` and `NodeRetired` node-topology published
(the facts carry the node id and an instant; the consumer demanded a `nodeType`) and dead-lettered them to
`retailnetwork.node-topology.events.dlq` with `x-dlq-error: poison message: malformed event payload`. Every node in
`returns_network_nodes` is therefore still `PENDING` and no returns centre is a destination.

**What is true about those facts** (each line is pinned by a test):

- None was claimed in `processed_events`, so the inbox does not swallow it when it is delivered again
  (`TestADeadLetteredFactWasNeverClaimedAndAppliesWhenRedelivered`).
- The fixed consumer applies them as published (`TestTheObservedDeadLetterIsAppliedNow`,
  `TestProducerExamplesNodeLifecycle`).
- They are still on the SOURCE topic, `retailnetwork.node-topology.events`, at the offset their `x-dlq-source-offset`
  header names, for as long as the topic retains them. This service reads the source topic only; it has no reader for a
  `.dlq` topic. The replay is therefore a second read of the source topic, never a publish.
- A status fact older than the one already applied is a no-op (`TestStaleNodeStatusFactIsANoOp`), so a fact replayed
  after a newer status of the same node cannot undo it.

**Steps.**

1. Confirm the release is running and migration `0004` is applied:
   `SELECT count(*) FROM information_schema.columns WHERE table_name = 'returns_network_nodes' AND column_name = 'lifecycle_at';`
   answers `1`.
2. List the dead letters (run where the Kafka CLI tools are; in the cluster that is inside the broker pod), keeping
   the headers: `kafka-console-consumer.sh --bootstrap-server <broker> --topic retailnetwork.node-topology.events.dlq --from-beginning --timeout-ms 10000 --property print.headers=true --property print.key=true`.
   Note the highest `x-dlq-source-offset` per `x-dlq-source-partition`, and one or two `nodeId`s to check at the end.
3. Check the source topic still holds them: the earliest offset of each partition
   (`kafka-get-offsets.sh --bootstrap-server <broker> --topic retailnetwork.node-topology.events --time -2`) must be
   at or below the lowest `x-dlq-source-offset` of that partition. Retention is not configured by this repository or by
   its chart, and `warehouse-infra` declares no topic resource for it, so it is whatever the broker has and must be read
   there: a topic override shows in
   `kafka-configs.sh --bootstrap-server <broker> --entity-type topics --entity-name retailnetwork.node-topology.events --describe`,
   otherwise the broker's `log.retention.hours` applies (Kafka's own default is 168 hours).
   If a dead letter's offset is already below the earliest offset, see *When the source topic no longer has the fact*.
4. Make the topology consumer read its topic again, one of:
   - **A new group id** (a configuration change, no downtime): set `config.topologyConsumerGroup` to a new stable id,
     for example `customer-returns-topology-r2` (in the cluster: `warehouse-infra` `helm-values/customer-returns.yaml`),
     and sync. The ConfigMap checksum rolls the pod; the new group has no committed offset and starts at the earliest
     offset. Keep the new id: it is the group's id from now on.
   - **Or reset the offsets of the current group.** A group's offsets can only be reset while it has no member:
     `kubectl -n warehouse-systems scale deploy/customer-returns --replicas=0`, then
     `kafka-consumer-groups.sh --bootstrap-server <broker> --group customer-returns-topology --topic retailnetwork.node-topology.events --reset-offsets --to-earliest --execute`,
     then scale back to `1`.
5. Every fact of the topic is delivered again. The ones already in `processed_events` (the registrations, the service
   areas) are skipped; the status facts are applied in the order node-topology published them.
6. Verify: `SELECT lifecycle, count(*) FROM returns_network_nodes GROUP BY lifecycle;` shows `ACTIVE` nodes; the
   `nodeId`s noted in step 2 have the lifecycle of their LAST status fact
   (`SELECT node_id, lifecycle, lifecycle_at FROM returns_network_nodes WHERE node_id = '<id>';`); the group's lag is
   `0`; and no new message arrived on `retailnetwork.node-topology.events.dlq` (its end offset is what it was in step 2).
   The old dead letters stay on the `.dlq` topic until its retention removes them; they are history, not a backlog.

**When the source topic no longer has the fact.** The `.dlq` record is then the only copy (same key and value, plus the
`x-dlq-*` headers). Nothing in this service reads it. Getting it applied means publishing that key and value again on
`retailnetwork.node-topology.events`, which is node-topology's topic: it is that context's decision and its operator's
action, not a step of this runbook. Because a stale status fact is a no-op and a redelivered id is skipped, such a
republish is safe for this consumer in any order.

## Rebuilding a read model from its topic

The read models are event-fed and there is no replay tool: a row is rebuilt by delivering its fact again. The inbox
decides what "again" means. An event id in `processed_events` is skipped, whatever the consumer group
(`TestRebuildOrderGroupPrefix`: redelivered while still claimed, the row is not repaired). So a rebuild is two things,
in this order: delete the claims of the event type whose rows must be rewritten, then make the consumer read its
topic from the start. Reading the topic again WITHOUT deleting the claims changes nothing.

The claims are deleted by event type, with the statement the Postgres contract test runs word for word
(`internal/adapters/outbound/postgres/contract_integration_test.go`; it deletes those claims and no other):

```sql
DELETE FROM processed_events WHERE event_type = '<event_type>';
```

| Rows to rebuild | Written wrong by | `event_type` of the claims | Topic, consumer group variable | Why applying the fact again is safe |
| --- | --- | --- | --- | --- |
| `order_group_views.destination_postal_prefix` is empty (`SELECT count(*) FROM order_group_views WHERE destination_postal_prefix = '';`) | releases before the ADR 0003 addendum of 2026-11-17: the prefix was read from the group, where customer-ordering never publishes it | `CustomerOrderPlaced` | `retailnetwork.customer-ordering.events`, `ORDER_CONSUMER_GROUP` | `OnOrderPlaced` writes the lines, the witness and the order's prefix and never demotes a status a notice already set (`TestRebuildOrderGroupPrefix`) |
| `group_origins` has no row for the follower groups of a merged release | the same releases: only the carrier group of a merged release got an origin | `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode` | `retailnetwork.order-sourcing.events`, `SOURCING_CONSUMER_GROUP` | `OnOriginFact` merges into the row; the releases are applied in topic order and each member's `FulfillmentLocked`, still claimed, is skipped (`TestRebuildMergedReleaseOrigins`) |

**Steps**, for one row of the table:

1. Check the topic still holds the facts: its earliest offset
   (`kafka-get-offsets.sh --bootstrap-server <broker> --topic <topic> --time -2`) must be `0`, or at least older than
   the first fact to rebuild. Retention is the broker's (see step 3 of the dead-letter replay above). A fact the topic
   no longer holds cannot be rebuilt from it, and deleting its claim gains nothing.
2. Delete the claims with the statement above and note the row count: it is the number of facts the replay will
   apply.
3. Make the consumer read the topic from the start: set its consumer group variable to a new stable id (the chart
   value named after the variable, in the cluster `warehouse-infra` `helm-values/customer-returns.yaml`) and sync, or
   scale the Deployment to `0`, reset the offsets of the current group to the earliest and scale back
   (`kafka-consumer-groups.sh ... --reset-offsets --to-earliest --execute`). Steps 2 and 3 may be done in either order
   only if the consumer is stopped in between; with a running consumer, delete the claims FIRST.
4. Verify with the query of the first column, and that the group's lag returns to `0`.

A rebuild never has to stop the service: a request that meets a row not rebuilt yet answers what a lagging view
answers (`group-not-shipped`, `no-return-destination` or `label-unavailable`), and the caller retries.

## Before the reverse-label client is enabled

`SHIPPING_LABELS_URL` is unset in every deployment and stays unset until every line below holds. The label call names
the return's destination by its node id and sends no postal prefix; the shipping context derives the label's
destination prefix from ITS OWN view of node-topology's registered nodes (ADR 0005, addendum 2026-11-18). This context
holds no postal code or prefix of a node, so there is nothing to backfill here for the label. Enabling the client early
is not unsafe, it only fails labels: a node the provider does not know is refused there with
`409 destination-prefix-unavailable`, which is `422 label-unavailable` here, with nothing linked and nothing published.

1. **The provider's node view is filled.** This is the shipping context's step, in its runbook (its ADR 0018): its view
   of the registered nodes must hold every node that can be a return destination, which for nodes registered before its
   change means its rebuild from `retailnetwork.node-topology.events`. Ask its operator for confirmation; this
   repository cannot check it.
2. **This context's node-topology dead letters were replayed** ([above](#replaying-the-node-topology-dead-letters)), so
   returns centres are `ACTIVE` here and `decide` can choose NEAREST_RETURNS_CENTER:
   `SELECT count(*) FROM returns_network_nodes WHERE node_type = 'RETURNS_CENTER' AND lifecycle = 'ACTIVE';` is not `0`.
3. Only then set `config.shippingLabelsUrl` (in the cluster: `warehouse-infra` `helm-values/customer-returns.yaml`).
4. After enabling: a `label-unavailable` for a return whose destination node the provider does not hold yet heals by
   itself. The provider does not keep that refusal under the `Idempotency-Key`, and the key is the deterministic
   `<authorizationId>:<packageNo>`, so the shopper's retry is served once the provider knows the node. Nothing is to be
   repaired on this side.

## Operational actions

```sh
kubectl -n warehouse-systems rollout restart deploy/customer-returns
kubectl -n warehouse-systems logs deploy/customer-returns --follow
```

Logs in Grafana Loki: `{namespace="warehouse-systems", app="customer-returns"}`
(JSON lines; `level`, `loop`, `consumer`, `error` fields). Restarts are graceful:
`/readyz` flips to 503, `SHUTDOWN_DRAIN_DELAY` (5s) waits, HTTP drains (10s), then
consumers and the loop stop, the relay LAST (so nothing committed is stranded); the chart's
`terminationGracePeriodSeconds` must stay above drain delay + 30s. Any `config.*`
value change rolls the pods automatically (ConfigMap checksum annotation).

## Analytics read side (ADR 0012, default off)

`analytics.enabled=true` in the chart adds two Deployments from the same image: `analytics-projector` (`/app/projector`,
no Service, admin probes on `:8091`) and `analytics-reports` (`/app/reports`, ClusterIP Service on `:80` to `:8092`;
`analytics.reports.route.enabled` adds the Kong route `/api/customer-returns/reports`, which carries
`konghq.com/regex-priority: "10"` because it sits inside the API route's prefix). The render is refused without a DSN
source, `kafka.brokers` or a consumer group.

- **Database**: a SEPARATE analytical Postgres (`analytics.database.projectorUrl`, a direct DSN, read-write; `reportsUrl`,
  a read-only role) with its own schema in `analytics/migrations/` (`0001_return_facts`: `analytics_processed_events`,
  `return_authorizations`, `return_receipts`), applied by `cmd/projector` at boot. It is a projection of the service's own
  topic and can be dropped and rebuilt by replaying it.
- **Kafka**: reads `retailnetwork.customer-returns.events` under the group `customer-returns-analytics`
  (`analytics.projector.consumerGroup`, stable); poison only goes to `retailnetwork.customer-returns.analytics.dlq`.
  Transient failures retry forever with capped backoff and are never dead-lettered.
- **Probe the route**: `curl <gateway>/api/customer-returns/reports/freshness`; a 200 `{asOf, lagSeconds}` proves the
  route and the projection (both `null` until the first event).
- **Reports lag or look empty**: check `/reports/freshness` `lagSeconds`, the projector logs (`analytics: skipping...`
  sampled WARN, `dead-lettering...` ERROR) and the DLQ topic. To rebuild, point the projector at a new empty analytical
  database or a new consumer group (a brand-new group starts at the earliest retained offset).
- **No return rate, no refund figures**: by design (ADR 0012); do not look for them in these reports.
