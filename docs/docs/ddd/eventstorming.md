---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
sidebar_position: 6
description: "Process-level EventStorming boards for Customer Returns, from a shopper's authorization to a restocked listing, with hotspots."
---

# EventStorming

Process-level boards in the ddd-crew [EventStorming glossary & cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet)
notation. Every sticky is a command, aggregate, event, policy or read model that exists in the code; the hotspots are
the open questions of this repository's ADRs and of the network design. The network-wide board is
[EventStorming big picture](https://iqvo.github.io/warehouse-docs/network/strategic-design/eventstorming-big-picture).

| Sticky | Colour | Meaning here |
| --- | --- | --- |
| Command | blue `#a5d8ff` | `AuthorizeReturn`, `IssueLabel`, `CancelReturn`... |
| Aggregate | yellow `#ffec99` | `ReturnAuthorization` |
| Domain Event | orange `#ffc078` | an event published or consumed |
| Policy | lilac `#d0bfff` | a rule applied by a use case |
| Read Model | green `#b2f2bb` | `OrderGroupView`, `ReturnsNetworkView`, `group_origins` |
| External System | pink `#fcc2d7` | another context or an actor |
| Hotspot | red `#ff8787` | an open question |

## 1. Authorize

```mermaid
flowchart TB
    S["shopper"]:::external
    C1["AuthorizeReturn"]:::command
    R1["OrderGroupView"]:::readmodel
    R2["ReturnsNetworkView<br/>group origins"]:::readmodel
    P1["Policy: Decide"]:::policy
    G1["ReturnAuthorization"]:::aggregate
    E1["ReturnAuthorized"]:::event
    H1["Hotspot: view lags the<br/>shoppers retry"]:::hotspot
    S --> C1 --> G1 --> E1
    R1 --- C1
    R2 --- C1
    P1 --- G1
    C1 --- H1
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef readmodel fill:#b2f2bb,stroke:#2f9e44,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/authorize.go`. `Decide` is a pure function over the read models' destinations —
it performs no I/O, so the authorize use case is one unit of work. A group is authorizable only while its view says
SHIPPED or DELIVERED (`group-not-shipped`).

## 2. Label and transit

```mermaid
flowchart TB
    S["shopper"]:::external
    C2["IssueLabel"]:::command
    X1["shipping-and-tracking<br/>POST /return-labels"]:::external
    P1["Policy: LinkLabel"]:::policy
    G2["ReturnAuthorization"]:::aggregate
    E2["ReturnLabelIssued"]:::event
    H1["Hotspot: route off until<br/>SHIPPING_LABELS_URL"]:::hotspot
    S --> C2 --> G2 --> E2
    C2 --> X1
    P1 --- G2
    E2 --- H1
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/labels.go`, `internal/adapters/outbound/labels` (ADR 0005). While the route is
not configured the answer is the tolerant `422 label-unavailable` — no 500, no retry storm, no outbox row. `MarkInTransit`
raises no event: transit is observed, not commanded.

## 3. Outcome and restock

```mermaid
flowchart TB
    V["returns centre"]:::external
    E1["VendorReturnOutcomeSubmitted"]:::event
    P1["Policy: FirstFittingOutcome"]:::policy
    C1["ApplyOutcome"]:::command
    G1["ReturnAuthorization"]:::aggregate
    E2["ReturnCompleted"]:::event
    X2["offer-availability<br/>restocks listings"]:::external
    H1["Hotspot: unexpected lines<br/>stay facts"]:::hotspot
    H2["Hotspot: PICKUP_FAILED<br/>has no trigger yet"]:::hotspot
    V --> E1 --> P1 --> C1 --> G1 --> E2 --> X2
    P1 --- H1
    G1 --- H2
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/outcome.go` (`OnOutcomeSubmitted`, `commitOutcome`, `recordUnexpected`). The
first graded outcome that fits an open authorization completes it; later outcomes and outcomes for unknown or closed
authorizations are recorded on the outcome facts and acknowledged — never a consumer error (ADR 0002 A6 and A7).

## Hotspots

| Hotspot | Where it comes from |
| --- | --- |
| A lagging `OrderGroupView` refuses authorization | The view may lag customer-ordering's facts; the shopper retries with the same `Idempotency-Key` instead of the context blocking ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-models)) |
| The reverse-label route is config-gated | `SHIPPING_LABELS_URL` empty answers `label-unavailable`; honest until the route ships ([ADR 0005](/docs/adr/0005-the-reverse-label-route), [Troubleshooting](/docs/operations/troubleshooting)) |
| Unexpected lines stay facts | Nothing here re-stocks or re-decides from them; the warehouse fleet's exception flow owns them downstream (ADR 0002 A7) |
| PICKUP is dormant | `PICKUP_FAILED` waits for the last-mile pickup wave's `PackageReturnInitiated` (ADR 0002) |
| No money moves here | Fees and prices are facts; the refund itself is another context's decision (ADR 0001) |
