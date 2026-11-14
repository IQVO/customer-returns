-- 0001_customer_returns_schema: the aggregate, its two event-fed read models
-- and the outcome-facts table (ADR 0002 / ADR 0003). The outbox/inbox/
-- idempotency stem lives in 0002.

-- The ReturnAuthorization aggregate. `lines` is the Snapshot's lines array
-- (lineNo, catalogItemId, quantity, unitPriceCents?, ownerRef); prices are the
-- CALLER-declared facts the storefront sent, never published (PII/no-money
-- rule: they never leave this table).
CREATE TABLE return_authorizations (
    authorization_id     TEXT        PRIMARY KEY,
    order_id             TEXT        NOT NULL,
    group_no             INTEGER     NOT NULL CHECK (group_no >= 1),
    lines                JSONB       NOT NULL CHECK (jsonb_typeof(lines) = 'array' AND jsonb_array_length(lines) BETWEEN 1 AND 50),
    reason               TEXT        NOT NULL CHECK (reason IN ('DEFECTIVE', 'NOT_AS_DESCRIBED', 'WRONG_ITEM', 'NOT_NEEDED', 'OTHER')),
    disposition          TEXT        NOT NULL CHECK (disposition IN ('KEEP', 'NEAREST_RETURNS_CENTER', 'ORIGIN_NODE')),
    method               TEXT        NOT NULL CHECK (method IN ('DROP_OFF', 'PICKUP')),
    destination_node_id  TEXT        NOT NULL DEFAULT '',
    origin_node_id       TEXT        NOT NULL DEFAULT '',
    pickup_window_start  TIMESTAMPTZ,
    pickup_window_end    TIMESTAMPTZ,
    tracking_numbers     JSONB       NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(tracking_numbers) = 'array' AND jsonb_array_length(tracking_numbers) <= 5),
    state                TEXT        NOT NULL CHECK (state IN ('AUTHORIZED', 'IN_TRANSIT', 'RECEIVED', 'CLOSED_KEEP', 'CANCELLED', 'PICKUP_FAILED')),
    authorized_at        TIMESTAMPTZ NOT NULL,
    expires_at           TIMESTAMPTZ NOT NULL CHECK (expires_at = authorized_at + INTERVAL '30 days'),
    closed_at            TIMESTAMPTZ,
    version              BIGINT      NOT NULL CHECK (version >= 1),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- State-machine edges enforceable on a row: KEEP closes at once and never
    -- carries a destination; a physical disposition always does; PICKUP
    -- requires a window and never KEEP; a terminal state requires closed_at;
    -- IN_TRANSIT requires a linked label.
    CONSTRAINT keep_closes_at_once CHECK (disposition <> 'KEEP' OR state IN ('CLOSED_KEEP', 'CANCELLED')),
    CONSTRAINT keep_has_no_destination CHECK (disposition <> 'KEEP' OR destination_node_id = ''),
    CONSTRAINT physical_has_destination CHECK (disposition = 'KEEP' OR destination_node_id <> ''),
    CONSTRAINT pickup_needs_window CHECK (method <> 'PICKUP' OR (pickup_window_start IS NOT NULL AND pickup_window_end IS NOT NULL AND disposition <> 'KEEP')),
    CONSTRAINT dropoff_has_no_window CHECK (method <> 'DROP_OFF' OR (pickup_window_start IS NULL AND pickup_window_end IS NULL)),
    CONSTRAINT terminal_closed CHECK (state NOT IN ('RECEIVED', 'CLOSED_KEEP', 'CANCELLED', 'PICKUP_FAILED') OR closed_at IS NOT NULL),
    CONSTRAINT open_not_closed CHECK (state IN ('RECEIVED', 'CLOSED_KEEP', 'CANCELLED', 'PICKUP_FAILED') OR closed_at IS NULL),
    CONSTRAINT transit_has_label CHECK (state <> 'IN_TRANSIT' OR jsonb_array_length(tracking_numbers) >= 1)
);

-- Keyset pages of an order's authorizations, newest first.
CREATE INDEX idx_return_authorizations_order ON return_authorizations (order_id, authorized_at DESC, authorization_id DESC);

-- OrderGroupView: the per-(orderId, groupNo) projection of customer-ordering's
-- CustomerOrderPlaced and the terminal ShopperNoticeDecided families.
CREATE TABLE order_group_views (
    order_id                    TEXT        NOT NULL,
    group_no                    INTEGER     NOT NULL CHECK (group_no >= 1),
    status                      TEXT        NOT NULL CHECK (status IN ('PROMISED', 'LOCKED', 'SHIPPED', 'DELIVERED', 'LOST', 'RETURNED')),
    destination_postal_prefix   TEXT        NOT NULL,
    witness_node_id             TEXT        NOT NULL DEFAULT '',
    lines                       JSONB       NOT NULL CHECK (jsonb_typeof(lines) = 'array'),
    updated_at                  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (order_id, group_no)
);

-- ReturnsNetworkView, node half: which returns centres exist and are ACTIVE
-- per region (from node-topology's node events).
CREATE TABLE returns_network_nodes (
    node_id    TEXT PRIMARY KEY,
    node_type  TEXT        NOT NULL CHECK (node_type IN ('FULFILLMENT_CENTER', 'RETURNS_CENTER', 'INBOUND_CROSS_DOCK', 'DELIVERY_STATION', 'SORTATION_CENTER', 'AIR_HUB', 'PICKUP_POINT')),
    region_id  TEXT        NOT NULL,
    lifecycle  TEXT        NOT NULL CHECK (lifecycle IN ('PENDING', 'ACTIVE', 'SUSPENDED', 'RETIRED')),
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_returns_network_nodes_region ON returns_network_nodes (region_id, node_id) WHERE node_type = 'RETURNS_CENTER' AND lifecycle = 'ACTIVE';

-- ReturnsNetworkView, prefix half: which region a postal prefix belongs to
-- (highest assignmentVersion wins; the repository no-ops a stale write).
CREATE TABLE service_area_prefixes (
    postal_prefix      TEXT   PRIMARY KEY,
    region_id          TEXT   NOT NULL,
    assignment_version BIGINT NOT NULL CHECK (assignment_version >= 0)
);

-- ReturnsNetworkView, origin half: which node fulfilled the order group and
-- whose stock each catalog item took (ownerRef by catalogItemId).
CREATE TABLE group_origins (
    order_id       TEXT        NOT NULL,
    group_no       INTEGER     NOT NULL CHECK (group_no >= 1),
    node_id        TEXT        NOT NULL DEFAULT '',
    owners_by_item JSONB       NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(owners_by_item) = 'object'),
    updated_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (order_id, group_no)
);

-- Outcome facts: the raw graded outcomes the vendors' connectivity context
-- submitted, lines beyond the authorization flagged unexpected (ADR 0003).
-- Facts for the audit trail only: nothing re-decides an authorization from
-- them and no money is computed here.
CREATE TABLE return_outcomes (
    id               BIGSERIAL   PRIMARY KEY,
    authorization_id TEXT        NOT NULL,
    node_id          TEXT        NOT NULL,
    received_at      TIMESTAMPTZ NOT NULL,
    lines            JSONB       NOT NULL CHECK (jsonb_typeof(lines) = 'array'),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_return_outcomes_authorization ON return_outcomes (authorization_id, id);
