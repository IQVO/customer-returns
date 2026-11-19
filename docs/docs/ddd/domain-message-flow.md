---
id: domain-message-flow
title: Domain Message Flow
sidebar_label: Domain Message Flow
sidebar_position: 5
description: "Domain Message Flow diagrams for Customer Returns: a physical return end to end, the returnless KEEP path, and cancellation with lazy expiry."
---

# Domain Message Flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling). Every
arrow is a message of this context, prefixed `cmd:`, `evt:` or `qry:`. Quantities and identifiers in the notes are
illustrative. Full event types are on [Domain events](/docs/ddd/domain-events); the runtime view with errors is on
[Sequence diagrams](/docs/ddd/sequence-diagrams). The vendor-side steps (the returns centre working the advice and
submitting through the Vendor API) belong to vendor-connectivity and are shown only as far as they reach this context.

## 1. A physical return, authorize to restock

### 1a. Authorize

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as shopper
    participant CO as customer-<br/>ordering
    participant NT as node-<br/>topology
    participant OS as order-<br/>sourcing
    participant CR as customer-returns
    CO->>CR: evt: CustomerOrderPlaced,<br/>ShopperNoticeDecided GROUP_SHIPPED
    NT->>CR: evt: NodeActivated RETURNS_CENTER,<br/>ServiceAreaAssigned
    OS->>CR: evt: FulfillmentLocked,<br/>FulfillmentReleasedToNode
    S->>CR: cmd: AuthorizeReturn<br/>2 lines, DEFECTIVE
    Note over CR: Decide: total above the<br/>returnless threshold
    Note over CR: NEAREST_RETURNS_CENTER,<br/>destination rc-central-4
    CR-->>S: 201 authorization
    CR->>CR: evt: ReturnAuthorized
```

Source: `internal/adapters/inbound/kafka/consumers.go` (the read models), `internal/application/usecases/authorize.go`
(`AuthorizeReturn`, `resolveLines`, `destinations`), `internal/domain/returnauthorization/decide.go`.

### 1b. Label, grade and restock

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as shopper
    participant CR as customer-returns
    participant ST as shipping-and-<br/>tracking
    participant VC as vendor-<br/>connectivity
    participant OA as offer-<br/>availability
    S->>CR: cmd: IssueLabel package 1
    CR->>ST: POST /return-labels<br/>Idempotency-Key ret-x:1
    ST-->>CR: trackingNumber
    Note over CR: LinkLabel<br/>ReturnLabelIssued
    CR-->>S: 200 tracking number
    CR->>VC: evt: ReturnAuthorized,<br/>ReturnLabelIssued
    Note over VC: the return advice<br/>served on the Vendor API
    VC->>CR: evt: VendorReturnOutcomeSubmitted<br/>graded 1 restockable, 2 unsellable
    Note over CR: ApplyOutcome<br/>RECEIVED
    CR->>OA: evt: ReturnCompleted<br/>qtyRestockable per item
    CR->>VC: evt: ReturnCompleted
```

Source: `internal/application/usecases/{labels,outcome}.go`, `internal/adapters/outbound/labels`,
`internal/adapters/outbound/kafka/encoder.go` (topic routing).

## 2. The returnless KEEP

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as shopper
    participant CR as customer-returns
    participant VC as vendor-<br/>connectivity
    S->>CR: cmd: AuthorizeReturn<br/>1 line at 900 cents
    Note over CR: Decide: total within<br/>ReturnlessThresholdCents
    Note over CR: KEEP: CLOSED_KEEP at once,<br/>no destination, no label
    CR-->>S: 201 authorization CLOSED_KEEP
    CR->>VC: evt: ReturnAuthorized<br/>disposition KEEP
    S->>CR: cmd: IssueLabel
    CR-->>S: 409 illegal-transition<br/>KEEP links no label
```

Source: `internal/domain/returnauthorization/decide.go` (`keepEligible`), `commands.go` (`LinkLabel` on KEEP). The
missing-price case is the same flow refused earlier: `pricesAbsent` makes KEEP impossible and the fallback dispositions
apply.

## 3. Cancellation and lazy expiry

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as shopper
    participant CR as customer-returns
    participant VC as vendor-<br/>connectivity
    S->>CR: cmd: CancelReturn<br/>nothing in transit yet
    Note over CR: Cancel SHOPPER_REQUESTED<br/>CANCELLED
    CR-->>S: 200 authorization CANCELLED
    CR->>VC: evt: ReturnCancelled
    Note over CR: 31 days later, the next touch:<br/>expireLazily cancels with EXPIRED<br/>and answers authorization-expired
```

Source: `internal/application/usecases/queries.go` (`CancelReturn`, `expireLazily`), `labels.go` (`expireAndReject`).
There is no sweeper: expiry is checked on every read, label and outcome touch
([ADR 0002](/docs/adr/0002-aggregates-and-invariants) A4).
