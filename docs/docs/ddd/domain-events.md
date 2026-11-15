---
id: domain-events
title: Domain Events
sidebar_label: Domain Events
sidebar_position: 7
description: "The four events Customer Returns publishes and the ten types it consumes, with payloads, an example envelope and idempotency rules."
---

# Domain Events

Every event this context publishes and consumes. The contract is `apis/asyncapi.yaml`; the payload structs the service
serialises are in `internal/adapters/outbound/kafka/encoder.go`. The envelope, topic routing and consumed-event effects
are on the [Event catalogue](/docs/api-reference/events); the decision record is
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue). Quantities are units of a catalog item. All
timestamps are RFC 3339 UTC strings with nanosecond precision where non-zero, and every optional field is omitted when
it does not apply — never defaulted.

## Published payloads

| Event | Fields (JSON, from `encoder.go`) |
| --- | --- |
| `ReturnAuthorized` | `authorizationId`, `orderId`, `groupNo`, `reason`, `disposition`, `method`, `lines` (`lineNo`, `catalogItemId`, `quantity`, `ownerRef` omitted when unknown), `destinationNodeId` (omitted for KEEP), `originNodeId` (omitted until a lock fact), `pickupWindowStart`/`pickupWindowEnd` (omitted unless PICKUP), `expiresAt`, `authorizedAt` |
| `ReturnLabelIssued` | `authorizationId`, `trackingNumber`, `destinationNodeId`, `issuedAt` — once per package |
| `ReturnCompleted` | `authorizationId`, `outcome` (`RECEIVED`), `nodeId`, `lines` (`catalogItemId`, `ownerRef` omitted when unknown, `qtyRestockable`, `qtyUnsellable`), `receivedAt` — the authorized lines only |
| `ReturnCancelled` | `authorizationId`, `reason` (`SHOPPER_REQUESTED` or `EXPIRED`), `cancelledAt` |

Value sets:

- `reason`: `DEFECTIVE`, `NOT_AS_DESCRIBED`, `WRONG_ITEM`, `NOT_NEEDED`, `OTHER`
  (`internal/domain/returnauthorization/authorization.go`).
- `disposition`: `KEEP`, `NEAREST_RETURNS_CENTER`, `ORIGIN_NODE`; `method`: `DROP_OFF`, `PICKUP`.
- `outcome`: `RECEIVED` in v1 (the one graded outcome that completes an authorization).

No published event carries a unit price (the no-money rule), an address, a name, a phone number or free text; the
`dataschema` version is the aggregate version after the change (v1 for `ReturnAuthorized`, v2 or later for the events of
the same authorization).

Nothing is published on a read, and `MarkInTransit` raises no event: transit is a local fact until the returns centre's
outcome arrives ([ADR 0002](/docs/adr/0002-aggregates-and-invariants)).

## Example envelope

`ReturnAuthorized` for a DROP_OFF return to the region's returns centre. The values below are illustrative, not
captured from a running service.

```json
{
  "specversion": "1.0",
  "id": "3f1a7c52-9d0e-4b6a-8c21-5e7f0a9b1c33",
  "source": "/retailnetwork/customer-returns",
  "type": "com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnAuthorized",
  "subject": "ret-0f3a91e2-7c4b-4a1e-9d2f-6b8e5a3c1d90",
  "time": "2026-11-14T13:10:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:customer-returns:events:ReturnAuthorized:v1",
  "data": {
    "authorizationId": "ret-0f3a91e2-7c4b-4a1e-9d2f-6b8e5a3c1d90",
    "orderId": "ord-51a0c9",
    "groupNo": 2,
    "reason": "DEFECTIVE",
    "disposition": "NEAREST_RETURNS_CENTER",
    "method": "DROP_OFF",
    "lines": [
      {"lineNo": 1, "catalogItemId": "ci-1001", "quantity": 1, "ownerRef": "own-77"},
      {"lineNo": 2, "catalogItemId": "ci-2049", "quantity": 2}
    ],
    "destinationNodeId": "rc-central-4",
    "originNodeId": "fc-east-2",
    "expiresAt": "2026-12-14T13:10:00Z",
    "authorizedAt": "2026-11-14T13:10:00Z"
  }
}
```

The ids in the example are shaped for readability; the service mints `authorizationId` as `ret-<uuid>`
(`ids.UUID` with the `ret` prefix). A KEEP payload omits `destinationNodeId`; a PICKUP payload adds the two window
fields; a group with no lock fact yet omits `originNodeId`.

## Consumed

Ten event types from four producers, one consumer per topic (`ORDER_CONSUMER_GROUP`, `TOPOLOGY_CONSUMER_GROUP`,
`SOURCING_CONSUMER_GROUP`, `VENDOR_CONSUMER_GROUP`), dispatch on the full `type`, effects idempotent on the CloudEvents
`id` through `processed_events` in the same transaction. The table of types and effects is on the
[Event catalogue](/docs/api-reference/events#consumed). Failure handling: a transient failure is retried with capped
backoff and then dead-lettered to `<topic>.dlq`; a payload that can never apply is dead-lettered at once
([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-models), [Troubleshooting](/docs/operations/troubleshooting)).
The two order-group events have no ordering guarantee: a notice may land before its placed event, and the projections
tolerate it. A type this context does not act on is silently committed past.
