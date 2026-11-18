---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/outbox/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

customer-returns PUBLISHES four events on ONE topic and CONSUMES ten from four producers
(ADR 0003, ADR 0004). Published: `retailnetwork.customer-returns.events` only. Consumed:
`retailnetwork.customer-ordering.events`, `retailnetwork.node-topology.events`,
`retailnetwork.order-sourcing.events`, `retailnetwork.vendor-connectivity.events`.
Never produce or consume a `warehouse.*` topic. The ONE synchronous seam is the shipping
context's reverse-label HTTP route (ADR 0005); everything else is an event.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes is a CloudEvents 1.0 event in structured
content mode. This is a hard rule, not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write, no dual-read, no
  envelope toggle env var. `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build and validate the envelope with `github.com/cloudevents/sdk-go/v2/event` through ONE helper
  package, `internal/adapters/kafka/cloudevents`. Transport stays `segmentio/kafka-go`; no
  hand-rolled CloudEvent structs. The only producing caller is the encoder
  `internal/adapters/outbound/kafka/encoder.go`.
- Every message carries the Kafka header `content-type: application/cloudevents+json; charset=UTF-8`.
  Message key = aggregate id (the `authorizationId`), `kafkago.Hash{}` balancer
  (`internal/adapters/outbound/kafka/writer.go`).
- Attributes: `specversion=1.0`; `id` (UUID minted ONCE per event at outbox append and stored in
  `outbox_events.event_id`, so a redelivery carries the same id); `source=/retailnetwork/customer-returns`;
  `type`; `subject` (the aggregate id, never empty); `time` (the domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:customer-returns:<stream>:<EventName>:v<aggregate version>`.
  No custom extension attributes without an ADR.
- `type` = `com.retailnetwork.commerce.customer-returns.<entity>.<EventName>` with the single
  entity `returnauthorization` (lowercase, no separators). A breaking payload change is a new
  `:v2` dataschema, never a mutation of v1; evolution is additive only.
- Payloads follow the wire conventions of contracts-delta: ids are strings, instants RFC 3339 UTC,
  enums UPPER_SNAKE, quantities are eaches, optional fields omitted, arrays always present.
  NO payload ever carries a unit price, an address, a name, a phone or free text (PII rule;
  `internal/architecture/pii_fitness_test.go`).
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the FULL `type` string,
  ignore unknown types (silently committed past) and unknown fields, dedupe on `id` in the
  `processed_events` inbox, and WARN+skip anything that is not a valid CloudEvent without
  claiming the inbox; they never fall back to a legacy shape.
- Delivery is at-least-once with an ATOMIC effect (inbox claim + projection commit in ONE unit
  of work), offsets committed only after success, capped backoff for transient failures, DLQ
  `<topic>.dlq` with `x-dlq-*` headers for poison payloads.
- Tests: a golden exact-JSON test per published type
  (`internal/adapters/outbound/kafka/encoder_test.go` + `testdata/`), the catalogue-vs-contract
  sensor (`TestEventCatalogueMatchesContract`), consumer tests in
  `internal/adapters/inbound/kafka/consumers_test.go`, and the Kafka proof against a real broker
  (`internal/adapters/outbound/outbox/relay_integration_test.go`,
  `cmd/api/e2e_integration_test.go`), all via testcontainers.

### Published types

`subject` and Kafka key is the aggregate root id (`authorizationId`).

| `type` | stream | `subject` | raised by |
| --- | --- | --- | --- |
| `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnAuthorized` | events | authorizationId | `AuthorizeReturn` (KEEP included) |
| `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnLabelIssued` | events | authorizationId | `IssueLabel`, once per package |
| `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnCompleted` | events | authorizationId | the first fitting graded outcome |
| `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnCancelled` | events | authorizationId | `CancelReturn` / lazy expiry |

### Consumed types

Constants and tolerant payload structs: `internal/adapters/inbound/kafka/consumers*.go`. A consumed
payload is read as its producer publishes it: every consumed type has a test that feeds the producer's
own AsyncAPI example through the real handler (`producer_examples_test.go`, ADR 0003 addendum). Each
consumer's group comes from its mandatory variable (`ORDER_CONSUMER_GROUP`,
`TOPOLOGY_CONSUMER_GROUP`, `SOURCING_CONSUMER_GROUP`, `VENDOR_CONSUMER_GROUP`).

| `type` | topic | handler (use case) |
| --- | --- | --- |
| `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced` | customer-ordering.events | `OnOrderPlaced` |
| `com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided` (terminal families) | customer-ordering.events | `OnNoticeDecided` |
| `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | node-topology.events | `OnNodeRegistered` |
| `com.retailnetwork.logistics.node-topology.node.NodeActivated`, `NodeSuspended`, `NodeRetired` (the node id and an instant only) | node-topology.events | `OnNodeStatus` |
| `com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned` | node-topology.events | `OnPrefixFact` |
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode`, `FulfillmentLocked` | order-sourcing.events | `OnOriginFact` |
| `com.retailnetwork.partner.vendor-connectivity.returnoutcome.VendorReturnOutcomeSubmitted` | vendor-connectivity.events | `OnOutcomeSubmitted` |
