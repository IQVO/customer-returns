# 3. Consumed contracts and local read models

## Status

Accepted (2026-11-14).

## Context

`decide` needs three facts this context does not own: whether the order group is SHIPPED or DELIVERED (customer-ordering), which returns centre serves the group's region (node-topology) and which node fulfilled the group with whose stock (order-sourcing). The network rule is Conformist: consume the sibling's published facts into a LOCAL read model and never call the producer back — with the one documented exception of the shipping context's reverse-label route (ADR 0005).

## Decision

### The four consumers (all in `internal/adapters/inbound/kafka`, one per producer topic)

| Consumer | Topic (group env) | Types handled |
| --- | --- | --- |
| customer-ordering | `retailnetwork.customer-ordering.events` (`ORDER_CONSUMER_GROUP`) | `...customerorder.CustomerOrderPlaced`, `...order.ShopperNoticeDecided` |
| node-topology | `retailnetwork.node-topology.events` (`TOPOLOGY_CONSUMER_GROUP`) | `NodeRegistered`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `...servicearea.ServiceAreaAssigned` |
| order-sourcing | `retailnetwork.order-sourcing.events` (`SOURCING_CONSUMER_GROUP`) | `...assignment.FulfillmentReleasedToNode`, `...assignment.FulfillmentLocked` |
| vendor-connectivity | `retailnetwork.vendor-connectivity.events` (`VENDOR_CONSUMER_GROUP`) | `...returnoutcome.VendorReturnOutcomeSubmitted` |

Delivery semantics are the fleet's: at-least-once with an atomic effect (the `processed_events` inbox claim and the projection commit in ONE unit of work), offsets committed only after success, capped-exponential-backoff retry for transient failures, and a DLQ (`<topic>.dlq` with `x-dlq-*` headers) for payloads that can never be applied. A type this context does not act on is silently committed past; `data` is decoded tolerantly (unknown fields ignored).

### The read models (event-fed, Postgres, migration `0001`)

- **OrderGroupView** (`order_group_views`, keyed `(orderId, groupNo)`): status, destination postal PREFIX, witness node and the placed lines. `CustomerOrderPlaced` writes PROMISED; the terminal `ShopperNoticeDecided` families (GROUP_SHIPPED, GROUP_DELIVERED, GROUP_LOST, GROUP_RETURNED) set the status — a fact's `status` wins over the family. Only SHIPPED and DELIVERED are authorizable (`GroupStatus.Authorizable`).
- **ReturnsNetworkView**, three halves:
  - nodes (`returns_network_nodes`): only ACTIVE `RETURNS_CENTER`s are `decide` candidates; the row's region comes from the node facts.
  - prefixes (`service_area_prefixes`): which region a postal prefix belongs to; the highest `assignmentVersion` wins (a stale write is a no-op).
  - origins (`group_origins`): `FulfillmentLocked` names the fulfilling node (the ORIGIN_NODE fallback); `FulfillmentReleasedToNode` carries the per-item `ownerRef`s. Both merge into one row per group; a nil owners map persists as `{}`.
- **Outcome facts** (`return_outcomes`): the raw graded outcomes with per-line `unexpected` flags — the audit trail; nothing re-decides an authorization from them.

### Order tolerance

The two order-group events have no ordering guarantee: a notice may land before its placed event. `OnOrderPlaced` fills the lines, prefix and witness but never demotes a status a notice already set; `OnNoticeDecided` for an unknown group creates the row with the fact's status. Redeliveries are swallowed by the inbox; `SavePrefix` ignores stale assignment versions.

### What this context NEVER does

No REST, MCP or A2A call into a sibling (the label route of ADR 0005 excepted), no reading of the producer's database, no dual-write of a sibling's fact, and no PII: the prefix is kept, the ship-to never arrives here.

## Consequences

- The authorize use case reads only local tables; its latency is one Postgres round trip per read model, and a lagging view answers `group-not-shipped` (or `no-return-destination`) instead of blocking — the shopper retries with the same `Idempotency-Key`.
- `apis/asyncapi.yaml` documents every consumed type with the fields relied on; a producer that renames a field breaks the tolerant decode at this context's own pace.
