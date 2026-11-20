---
id: entity-relationship
title: Entity Relationship
sidebar_label: Entity Relationship
sidebar_position: 10
description: "The Postgres schema of Customer Returns's own database, as created by migrations 0001 to 0004, and the optional analytical database."
---

# Entity Relationship

The schema of this context's own Postgres database, as created by the four embedded migrations in
`internal/adapters/outbound/postgres/migrations/`: `0001_customer_returns_schema` (the aggregate, the read models and the
outcome facts), `0002_customer_returns_stem` (outbox, inbox, idempotency keys), `0003_idempotency_key_global` (the
`Idempotency-Key` becomes the primary key on its own) and `0004_node_status_facts` (`returns_network_nodes.lifecycle_at`,
and the node type check dropped so the type is stored as node-topology publishes it). Nine tables, no foreign keys: the
aggregate is one row with its lines as `JSONB`, and ids of other contexts are held by value.

The aggregate and the outcome facts:

```mermaid
erDiagram
    return_authorizations {
        TEXT authorization_id PK "ret-uuid"
        TEXT order_id "by value"
        INTEGER group_no
        JSONB lines "1 to 50: lineNo, catalogItemId, quantity, unitPriceCents, ownerRef"
        TEXT reason "DEFECTIVE, NOT_AS_DESCRIBED, WRONG_ITEM, NOT_NEEDED, OTHER"
        TEXT disposition "KEEP, NEAREST_RETURNS_CENTER, ORIGIN_NODE"
        TEXT method "DROP_OFF, PICKUP"
        TEXT destination_node_id "empty for KEEP"
        TEXT origin_node_id
        TIMESTAMPTZ pickup_window_start "nullable"
        TIMESTAMPTZ pickup_window_end "nullable"
        JSONB tracking_numbers "at most 5"
        TEXT state "six states"
        TIMESTAMPTZ authorized_at
        TIMESTAMPTZ expires_at "authorized_at + 30 days"
        TIMESTAMPTZ closed_at "nullable"
        BIGINT version
        TIMESTAMPTZ created_at
    }
    return_outcomes {
        BIGSERIAL id PK
        TEXT authorization_id "by value"
        TEXT node_id
        TIMESTAMPTZ received_at
        JSONB lines "graded lines, unexpected ones included"
        TIMESTAMPTZ created_at
    }
```

Read models fed by consumed events ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-models)):

```mermaid
erDiagram
    order_group_views {
        TEXT order_id PK
        INTEGER group_no PK
        TEXT status "PROMISED, LOCKED, SHIPPED, DELIVERED, LOST, RETURNED"
        TEXT destination_postal_prefix
        TEXT witness_node_id
        JSONB lines
        TIMESTAMPTZ updated_at
    }
    group_origins {
        TEXT order_id PK
        INTEGER group_no PK
        TEXT node_id "fulfilling node"
        JSONB owners_by_item
        TIMESTAMPTZ updated_at
    }
    returns_network_nodes {
        TEXT node_id PK
        TEXT node_type
        TEXT region_id
        TEXT lifecycle "PENDING, ACTIVE, SUSPENDED, RETIRED"
        TIMESTAMPTZ lifecycle_at "nullable, migration 0004"
        TIMESTAMPTZ updated_at
    }
    service_area_prefixes {
        TEXT postal_prefix PK
        TEXT region_id
        BIGINT assignment_version
    }
```

Outbox, inbox and idempotency (infrastructure tables):

```mermaid
erDiagram
    outbox_events {
        BIGSERIAL id PK
        TEXT event_id "UNIQUE with topic"
        TEXT topic
        TEXT event_type "full CloudEvents type"
        TEXT subject
        BYTEA key "nullable"
        TEXT dataschema
        BYTEA value "encoded CloudEvent"
        JSONB headers
        TIMESTAMPTZ created_at
        TIMESTAMPTZ published_at "nullable"
        INTEGER attempts
        TEXT last_error "nullable"
    }
    processed_events {
        TEXT event_id PK "CloudEvents id"
        TEXT event_type
        TIMESTAMPTZ processed_at
    }
    idempotency_keys {
        TEXT key PK "global since 0003"
        TEXT route
        TEXT request_hash
        INTEGER status_code "nullable"
        BYTEA response_body "nullable"
        JSONB response_headers "nullable"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ completed_at "nullable"
    }
```

Source: the four migrations above. The idempotency rows are purged after 24 hours by the retention sweep in `cmd/api`
(see [Runbook](/docs/operations/runbook)). The check constraints of `return_authorizations` (a `KEEP` closes at once and
has no destination, a `PICKUP` needs a window, `IN_TRANSIT` needs a tracking number, terminal states carry `closed_at`)
are in `0001`. Column details of the outbox and consumer behaviour are on
[Troubleshooting](/docs/operations/troubleshooting).

## Analytical database tables

The analytical database is separate from the OLTP one (ADR 0012, optional, default off). It is a projection of this
context's own topic `retailnetwork.customer-returns.events`, written only by `cmd/projector` and read read-only by
`cmd/reports`; it can be dropped and rebuilt from the topic.

```mermaid
erDiagram
    analytics_processed_events {
        TEXT event_id PK "CloudEvents id"
        TEXT event_type
        TIMESTAMPTZ occurred_at "CloudEvents time"
        TIMESTAMPTZ applied_at
    }
    return_authorizations {
        TEXT authorization_id PK
        TEXT reason "enum return reason code"
        TIMESTAMPTZ authorized_at
        TEXT event_id
    }
    return_receipts {
        TEXT authorization_id PK
        TIMESTAMPTZ received_at
        TEXT event_id
    }
```

Source: `analytics/migrations/0001_return_facts.up.sql`, `internal/adapters/outbound/analyticsstore/projection.go`,
`reader.go`. The migration declares no foreign keys, so no relationship line is drawn. The tables are related by value:
the report left-joins `return_receipts` to `return_authorizations` on `authorization_id` for the time to receive, and a
receipt without its authorization counts as received but has no timing.

- `analytics_processed_events` is the idempotency claim: the projector inserts the event id (`ON CONFLICT (event_id) DO
  NOTHING`) in the same transaction as the fact it projects, so a replayed id is a no-op. `max(occurred_at)` is the basis
  of the freshness report.
- `return_authorizations` has one row per authorization from `ReturnAuthorized`, upserted on `authorization_id`;
  `authorized_at` is the payload's `authorizedAt`.
- `return_receipts` has one row per authorization from `ReturnCompleted`, upserted on `authorization_id`; `received_at` is
  the payload's `receivedAt`.
- `authorization_id`, `reason` are `CHECK (... <> '')` non-empty. Three indexes:
  `idx_return_authorizations_authorized_at`, `idx_return_receipts_received_at` and
  `idx_analytics_processed_events_occurred_at`.
