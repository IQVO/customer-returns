# 4. CloudEvents envelope and type catalogue

## Status

Accepted (2026-11-14).

## Context

The network mandates CloudEvents 1.0 in structured content mode for every Kafka message produced or consumed; the fleet's fitness tests fail the build on any flat envelope or envelope toggle. This ADR fixes this context's namespace, subjects, schemas and the four published types.

## Decision

### Envelope (built and validated ONLY by `internal/adapters/kafka/cloudevents`)

- `specversion` `1.0`, `source` `/retailnetwork/customer-returns`, `type` `com.retailnetwork.commerce.customer-returns.<entity>.<EventName>` with the single entity `returnauthorization`, `subject` = the aggregate root id (the `authorizationId`), `datacontenttype` `application/json`, `dataschema` `urn:retailnetwork:customer-returns:<stream>:<EventName>:v<version>`.
- Kafka message key = the subject (one authorization's events stay ordered on one partition), `kafkago.Hash{}` balancer, `RequiredAcks: RequireAll`, header `content-type` = `application/cloudevents+json; charset=UTF-8`.
- The only producing caller is the outbox encoder (`internal/adapters/outbound/kafka/encoder.go`); transport is `segmentio/kafka-go`; no hand-rolled CloudEvent structs.
- The `dataschema` version is the aggregate version AFTER the change (v1 for ReturnAuthorized, ≥ v2 for later events of the same authorization).

### The four types (all on `retailnetwork.customer-returns.events`, stream `events`)

| Type | Raised by | Payload (camelCase, optional omitted) |
| --- | --- | --- |
| `returnauthorization.ReturnAuthorized` | `AuthorizeReturn` (every disposition, KEEP included) | `authorizationId, orderId, groupNo, reason, disposition, method, lines[]{lineNo, catalogItemId, quantity, ownerRef?}, destinationNodeId?, originNodeId?, pickupWindowStart/End?, expiresAt, authorizedAt` |
| `returnauthorization.ReturnLabelIssued` | `IssueLabel`, once per package | `authorizationId, trackingNumber, destinationNodeId, issuedAt` |
| `returnauthorization.ReturnCompleted` | the first fitting graded outcome | `authorizationId, outcome (RECEIVED), nodeId, lines[]{catalogItemId, ownerRef?, qtyRestockable, qtyUnsellable}, receivedAt` — authorized lines only |
| `returnauthorization.ReturnCancelled` | `CancelReturn`, lazy expiry | `authorizationId, reason (SHOPPER_REQUESTED \| EXPIRED), cancelledAt` |

### Omission semantics

Every optional field absent = not applicable, never a default value (KEEP omits `destinationNodeId`; DROP_OFF omits the window; an unknown owner omits `ownerRef`). No event is retracted. Families are idempotent per CloudEvents `id` (the consumers' inbox). No published event carries a unit price, an address, a name, a phone number or free text.

### The outbox

The aggregate row and the already-encoded events are written in ONE transaction (`outbox_events`, migration `0002`); the always-on relay drains them at-least-once with the same `id` minted at append time, bounded by `OUTBOX_MAX_ATTEMPTS` (ADR 0008), traceparent stamped from the writing span.

## Consequences

- Consumers route on the full `type` and dedupe on `id`; vendor-connectivity builds its return advices from the first three types and offer-availability restocks from `ReturnCompleted`.
- The goldens in `internal/adapters/outbound/kafka/testdata/` pin each payload (authorized, KEEP and pickup variants); `apis/asyncapi.yaml` is the published contract.
