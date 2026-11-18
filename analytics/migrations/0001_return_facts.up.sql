-- customer-returns analytics read model (ADR 0012).
--
-- This is the ANALYTICAL database, separate from the OLTP database. It is
-- written only by cmd/projector and read (read-only) by cmd/reports.
-- Everything here is a projection of this context's own integration topic
-- (retailnetwork.customer-returns.events), not a source of truth, and can be
-- dropped and rebuilt from the topic. No free text, address, name or price is
-- stored (the PII rule): reason is the enum return reason code.

-- Idempotency: every applied CloudEvents id is recorded here exactly once, in
-- the SAME transaction as its effect on a fact table. occurred_at is the
-- event's CloudEvents `time` (the basis of /reports/freshness).
CREATE TABLE analytics_processed_events (
    event_id    TEXT        PRIMARY KEY,
    event_type  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per authorization, from returnauthorization.ReturnAuthorized.
--   authorized_at  the payload's authorizedAt (the business instant)
CREATE TABLE return_authorizations (
    authorization_id TEXT        PRIMARY KEY CHECK (authorization_id <> ''),
    reason           TEXT        NOT NULL CHECK (reason <> ''),
    authorized_at    TIMESTAMPTZ NOT NULL,
    event_id         TEXT        NOT NULL
);

-- One row per authorization, from returnauthorization.ReturnCompleted.
--   received_at  the payload's receivedAt (the business instant)
CREATE TABLE return_receipts (
    authorization_id TEXT        PRIMARY KEY CHECK (authorization_id <> ''),
    received_at      TIMESTAMPTZ NOT NULL,
    event_id         TEXT        NOT NULL
);

-- The reports filter each fact on its business instant; the receipt side
-- joins to the authorization by its primary key.
CREATE INDEX idx_return_authorizations_authorized_at ON return_authorizations (authorized_at);
CREATE INDEX idx_return_receipts_received_at ON return_receipts (received_at);
CREATE INDEX idx_analytics_processed_events_occurred_at ON analytics_processed_events (occurred_at);
