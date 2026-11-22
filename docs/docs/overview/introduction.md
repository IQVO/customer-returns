---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
description: "What customer-returns owns and does not own, its Supporting classification in the retail network, its main capabilities and where to read next."
---

# Customer Returns

**Customer Returns** (`customer-returns`) is the shopper-facing returns front door of the retail network: after an
order group has shipped or been delivered, the storefront asks this context to **authorize** its return. The context
decides the **disposition** itself (`KEEP`, returnless, under the threshold and with every price known;
`NEAREST_RETURNS_CENTER`, the region's ACTIVE returns centre; or `ORIGIN_NODE`, the node that fulfilled the group), asks
the shipping context for the shopper's **reverse label**, and closes the loop when the returns centre's graded
**outcome** arrives. It holds no money, no address and no free text.

| | |
| --- | --- |
| Area | Retail network, tier `commerce` |
| Subdomain class | **Supporting**, custom-built ([Classification](/docs/ddd/subdomain-classification)) |
| Repository | [`IQVO/customer-returns`](https://github.com/IQVO/customer-returns), Go, hexagonal |
| Binaries | `cmd/api` (REST, four consumers, outbox relay), `cmd/mcp` (read-only MCP), `cmd/a2a` (read-only A2A), `cmd/projector` and `cmd/reports` (the optional analytics read side, [ADR 0012](/docs/adr/0012-analytics-read-side-and-return-rate-facts)) |
| Store and bus | Postgres (owned by the context; a separate analytical database for the read side), Kafka (the fleet's single broker) |
| Design source | `network/model/model.yaml` in IQVO/warehouse-docs; this repository's [ADRs](/docs/adr) record what the service does |

This repository is a service of the retail network, not of the warehouse-systems fleet.

## What it owns

- **`ReturnAuthorization`** (`ret-<uuid>`): the aggregate, its six states (`AUTHORIZED`, `IN_TRANSIT`, `RECEIVED`,
  `CLOSED_KEEP`, `CANCELLED`, `PICKUP_FAILED`), the 30-day lazy expiry and the pure `decide`
  ([Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas), [ADR 0002](/docs/adr/0002-aggregates-and-invariants)).
- **Two event-fed read models** ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-models)):
  `OrderGroupView` (from customer-ordering, with the group origin from order-sourcing) and `ReturnsNetworkView` (nodes
  and service areas from node-topology).
- **The four published events** `ReturnAuthorized`, `ReturnLabelIssued`, `ReturnCompleted`, `ReturnCancelled`
  ([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue)).
- **The return-rate KPI baseline** (optional, off by default): a projection of its own events into a separate
  analytical database, served read-only by `cmd/reports`.

## What it does not own

- **The order and its ship-to** belong to customer-ordering; **the nodes and service areas** to node-topology; **the
  fulfilling node and the per-item owners** to order-sourcing. They are copied in as events, never looked up.
- **The returns-centre receipt.** vendor-connectivity hands it over as `VendorReturnOutcomeSubmitted`.
- **The label.** The shipping context issues the reverse label; this context makes the ONE in-org synchronous call to
  its reverse-label route ([ADR 0005](/docs/adr/0005-the-reverse-label-route)).
- **Money.** Prices are the storefront's facts: stored and echoed, never published; refunds are customer-payments'.
- **PII.** No address, name, phone or free text enters any event, table, log or tool answer; `reason` is an enum.
- **Authentication.** REST, MCP and A2A are unauthenticated; the in-cluster boundary is the access control.

## Main capabilities

1. **Authorize** a return for a SHIPPED or DELIVERED order group, deciding the disposition.
2. **Label**: obtain the reverse label from the shipping context and move the authorization `IN_TRANSIT`.
3. **Cancel** an authorization while it is `AUTHORIZED`; an expired one is cancelled lazily (`EXPIRED`) by the next
   command that touches it.
4. **Complete** it from the returns centre's graded outcome (`ReturnCompleted`, the restock fact).
5. **Read** authorizations over REST, MCP and A2A, and (optionally) the returns-performance report.

## Business rules in one place

- `KEEP` only when every price is known, the method is `DROP_OFF` and the total is within `ReturnlessThresholdCents`
  (1500); otherwise the region's ACTIVE returns centre, else the origin node, else `422 no-return-destination`.
- An authorization expires 30 days after it is authorized.
- Unexpected outcome lines stay recorded facts; they are never restocked.
- Every `POST` takes an `Idempotency-Key` ([ADR 0010](/docs/adr/0010-fleet-rest-api-guideline),
  [ADR 0013](/docs/adr/0013-idempotency-replay-classes)).

## Where to read next

| You want to | Read |
| --- | --- |
| Run it | [Quickstart](/docs/overview/quickstart), [Configuration](/docs/operations/configuration) |
| Understand the structure | [Architecture](/docs/overview/architecture), [Runtime](/docs/overview/runtime) |
| Operate and debug it | [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Troubleshooting](/docs/operations/troubleshooting) |
| Test it | [Testing](/docs/development/testing) |
| Learn the domain | [Ubiquitous language](/docs/ddd/ubiquitous-language), [Use cases](/docs/ddd/use-cases), [DDD artifacts](/docs/ddd/ddd-artifacts) |
| Integrate with it | [API reference](/docs/api-reference), [Events](/docs/api-reference/events), [Integration](/docs/ecosystem/integration), [MCP tools](/docs/mcp/tools) |
| See why | [Architecture decision records](/docs/adr) |

The network-wide view of this context is on the
[retail network documentation](https://iqvo.github.io/warehouse-docs/network/contexts/customer-returns).

:::note[Disclaimer]
This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.
:::
