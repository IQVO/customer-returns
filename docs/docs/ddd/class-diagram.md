---
id: class-diagram
title: Class Diagram
sidebar_label: Class Diagram
sidebar_position: 9
description: "UML class diagrams of the Customer Returns domain types and its hexagonal layout, with the as-built package names."
---

# Class Diagram

UML class diagrams of the domain model. Fields are unexported (`-`), methods exported (`+`); only methods that exist in
the code are listed. The packages are `internal/domain/returnauthorization` (the aggregate, its value objects and
`Decide`), `internal/domain/network` (the read-model value types) and `internal/domain/shared` (the event envelope and
the typed errors). The terms are mapped on [Ubiquitous language](/docs/ddd/ubiquitous-language).

## 1. ReturnAuthorization aggregate

```mermaid
classDiagram
    direction TB
    class ReturnAuthorization {
        <<AggregateRoot>>
        -id ID
        -orderID string
        -groupNo int
        -lines Line[]
        -reason Reason
        -disposition Disposition
        -method Method
        -destinationNodeID string
        -originNodeID string
        -pickupStart Time
        -pickupEnd Time
        -trackingNumbers string[]
        -state State
        -authorizedAt Time
        -expiresAt Time
        -closedAt Time
        -version int64
        +LinkLabel(trackingNumber string, now Time) error
        +MarkInTransit() error
        +ApplyOutcome(lines OutcomeLine[], nodeID string, now Time) error
        +Cancel(reason CancelReason, now Time) error
        +MarkPickupFailed(now Time) error
        +PricesAbsent() bool
        +Snapshot() Snapshot
        +Events() Event[]
    }
    class Line {
        <<ValueObject>>
        +LineNo int
        +CatalogItemID string
        +Quantity int
        +UnitPriceCents *int64
        +OwnerRef string
    }
    class OutcomeLine {
        <<ValueObject>>
        +CatalogItemID string
        +OwnerRef string
        +QtyRestockable int
        +QtyUnsellable int
    }
    class Reason {
        <<Enumeration>>
        DEFECTIVE
        NOT_AS_DESCRIBED
        WRONG_ITEM
        NOT_NEEDED
        OTHER
    }
    class Disposition {
        <<Enumeration>>
        KEEP
        NEAREST_RETURNS_CENTER
        ORIGIN_NODE
    }
    class Method {
        <<Enumeration>>
        DROP_OFF
        PICKUP
    }
    ReturnAuthorization "1" *-- "1..50" Line
    ReturnAuthorization ..> OutcomeLine : graded by ApplyOutcome
    ReturnAuthorization ..> Reason
    ReturnAuthorization ..> Disposition
    ReturnAuthorization ..> Method
```

Source: `internal/domain/returnauthorization/{authorization,commands,events,invariants}.go`. `Restore(Snapshot)`
rebuilds the aggregate and re-validates it (`checkSnapshot`). `LinkLabel`, `ApplyOutcome` and `Cancel` raise
`ReturnLabelIssued`, `ReturnCompleted` and `ReturnCancelled`; `MarkInTransit` and `MarkPickupFailed` raise nothing in
v1. `ReturnAuthorized` is raised by the constructor on `AuthorizeReturn`.

## 2. Decide and the read-model types

```mermaid
classDiagram
    direction LR
    class Decide {
        <<Function>>
        +Decide(in DecideInput) Disposition, error
        -keepEligible(in DecideInput) bool
    }
    class DecideInput {
        <<ValueObject>>
        +Lines Line[]
        +Method Method
        +NearestNodeID string
        +OriginNodeID string
    }
    class OrderGroup {
        <<ValueObject>>
        +OrderID string
        +GroupNo int
        +Status GroupStatus
        +DestinationPostalPrefix string
        +Lines PlacedLine[]
    }
    class GroupStatus {
        <<Enumeration>>
        PROMISED
        LOCKED
        SHIPPED
        DELIVERED
        LOST
        RETURNED
        +Authorizable() bool
    }
    class Node {
        <<ValueObject>>
        +NodeID string
        +Type NodeType
        +RegionID string
        +Lifecycle NodeLifecycle
    }
    class PrefixAssignment {
        <<ValueObject>>
        +PostalPrefix string
        +RegionID string
        +AssignmentVersion int64
    }
    class GroupOrigin {
        <<ValueObject>>
        +NodeID string
        +OwnersByItem map
    }
    Decide ..> DecideInput
    DecideInput ..> Line
    DecideInput ..> Node : NearestNodeID
    DecideInput ..> GroupOrigin : OriginNodeID
    OrderGroup ..> GroupStatus
    GroupStatus ..> PrefixAssignment : region via prefix
```

Source: `internal/domain/returnauthorization/decide.go`, `internal/domain/network/network.go`. `Decide` is pure: it
takes only what the read models resolved, performs no I/O, so the authorize use case is one unit of work. The
`ReturnlessThresholdCents = 1500` constant lives beside it. `shared` holds `Event`, `ErrValidation` and
`ErrIllegalTransition`; the port errors (`ErrNotFound`, `ErrConcurrentUpdate`, `ErrGroupNotShipped`,
`ErrLabelUnavailable`) live in `internal/application/ports`.

## 3. Hexagonal layout

```mermaid
flowchart TB
    subgraph IN["inbound adapters"]
        HTTP["http returns API"]
        CONS["kafka consumers x4"]
        MCP["mcp and a2a reads"]
    end
    subgraph APP["application"]
        UC["use cases and ports"]
    end
    subgraph DOM["domain"]
        AGG["returnauthorization,<br/>network, shared"]
    end
    subgraph OUT["outbound adapters"]
        PG["postgres repos<br/>outbox, inbox"]
        RELAY["outbox relay to kafka"]
        LB["labels client, ids, clock"]
    end
    HTTP --> UC
    CONS --> UC
    MCP --> UC
    UC --> AGG
    UC --> PG
    UC --> LB
    PG --> RELAY
```

Source: `internal/adapters/{inbound,outbound}`, `internal/application`; the dependency rule is enforced by
`TestHexagonalArchitecture` (see [Testing](/docs/development/testing) and [Architecture](/docs/overview/architecture)).
The `labels` client is the ONLY outbound HTTP adapter ([ADR 0005](/docs/adr/0005-the-reverse-label-route)); the MCP and
A2A adapters wire the read use cases only, never a write seam ([ADR 0006](/docs/adr/0006-read-only-mcp-server)).
