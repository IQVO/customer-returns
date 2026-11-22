---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
sidebar_position: 4
description: "Symptom, cause, check and fix for customer-returns: boot errors, readiness, outbox, consumers and dead letters, and every REST problem type."
---

# Troubleshooting

Each entry is symptom, cause, how to check, how to fix. Every cause comes from the code named in the entry. For the
signals themselves see [Observability](/docs/operations/observability); for routine procedures the
[Runbook](/docs/operations/runbook).

## Boot and readiness

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Process exits at once with `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode` | The variable is empty. There is no in-memory runtime. | The error line `service exited with error` names the variable (`cmd/api/config.go` `loadRuntime`). | Set `DATABASE_URL` ([Configuration](/docs/operations/configuration)). |
| Exit with `KAFKA_BROKERS is required` or `ORDER_CONSUMER_GROUP is required: the consumer always runs on real Kafka` (or the topology, sourcing or vendor group) | A mandatory `cmd/api` variable is missing. | Same log line. | Set it. The chart's `config.orderConsumerGroup`, `config.topologyConsumerGroup`, `config.sourcingConsumerGroup`, `config.vendorConsumerGroup` and `kafka.brokers` must all be non-empty. |
| Exit with `EVENT_PUBLISHER=log is not supported: this context publishes to real Kafka` or `EVENT_PUBLISHER: unknown value` | Only `kafka` (or unset) is accepted. | `cmd/api/config.go` `parsePublisherMode`. | Unset the variable or set `kafka`. |
| `cmd/projector` or `cmd/reports` exits naming `ANALYTICS_DATABASE_URL` (or `ANALYTICS_READER_DATABASE_URL`) or `KAFKA_BROKERS` | A mandatory analytics variable is missing. | `cmd/projector/main.go`, `cmd/reports/main.go`. | Set `analytics.database.*` and `kafka.brokers` in the chart. |
| `cmd/a2a` exits naming `A2A_AGENT_URL` | The Agent Card URL is missing, relative or not `http(s)`. | `cmd/a2a/main.go` `validateBootConfig`. | Set an absolute URL, for example `http://customer-returns-a2a.warehouse-systems.svc.cluster.local:8093/`. |
| Crash-loop after `retrying` lines (`op=run migrations` or `ping postgres`), then `service exited with error` | Postgres unreachable or the migration failed; the boot retry (`internal/bootretry`, 5 attempts, backoff 1+2+4+8 s) is exhausted and the process refuses to boot. | The last `err` on the `retrying` lines; `kubectl logs --previous`. | Fix the DSN, the Secret `customer-returns-db` or PgBouncer; the pod restarts into a healthy database. |
| Migration hangs or fails behind PgBouncer | golang-migrate holds a session advisory lock that does not survive a transaction-pooling proxy. | The migration step never finishes. | Set `MIGRATIONS_DATABASE_URL` to a direct DSN (chart `database.migrationsExistingSecretKey`). |
| `/readyz` answers 503 `{"status":"not_ready"}` | The pod is draining: SIGTERM arrived and the drain gate flipped. | Pod phase `Terminating`; the log line `shutdown: readiness flipped to not-ready`. | Nothing; it is the graceful stop. |
| `/readyz` answers 503 `dependency-unavailable`, detail `not ready: kafka` (or `postgres`) | The ping or the topic-less Kafka metadata request failed in the last 3 s round. | WARN `readiness: dependency is not ready` with `dependency` and `error`. | Restore Postgres or the broker (`KAFKA_BROKERS`). Writes keep working while only Kafka is down: events queue in `outbox_events`. |
| Pod restarts after Kafka was down | A consumer or the relay returned on its own after a persistent broker failure (`maxTransientFetchFailures` = 10); `watchWorkers` exits the process so the orchestrator restarts it. | ERROR `consumer stopped` or `outbox relay stopped`. | Restore the broker; the restart is by design. |
| Shutdown takes longer than the pod's grace period | `SHUTDOWN_DRAIN_DELAY` (5 s) + HTTP drain (10 s) + worker drain (10 s each) exceed `terminationGracePeriodSeconds`. | WARN `worker did not stop before the shutdown drain deadline`. | Keep `terminationGracePeriodSeconds` above the drain delay + 30 s. |

## Outbox and events

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Downstream contexts do not see `ReturnAuthorized` (or any event) | Kafka down, or the relay cannot publish a row. | `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id;` and ERROR `outbox relay pass failed` (`error` starts `outbox row <id> (<type>):`). | Restore Kafka; the backlog drains within `OUTBOX_RELAY_INTERVAL` passes. |
| A row stays unpublished while later rows publish | The row reached `OUTBOX_MAX_ATTEMPTS` (default 20) and is excluded from claims; ERROR `outbox relay: row reached the attempt cap...` logged once. | The same SELECT: `attempts >= 20`, `last_error` says why. | Fix the cause, then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`. |
| Events published but the CloudEvent is rejected by a consumer | The envelope is the repo's CloudEvents encoder (`internal/adapters/outbound/kafka/encoder.go`). Consumers dead-letter anything they cannot decode. | The consumer's `.dlq` message headers `x-dlq-error`. | Compare the payload with [Events](/docs/api-reference/events). |
| No trace links across services | A message without a `traceparent` header starts a new trace; rows written outside a span carry none. | Kafka headers on the message. | Expected for events written outside a request span. |

## Consumers and dead letters

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Consumer logs `broker connection stalled or lost; fetching again after the backoff` for a topic | The producer has not written yet, so the topic does not exist (the service never creates topics). | The topic list in your broker. | Benign: it recovers once the producer writes. |
| `handling failed; retrying the same message` repeating for one `partition`/`offset` | A transient failure (database error, lost race): the same message is retried with capped exponential backoff (200 ms doubling to 5 s), up to 5 attempts. | The `error` field. | Fix the database; after the 5th attempt the message is dead-lettered. |
| Messages appear on `<topic>.dlq` | Poison message: an undecodable payload or one without a field the handler needs. Or 5 transient attempts failed. An outcome for an unknown or closed authorization is NOT poison: it is recorded and acknowledged. | Headers `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error`, `x-dlq-failed-at`. | Fix the producer or the data. A replay re-applies safely only if the original was never committed: check `processed_events` (idempotent on the CloudEvents id) first. |
| `dead-letter publish failed; retrying the same message` | The DLQ write itself fails (broker down). The consumer never drops a message it could not dead-letter. | ERROR line, `attempt`. | Restore the broker. |
| An authorization stays `IN_TRANSIT` | The vendor consumer has not received a fitting `VendorReturnOutcomeSubmitted`, or every line of the outcome was unexpected (recorded, nothing completed). | `SELECT * FROM return_outcomes WHERE authorization_id = '<id>' ORDER BY id;`; consumer lag of `VENDOR_CONSUMER_GROUP`. | Wait for the returns centre; the first fitting outcome completes it. |
| Authorizations refused `group-not-shipped` for a shipped group | `order_group_views` is fed by customer-ordering events; the view lags or was written wrong. | `SELECT status, destination_postal_prefix FROM order_group_views WHERE order_id = '<id>';`; lag of `ORDER_CONSUMER_GROUP`. | Wait for the consumer, or [rebuild the read model](/docs/operations/runbook#rebuilding-a-read-model-from-its-topic). |
| Authorizations refused `no-return-destination` | No ACTIVE `RETURNS_CENTER` in the region and no origin node. | `SELECT node_type, lifecycle, count(*) FROM returns_network_nodes GROUP BY 1, 2;` and `group_origins`. | [Replay the node-topology dead letters](/docs/operations/runbook#replaying-the-node-topology-dead-letters) if every node is `PENDING`. |

## REST problem types

Errors are `application/problem+json` with `type` = the slug, `title`, `status`, `detail` and `instance` (the
request path). The table is the problem catalogue in `internal/adapters/inbound/http/errors.go` (and
`internal/adapters/inbound/reportshttp/body.go` for `cmd/reports`).

| Status | `type` | Raised when |
| --- | --- | --- |
| 404 | `not-found` | Unknown authorization id. |
| 409 | `illegal-transition` | A state change the aggregate forbids: cancelling an authorization that is no longer `AUTHORIZED`, a label for one that is closed. |
| 409 | `concurrent-update` | Another request modified the authorization (optimistic version check); re-fetch and retry. |
| 422 | `validation-failed` | Bad JSON, an unknown field, a value outside its bounds, an unknown `lineNo` for the group, a pickup window on a drop-off. |
| 422 | `group-not-shipped` | The order group is not SHIPPED or DELIVERED in `order_group_views` (the view may lag); retry with the same key. |
| 422 | `no-return-destination` | No ACTIVE returns centre serves the group's region and no origin node is known. |
| 422 | `label-unavailable` | `SHIPPING_LABELS_URL` is empty, or the shipping context's reverse-label route refused or failed (any answer but `201`). |
| 422 | `authorization-expired` | A label was asked for past `expiresAt`: the lazy cancellation (`EXPIRED`) committed with this answer. |
| 400 | `idempotency-key-required` | A `POST` without an `Idempotency-Key` header. |
| 400 | `idempotency-key-invalid` | An `Idempotency-Key` that is not 1 to 128 printable ASCII characters. |
| 409 | `idempotency-key-reused` | The same key with a different method, path or body (the key is global). A key replays a stored answer (a success or `authorization-expired`), with `Idempotency-Replayed: true`, for 24 h. A client bug: do not retry. |
| 400 | `invalid-report-range` | `cmd/reports` only: `from`/`to` not UTC dates, `from` after `to`, or a range above 366 days. |
| 405 | `method-not-allowed` | `cmd/reports` only: a method other than `GET` on a report route. |
| 503 | `dependency-unavailable` | Only `/readyz`. |
| 500 | `internal-error` | An unmapped error; the real error is in the ERROR log line `request failed`. |

Other points: a NUL character (U+0000) in the path, query or body is a uniform client error and never records the
idempotency key; a 5xx is never cached, so the same key can retry. A refusal is cached only when its problem type
is FINAL or OUTCOME (`authorization-expired`); a RETRYABLE one (`group-not-shipped`, `no-return-destination`,
`label-unavailable`, `concurrent-update`, `illegal-transition`, `not-found`, `validation-failed`) releases the key, so the
same key and body executes again ([ADR 0013](/docs/adr/0013-idempotency-replay-classes)).

## MCP and A2A

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| MCP tool returns an error with `unexpected error` | An unmapped error inside a tool; logged as ERROR `mcp tool failed with an unexpected error`. | The log line. | Same causes as `internal-error` above. |
| MCP or A2A shows stale data | They read the database `cmd/api` owns and never write; lag is the data's, not theirs. | Compare with `GET` on the REST API. | Check `cmd/api`. |
| A2A skill answers a problem envelope | The delegated message was malformed or the skill is unknown; it is answered, not raised. | Span `a2a.skill <skill>` status `Error`. | See [API reference](/docs/api-reference). |
