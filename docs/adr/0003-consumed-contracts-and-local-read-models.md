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

## Addendum 2026-11-17: the consumed facts as their producers publish them

The Decision above names the consumed types; it does not pin the payload of each one, and three were read in a shape their producer never publishes. Nothing of the Decision is withdrawn: this addendum records what each producer sends (its outbound encoder and its `apis/asyncapi.yaml` on `origin/develop`) and what this context does with it. The rule it adds: **a consumed payload is read as the producer publishes it, the handler requires only what the producer always sends, and every consumed type has a test that feeds the producer's own example through the real handler** (`internal/adapters/inbound/kafka/producer_examples_test.go`, `producer_contract_test.go`).

### Node facts: the registration describes, the status facts only move the lifecycle

node-topology publishes the node's descriptive attributes on `NodeRegistered` only. `NodeActivated` is `{nodeId, activatedAt}`, `NodeSuspended` is `{nodeId, reason, suspendedAt}`, `NodeRetired` is `{nodeId, retiredAt}`. The first cut read `nodeType` and `regionId` from all four and refused a node fact without `nodeType` as a malformed payload, so every real status fact was dead-lettered at once (`x-dlq-error: poison message: malformed event payload`) and no node ever left PENDING: `decide` could never choose NEAREST_RETURNS_CENTER. The node half of the ReturnsNetworkView now keeps the two kinds of fact apart, in the use case and in both repositories (one contract suite, `internal/testing/repocontract`, run against the memory double and Postgres):

- `NodeRegistered` (`OnNodeRegistered`, `RegisterNode`) upserts the type and the region. A node never seen starts PENDING. A node already held keeps its lifecycle: a registration applied again never demotes an ACTIVE, SUSPENDED or RETIRED node, which is what makes a rebuild of the view safe.
- The three status facts (`OnNodeStatus`, `SetNodeLifecycle`) read the node id from `data` and the instant from the CloudEvents `time`, the same attribute on all three. They move `lifecycle` and `lifecycle_at` and never touch what the registration wrote.
- **A stale status fact is a no-op.** A fact older than the one that set the current lifecycle changes nothing (`lifecycle_at`, migration `0004`). The precedent is the prefix half (`SavePrefix` ignores a stale `assignmentVersion`). It is what lets a dead-lettered `NodeActivated` be replayed after a newer `NodeSuspended` without undoing it.
- **A status fact for a node this view never saw keeps a lifecycle-only row** (empty type and region) that the registration completes without demoting it. node facts are keyed by `nodeId`, so the registration precedes its status facts on the partition and this is the rare case; it is still a well-formed fact and never a dead letter. This is the Order tolerance of the Decision applied to nodes: `OnNoticeDecided` for an unknown group creates the row with the fact's status and `OnOrderPlaced` fills it without demoting it. A lifecycle-only row is never a `decide` candidate (the lookup asks for type `RETURNS_CENTER`).
- **The node type is stored as published.** Migration `0004` drops the `CHECK` that enumerated node-topology's seven node types: the enumeration is the producer's, only `RETURNS_CENTER` influences a disposition here, and the list turned a node type the producer adds into a dead letter.

### The destination postal prefix is the order's

customer-ordering publishes `destinationPostalPrefix` once, at the top of `CustomerOrderPlaced`'s `data`; a group carries none. The first cut read it from each group, so every row of the OrderGroupView had an empty prefix, no region was ever found and NEAREST_RETURNS_CENTER was again unreachable. `OnOrderPlaced` writes the order's prefix on every group row of the order.

### A merged release names every group it covers

order-sourcing's `FulfillmentReleasedToNode` of a merged release (its ADR 0015) keeps the carrier group in `orderId` and `groupNo`, lists every merged group in `members` and names the member of each line in `lines[].assignmentId`. The first cut wrote one origin, for the carrier. `OnOriginFact` now writes one origin per member, each with the owners of its own lines, under the one inbox claim of the event. Each member still raises its own `FulfillmentLocked`, which names the fulfilling node as before.

### What was already dead-lettered

The inbox claim of an event id is made inside the unit of work that applies the fact (`usecases.consume`); a payload the handler refuses never reaches it. A dead-lettered fact was therefore never claimed in `processed_events`, and delivering it again once the handler can read it applies it (`TestADeadLetteredFactWasNeverClaimedAndAppliesWhenRedelivered`). The operator procedure is in the runbook (Replaying the node-topology dead letters).

## Addendum 2026-11-18: the producers' examples are read from pinned specs

The addendum above made every consumed type pass through the real handler from its producer's own example, and kept those examples as a copy pasted into `producer_examples_test.go`. A pasted copy drifts the day the producer changes. That file is gone: each producer's `apis/asyncapi.yaml` is pinned as a byte copy under `contracts/` (registered in `contracts/pinned.json`), the CI job `pinned-contracts` fails when a pin differs from its producer's `develop`, and the tests read the examples from the pins at test time (`pinned_examples_test.go`, the archetype tests of `contract_events_test.go` and `contract_envelope_test.go`, the scenarios of `producer_contract_test.go`). The label route of ADR 0005 is pinned the same way. Nothing of the Decision or of the first addendum changes; what is pinned, where and how a pin is refreshed is in `docs/docs/development/contract-testing.md`.
