---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 1
description: "Every application use case of customer-returns: trigger, inputs, invariants checked, errors and the events raised."
---

# Use cases

One method of `usecases.Service` (`internal/application/usecases`) per command, query or consumed event. Each command
runs in one Postgres transaction (`atomically`): the aggregate change, its outbox rows and, for consumers, the
processed-event claim commit or roll back together. Nothing else leaves the context synchronously except the one
reverse-label call of [ADR 0005](/docs/adr/0005-the-reverse-label-route); every sibling fact is read from local tables
fed by Kafka ([Integration](/docs/ecosystem/integration)).

## Conventions

- **Errors** are `application/problem+json`; the status and slug of every error are in
  [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).
- **Idempotency-Key** is required on every `POST` (1 to 128 printable ASCII characters, global); a repeat with the same
  fingerprint replays the first answer (`Idempotency-Replayed: true`), the same key on a different method, path or body
  is `409 idempotency-key-reused` ([ADR 0010](/docs/adr/0010-fleet-rest-api-guideline)).
- **The group must be SHIPPED or DELIVERED** (`GroupStatus.Authorizable`), as customer-ordering's facts landed in
  `order_group_views`; otherwise `422 group-not-shipped` — the shopper retries with the same `Idempotency-Key`.
- **The disposition is decided here** by `Decide`, a pure function over the lines' caller-declared prices, the method
  and the read models' destinations; the caller never chooses it.
- **Expiry is checked first on every command touch** (label, outcome, cancel — `expireLazily`): past
  `authorizedAt + 30d` the authorization is cancelled with `EXPIRED` and the call answers `422 authorization-expired`.
  A pure read does not cancel: it reports the authorization's remaining state.
- Unit prices are the storefront's facts: optional per line, stored and echoed, never published; an absent price makes
  KEEP impossible and shows as `pricesAbsent`. Quantities are units of a catalog item.

Source: `internal/application/usecases/{authorize,labels,outcome,queries,consumers}.go`, domain rules in
`internal/domain/{returnauthorization,network,shared}`.

## Summary

| Use case | Trigger | Raises |
| --- | --- | --- |
| `AuthorizeReturn` | `POST /return-authorizations` | `ReturnAuthorized` |
| `IssueLabel` | `POST /return-authorizations/{authorizationId}/labels` | `ReturnLabelIssued` |
| `CancelReturn` | `POST /return-authorizations/{authorizationId}/cancellations` | `ReturnCancelled` |
| `GetReturnAuthorization` | `GET /return-authorizations/{authorizationId}`; MCP `get_return_authorization`; the A2A skill | none |
| `ListReturnAuthorizations` | `GET /return-authorizations?orderId=` (keyset-paged); MCP `list_return_authorizations` | none |
| `OnOutcomeSubmitted` | `VendorReturnOutcomeSubmitted` on Kafka | `ReturnCompleted` (first fitting outcome) |
| `OnOrderPlaced`, `OnNoticeDecided` | customer-ordering events | none (updates `order_group_views`) |
| `OnNodeFact`, `OnPrefixFact` | node-topology events | none (updates `returns_network_nodes`, `service_area_prefixes`) |
| `OnOriginFact` | order-sourcing events | none (updates `group_origins`) |

## Return commands

### AuthorizeReturn

- **Inputs:** `orderId`, `groupNo`, `lines` (1 to 50, each picking one `lineNo` of the placed group with its declared
  `unitPriceCents`), `reason` (one of the five codes), optional `method` (`DROP_OFF` is the default; `PICKUP` requires
  `pickupWindowStart`/`pickupWindowEnd` inside the authorization's validity).
- **Invariants:** the group exists and is authorizable (`group-not-shipped`); every `lineNo` resolves against the placed
  group (`validation-failed` on an unknown line); `Decide` finds a disposition (`no-return-destination` otherwise);
  quantity at least 1 per line.
- **Result:** the authorization (`ret-<uuid>`, v1) with its decided disposition, `expiresAt = authorizedAt + 30d`; a
  KEEP closes at once (`CLOSED_KEEP`). `ReturnAuthorized` appended to the outbox on every disposition, KEEP included.

### IssueLabel

- **Inputs:** `authorizationId`, `packageNo` (1 to 5).
- **Invariants:** expiry checked first; the authorization is open and its disposition is not KEEP (`illegal-transition`
  otherwise); the tracking number is new (a duplicate is not re-linked) and at most five are linked.
- **Effect:** calls the shipping context's `POST /return-labels` with `Idempotency-Key: <authorizationId>:<packageNo>`
  OUTSIDE the unit of work; then ONE transaction re-reads the aggregate, links the tracking number and appends
  `ReturnLabelIssued`. With `SHIPPING_LABELS_URL` unset — or any transport failure, non-200 or empty answer — the call
  answers `422 label-unavailable` and nothing else happens.

### CancelReturn

- **Invariants:** the authorization exists (`not-found`) and is still `AUTHORIZED` (`illegal-transition` once anything
  is in transit). The reason is `SHOPPER_REQUESTED`; the lazy expiry is the only other cancellation cause in v1.
- **Events:** `ReturnCancelled`.

## Consumed events

### OnOutcomeSubmitted (policy FirstFittingOutcome)

- **Trigger:** `VendorReturnOutcomeSubmitted` from vendor-connectivity — the returns centre's graded outcome, per item
  `qtyRestockable` and `qtyUnsellable`.
- **Invariants:** the outcome's per-item quantities never exceed the authorized quantity, and each item appears once;
  the authorization is `AUTHORIZED` or `IN_TRANSIT` (`ApplyOutcome`).
- **Effect:** the first outcome that fits an open authorization completes it (`RECEIVED`, `ReturnCompleted` with the
  authorized lines only — the restock fact offer-availability consumes). Outcomes for unknown or closed authorizations
  and later duplicates are recorded on `return_outcomes` and acknowledged — never a consumer error. Lines beyond the
  authorization are recorded flagged `unexpected`, never published, never restocked (ADR 0002 A6 and A7).
- **Errors:** none are returned to a caller. A transient failure is retried with capped backoff; a payload that can
  never apply is dead-lettered ([Troubleshooting](/docs/operations/troubleshooting)).

### The read-model consumers

`OnOrderPlaced` and `OnNoticeDecided` fold customer-ordering's facts into `order_group_views` (the placed lines, the
destination prefix and witness; a terminal notice sets the status — a fact's status wins over the family). `OnNodeFact`
and `OnPrefixFact` fold node-topology's facts into `returns_network_nodes` (only ACTIVE `RETURNS_CENTER`s are `Decide`
candidates) and `service_area_prefixes` (the highest `assignmentVersion` wins; a stale write is a no-op). `OnOriginFact`
folds order-sourcing's facts into `group_origins`: the lock names the fulfilling node, the release carries the per-item
`ownerRef`s. They tolerate duplicates and out-of-order delivery — a notice before its placed event creates the row with
the fact's status — and each is claimed in `processed_events` by the CloudEvents id.

## Queries

| Query | Reads | Notes |
| --- | --- | --- |
| `GetReturnAuthorization` | `return_authorizations` | `not-found` for an unknown id. The full standing: state, disposition, destination, tracking numbers, `pricesAbsent`, expiry. |
| `ListReturnAuthorizations` | `return_authorizations` | One order's authorizations, keyset-paged (`orderId`, `cursor`, `limit`), newest first. |

Both are pure reads: they raise nothing and never cancel an expired authorization — only a command's touch does
(`GetReturnAuthorization` reports an authorization past its `expiresAt` with its remaining state). They are the same two
use cases the REST handlers, the MCP tools and the A2A skill call
([ADR 0006](/docs/adr/0006-read-only-mcp-server)).
