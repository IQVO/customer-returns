---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
sidebar_position: 2
description: "Why customer-returns is a Supporting subdomain of the retail network, and how its neighbours are classified."
---

# Subdomain classification

**Customer Returns is a Supporting subdomain**: bounded context `customer-returns`, CloudEvents tier `commerce`, business
model *service enabler*, evolution *custom-built*. Its place on the Core Domain Chart is business differentiation 0.34,
model complexity 0.42 ([Core Domain Chart](/docs/ddd/core-domain-chart)).

## Reasoning

- **Low differentiation.** The rules are the common practice of reverse logistics: authorize the return of a shipped
  order group, issue a reverse label, grade the outcome. Nothing in this context decides a promise or a route, which is
  what sets the network apart. It enables the post-purchase service; it does not differentiate.
- **Moderate complexity.** One aggregate with a six-state machine, a pure disposition function over three local read
  models (`OrderGroupView`, `ReturnsNetworkView` in three halves, `GroupOrigin`), ten consumed event types from four
  producers, and one synchronous seam (the reverse-label route, [ADR 0005](/docs/adr/0005-the-reverse-label-route)).
- **Not Generic.** Buying a returns product would still leave the disposition policy (`ReturnlessThresholdCents`, the
  destination selection over this network's returns centres and origin nodes) and the no-money/no-PII seams to write,
  and they are specific to this network's model.
- **Why a context of its own.** It is the shopper's contract, not the vendor's (vendor-connectivity is the vendor
  protocol); it owns the disposition decision nobody else should make; and it keeps the PII boundary — no address, name,
  phone or free text ever enters it ([ADR 0001](/docs/adr/0001-customer-returns-bounded-context)).
- **What would move it.** If the returns policy became a scored, competitive programme (dynamic returnless thresholds,
  instant refunds, graded recovery), the context would grow a real policy role and move up. Today the threshold is one
  constant in `decide.go`.

## Neighbours

Copied from the retail network's classification
([Subdomain Classification](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification),
`origin/main` of IQVO/warehouse-docs), with the relation to this context:

| Bounded context | Classification | Tier | Relation to customer-returns |
| --- | --- | --- | --- |
| `customer-ordering` | Generic | `commerce` | Upstream: group status and placed lines (Conformist). |
| `node-topology` | Supporting | `logistics` | Upstream: returns centres and service-area prefixes (Conformist). |
| `order-sourcing` | Core | `commerce` | Upstream: which node fulfilled the group and whose stock each item took (Conformist). |
| `vendor-connectivity` | Supporting | `partner` | Upstream for graded outcomes, downstream for the return advices (Customer/Supplier both ways, events only). |
| `shipping-and-tracking` | Generic | `logistics` | Upstream supplier of the reverse label, over the one synchronous route (Customer/Supplier). |
| `offer-availability` | Supporting | `commerce` | Downstream: restocks listings from `ReturnCompleted`. |
| `customer-returns` | **Supporting** | `commerce` | This context. |
| `delivery-promise` | Core | `commerce` | No edge (Separate Ways). |
| `linehaul-transportation`, `last-mile-delivery` | Supporting | `logistics` | No edge today; the last-mile pickup wave will raise `PICKUP_FAILED`. |

In the problem space the subdomain is *Customer Returns*: authorizing shoppers to return shipped order groups, deciding
the disposition of the goods, issuing the reverse label and closing the loop on the returns centre's graded outcome.

The warehouse-systems fleet is not a neighbour: its returns-processing context reaches this one only through the Vendor
API of vendor-connectivity (return advices and outcome submission), never directly
([Integration](/docs/ecosystem/integration#edges-that-do-not-exist)). The fleet's own classification of its contexts is
on the [warehouse-docs strategic design](https://iqvo.github.io/warehouse-docs/strategic-design/subdomain-classification)
site.
