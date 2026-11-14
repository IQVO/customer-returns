# 1. Customer Returns is a bounded context of the retail network

Status: Accepted (bootstrap). Design source: IQVO/warehouse-docs `network/model/model.yaml`, context `customer-returns`, the network ADRs and the wave-4 design (`.hermes/plans/network-evolution-2026-11-08/wave4-design.md` section 4).

## Context

The retail network is modelled as bounded contexts, each its own service and database. customer-returns is the shopper-facing returns front door of the network: after a customer order group has shipped (or been delivered), the shopper asks to return it; this context decides what happens to the goods — keep them (returnless), bring them to the nearest returns centre of the region, or send them back to the node that fulfilled the order — issues the reverse label through the shipping context, and closes the loop when the returns centre grades what came back. It holds no money, no address and no free text.

## Decision

- This repo owns ONE aggregate, `ReturnAuthorization` (root id `ret-<uuid>`); the invariants and error names are those of the model. The state machine is `AUTHORIZED → IN_TRANSIT → RECEIVED | CLOSED_KEEP | CANCELLED`, plus `PICKUP_FAILED` (reserved for the last-mile pickup wave).
- Business decisions of the context:
- The disposition is decided HERE, never chosen by the caller: `decide` is a pure function over the lines' caller-declared prices, the method, the region's ACTIVE returns centre and the group's origin node. KEEP (returnless) when the total is within `ReturnlessThresholdCents` and every price is known; else NEAREST_RETURNS_CENTER; else ORIGIN_NODE; else `422 no-return-destination`.
- A group may be returned only while its OrderGroupView row is SHIPPED or DELIVERED (customer-ordering's facts); the view may lag — the shopper retries with the same `Idempotency-Key`.
- The reason is an enum (`DEFECTIVE|NOT_AS_DESCRIBED|WRONG_ITEM|NOT_NEEDED|OTHER`); quantities are eaches; every optional fact is omitted when it does not apply, never defaulted.
- An authorization expires 30 days after it was authorized; expiry is checked lazily on the next touch (no sweeper: this context has none of the fleet's sweeper machinery and does not need one).
- Unit prices are the storefront's facts, stored for the decision and echoed to the caller, never published (no-money rule); an absent price is recorded as `pricesAbsent: true` on the view and makes KEEP impossible.
- PII rule: no event, table, log or tool answer of this context carries an address, a person's name, a phone number or free text. The ship-to stays in the shipping context, which issues the reverse label; this context keeps only the postal PREFIX of the group's destination. `internal/architecture/pii_fitness_test.go` fails the build on PII-shaped identifiers.
- Events use the network CloudEvents namespace `com.retailnetwork.commerce.customer-returns.returnauthorization.<Event>` on `retailnetwork.customer-returns.events`. Published: ReturnAuthorized, ReturnLabelIssued, ReturnCompleted, ReturnCancelled. No `warehouse.*` topic is consumed or produced.
- The ONE synchronous in-org call is the shipping context's reverse-label route `POST /return-labels` (ADR 0005); every other sibling fact arrives as a consumed event. No REST/MCP call to any other context.
- No authentication on REST, consistent with the fleet.

## Consequences

Relationships, the consumed-event list and the local read models are in ADR 0003; the envelope in ADR 0004. The REST contract is `apis/openapi.yaml`, the event catalogue `apis/asyncapi.yaml`.
