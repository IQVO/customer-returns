---
id: ddd-artifacts
title: DDD artifacts (ddd-crew)
sidebar_label: DDD artifacts index
sidebar_position: 10
description: "Index of the ddd-crew design pack for customer-returns: charts, canvases, maps, flows, diagrams and the event catalogue."
---

# DDD artifacts (ddd-crew)

The strategic and tactical design of `customer-returns`, drawn with the [ddd-crew](https://github.com/ddd-crew) tools plus
UML and ER diagrams. Every diagram is Mermaid. The pack was written for this repository's returns domain and verified
against its code, migrations, contracts and ADRs on `develop`; each page carries a `Source:` line where it states
something the code decides. The design sources are the network model (`network/model/model.yaml`, context
`customer-returns`, in IQVO/warehouse-docs), the wave-4 design and this repo's
[ADR 0001](/docs/adr/0001-customer-returns-bounded-context).

| Artifact | ddd-crew tool / notation | What it shows |
| --- | --- | --- |
| [Core domain chart](/docs/ddd/core-domain-chart) | [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts) | Where this context sits on business differentiation vs model complexity, and why it is Supporting. |
| [Bounded context canvas](/docs/ddd/bounded-context-canvas) | [Bounded Context Canvas](https://github.com/ddd-crew/bounded-context-canvas) | Purpose, classification, roles, every inbound and outbound message, business decisions. |
| [Context map](/docs/ddd/context-map) | [Context Mapping](https://github.com/ddd-crew/context-mapping) | This context's slice of the network map: pattern and channel per edge. |
| [Aggregate design canvas](/docs/ddd/aggregate-design-canvas) | [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas) | `ReturnAuthorization`: states, invariants, commands, events, and the read models `Decide` reads. |
| [Domain message flow](/docs/ddd/domain-message-flow) | [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling) | A physical return end to end, the returnless KEEP path, and cancellation with lazy expiry. |
| [EventStorming](/docs/ddd/eventstorming) | [EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) | Process-level boards with sticky colours and hotspots. |
| [Ubiquitous language](/docs/ddd/ubiquitous-language) | Glossary | Every term with the Go identifier that implements it. |
| [Class diagrams](/docs/ddd/class-diagram) | UML class diagram | Domain types and the hexagonal layout. |
| [Entity-relationship diagram](/docs/ddd/entity-relationship) | ER diagram | The Postgres schema of migrations 0001 to 0003: nine tables and one sequence; plus the three tables of the optional analytical database (`analytics/migrations/0001_return_facts.up.sql`, ADR 0012). |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | UML sequence diagram | `AuthorizeReturn`, `IssueLabel`, the outcome consumer and cancellation, with error branches. |
| [Domain events](/docs/ddd/domain-events) | Event catalogue | The four published events with payloads and an example envelope; the ten consumed types. |

Related pages outside the pack: [Use cases](/docs/ddd/use-cases), [Subdomain classification](/docs/ddd/subdomain-classification),
[Integration](/docs/ecosystem/integration) and the [event catalogue](/docs/api-reference/events).

:::note[Sources of truth]
The code wins. Domain rules come from `internal/domain/{returnauthorization,network,shared}`, behaviour from
`internal/application/usecases`, the envelope from `internal/adapters/kafka/cloudevents`, the schema from
`internal/adapters/outbound/postgres/migrations`, the REST contract from `apis/openapi.yaml`, the event contract from
`apis/asyncapi.yaml`, and decisions from the ADRs in `docs/adr/`. Where a page and the code disagree, the page is wrong.
:::

:::info Not in this pack
The read-only MCP server ([ADR 0006](/docs/adr/0006-read-only-mcp-server)) and the A2A adapter
([ADR 0009](/docs/adr/0009-a2a-inbound-adapter)) are built, but they are read surfaces over the model and add no
aggregate, entity or event of their own, so the pack has no pages for them. They are described under
[MCP tools](/docs/mcp/tools) and the [API overview](/docs/api-reference).
:::
