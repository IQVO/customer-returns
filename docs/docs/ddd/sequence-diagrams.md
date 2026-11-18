---
id: sequence-diagrams
title: Sequence Diagrams
sidebar_label: Sequence Diagrams
sidebar_position: 8
description: "One sequence per key Customer Returns use case, from inbound adapter through aggregate, repository and outbox."
---

# Sequence Diagrams

Shared conventions, from the code:

- Every command use case runs inside `Service.atomically`, one Postgres transaction (`UnitOfWork`). The repository
  saves with an optimistic `version`; a lost race is `concurrent-update` (409).
- Outbox rows are written in the same transaction as the aggregate (`EventOutbox.Append`); the in-process relay
  publishes them later to `retailnetwork.customer-returns.events`.
- Write routes pass the HTTP idempotency middleware first (`Idempotency-Key`, global, `409` on reuse); it is omitted
  from the diagrams.
- Consumers claim the CloudEvents `id` in `processed_events` in the same transaction as their effect.
- Every command touch re-checks expiry first (`expireLazily`): past `expiresAt` the authorization is cancelled with
  `EXPIRED` and the call answers `authorization-expired`; a pure read reports the remaining state without cancelling.
- Event names are short; full types are on the [Event catalogue](/docs/api-reference/events).

## 1. AuthorizeReturn

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http POST authorizations
    participant UC as AuthorizeReturn
    participant V as read models
    participant D as Decide
    participant A as ReturnAuthorization
    participant R as AuthRepo
    participant OB as Outbox
    H->>UC: AuthorizeReturn(orderId, groupNo, lines, reason)
    UC->>V: Groups.Group(orderId, groupNo)
    alt unknown or not SHIPPED or DELIVERED
        UC-->>H: 422 group-not-shipped
    else authorizable
        UC->>V: resolveLines against the placed group
        UC->>V: destinations (returns centre, origin)
        UC->>D: Decide(lines with prices, method)
        alt neither destination and KEEP does not apply
            D-->>UC: ErrNoReturnDestination
            UC-->>H: 422 no-return-destination
        else decided
            UC->>A: New(Keep or physical)
            A-->>UC: AUTHORIZED or CLOSED_KEEP
            UC->>R: Save
            UC->>OB: Append(ReturnAuthorized)
            UC-->>H: 201 authorization
        end
    end
```

Source: `internal/application/usecases/authorize.go`, `internal/domain/returnauthorization/decide.go`. The disposition is
decided here, never chosen by the caller; KEEP closes the aggregate at once.

## 2. IssueLabel

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http POST labels
    participant UC as IssueLabel
    participant N as NetworkView
    participant L as LabelIssuer
    participant A as ReturnAuthorization
    participant R as AuthRepo
    participant OB as Outbox
    H->>UC: IssueLabel(authorizationId, packageNo)
    UC->>A: Get, expiry checked
    UC->>A: open and not KEEP?
    UC->>N: Node(DestinationNodeID)
    alt node unknown or no label prefix
        N-->>UC: not found, or LabelPostalPrefix empty
        UC-->>H: 422 label-unavailable, the route is not called
    end
    UC->>L: POST /return-labels with the node's label prefix, outside the unit of work
    alt route off or non-201
        L-->>UC: ErrLabelUnavailable
        UC-->>H: 422 label-unavailable, nothing else happens
    else 201 with tracking number
        UC->>A: LinkLabel(trackingNumber)
        alt already linked or 5 linked
            A-->>UC: Illegal or Invalid
            UC-->>H: 409 or 422
        else linked
            UC->>R: Save(expected version), ONE transaction
            UC->>OB: Append(ReturnLabelIssued)
            UC-->>H: 200 authorization
        end
    end
```

Source: `internal/application/usecases/labels.go`, `internal/adapters/outbound/labels`
([ADR 0005](/docs/adr/0005-the-reverse-label-route)). The destination prefix sent is the destination node's
(`returns_network_nodes.label_postal_prefix`), never the order group's. The client sends
`Idempotency-Key: <authorizationId>:<packageNo>`; a retry is a replay, and a failure between the call and the commit is
healed by the shipping context's dedupe.

## 3. OnOutcomeSubmitted (consumer)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant K as Kafka
    participant C as vendor consumer
    participant IB as processed_events
    participant UC as OnOutcomeSubmitted
    participant A as ReturnAuthorization
    participant F as Outcome facts
    participant OB as Outbox
    K->>C: VendorReturnOutcomeSubmitted
    C->>IB: claim event id
    alt already processed
        IB-->>C: duplicate
        C-->>K: commit and skip
    else new
        C->>UC: Execute(outcome)
        alt unknown or closed authorization
            UC->>F: record, unexpected lines flagged
            C-->>K: commit, acknowledged
        else open and fits
            UC->>A: ApplyOutcome(lines, nodeId)
            A-->>UC: RECEIVED and ReturnCompleted
            UC->>F: record the fact
            UC->>OB: Append(ReturnCompleted)
            C-->>K: commit after the transaction
        end
    end
```

Source: `internal/adapters/inbound/kafka/consumer.go`, `internal/application/usecases/outcome.go`. The first outcome
that fits completes the authorization (ADR 0002 A6); lines beyond the authorization are recorded `unexpected` and never
published (A7). The same inbox pattern runs in the three read-model consumers.

## 4. CancelReturn and the lazy expiry

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http POST cancellations
    participant UC as CancelReturn
    participant A as ReturnAuthorization
    participant R as AuthRepo
    participant OB as Outbox
    H->>UC: CancelReturn(authorizationId)
    UC->>A: Get, expiry checked first
    alt expired on this touch
        UC->>OB: Append(ReturnCancelled EXPIRED)
        UC-->>H: 422 authorization-expired
    else still AUTHORIZED
        UC->>A: Cancel(SHOPPER_REQUESTED)
        A-->>UC: CANCELLED and ReturnCancelled
        UC->>R: Save(expected version)
        UC->>OB: Append(event)
        UC-->>H: 200 authorization
    else IN_TRANSIT or later
        UC-->>H: 409 illegal-transition
    end
```

Source: `internal/application/usecases/queries.go` (`CancelReturn`, `expireLazily`). There is no sweeper: the lazy
expiry is the only other cancellation cause in v1 (ADR 0002 A4).
