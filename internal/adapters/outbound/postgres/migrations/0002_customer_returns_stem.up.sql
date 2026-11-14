-- 0002_customer_returns_stem: the fleet stem -- the transactional outbox,
-- the consumers' inbox and the Idempotency-Key middleware's table.
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);

-- The relay only ever scans the unpublished tail; keep that scan tiny.
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- processed_events: the inbox of the consumers. The CloudEvents id is claimed
-- with INSERT ... ON CONFLICT DO NOTHING in the SAME transaction as the effect,
-- so a rollback un-claims it.
CREATE TABLE processed_events (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL
);

-- idempotency_keys: the fleet Idempotency-Key middleware, keyed by (route, key)
-- with a 24 hour TTL (model conventions). The middleware INSERTs a bare row
-- inside the transaction shared with the use case, then UPDATEs it with the
-- outcome before it commits, so a COMMITTED row always has a status_code.
CREATE TABLE idempotency_keys (
    route            TEXT        NOT NULL,
    key              TEXT        NOT NULL,
    request_hash     TEXT        NOT NULL,
    status_code      INTEGER,
    response_body    BYTEA,
    response_headers JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ,
    PRIMARY KEY (route, key)
);

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
