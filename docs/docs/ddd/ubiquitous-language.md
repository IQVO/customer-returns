---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
sidebar_position: 0
description: "The terms customer-returns speaks, mapped to the Go identifiers in this repository, plus the neighbour terms it reads."
---

# Ubiquitous language

The terms this context owns, with the Go identifiers the service uses (`package.Type`). Where a word is also a retail
term, the definition says what this model means by it. The design source is `network/model/model.yaml` in
IQVO/warehouse-docs and [ADR 0001](/docs/adr/0001-customer-returns-bounded-context); the aggregate is in
[Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas).

## This context's terms

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Return Authorization** | The shopper-facing decision that one order group may come back, with its disposition decided at authorization. Root id `ret-<uuid>`. States `AUTHORIZED`, `IN_TRANSIT`, `RECEIVED`, `CLOSED_KEEP`, `CANCELLED`, `PICKUP_FAILED` (reserved). | `returnauthorization.ReturnAuthorization`, `ID` | A refund (no money moves here) or a carrier pickup order. |
| **Order group** | The unit a shopper returns: one `orderId` and `groupNo` of a placed customer order. | `network.OrderGroup`, `order_group_views` | The whole order. |
| **Line** | One authorized order line: `lineNo`, `catalogItemId`, `quantity`, optional `unitPriceCents`, `ownerRef`. | `returnauthorization.Line` | A package or an outcome line. |
| **Reason** | Why the shopper returns: `DEFECTIVE`, `NOT_AS_DESCRIBED`, `WRONG_ITEM`, `NOT_NEEDED`, `OTHER`. Codes only, never free text. | `returnauthorization.Reason`, `Reason.Valid` | A cancel reason. |
| **Disposition** | What the shopper does with the goods: `KEEP` (returnless), `NEAREST_RETURNS_CENTER`, `ORIGIN_NODE`. Decided here, never chosen by the caller. | `returnauthorization.Disposition`, `Decide` | The method. |
| **Returnless refund (KEEP)** | When the caller-declared total is within `ReturnlessThresholdCents` (1500), every price is known and the method is not PICKUP: no destination, no label, `CLOSED_KEEP` at once. | `ReturnlessThresholdCents`, `keepEligible`, `StateClosedKeep` | A discount or a partial refund. |
| **Method** | How the goods travel back: `DROP_OFF` (live, the default) or `PICKUP` (activation-gated, dormant in v1). | `returnauthorization.Method` | The disposition. |
| **Pickup window** | The promised doorstep window of a PICKUP return, inside `[authorizedAt, expiresAt]`, both bounds. | `PickupWindowStart`, `PickupWindowEnd` | The authorization's validity. |
| **Returns centre** | An `ACTIVE` node of type `RETURNS_CENTER` serving the group's region; the `NEAREST_RETURNS_CENTER` destination. | `returns_network_nodes`, `network.Node` | Any node (fulfillment centres are origins, not destinations). |
| **Origin node** | The node that fulfilled the group, named authoritatively by a sourcing lock fact; the `ORIGIN_NODE` destination. | `network.GroupOrigin`, `group_origins` | The destination node. |
| **Destination node** | Where the goods go: the returns centre or the origin node; `""` for KEEP. | `DestinationNodeID` | The ship-to address, which never enters this context. |
| **Postal prefix** | The PII-safe prefix of the group's destination kept to resolve the region. | `OrderGroup.DestinationPostalPrefix`, `service_area_prefixes` | The full address. |
| **Reverse label** | The carrier label the shipping context issues on `POST /return-labels`; this context keeps only the tracking number and the destination node id. | `ports.LabelIssuer`, `usecases.IssueLabel` | An outbound shipping label. |
| **Package / tracking number** | One linked reverse label of a return; at most `MaxPackagesPerReturn` (5), each linked once. | `LinkLabel`, `TrackingNumbers` | A line of the return. |
| **Outcome** | The returns centre's graded receipt of what came back, per item `qtyRestockable` and `qtyUnsellable`. | `ApplyOutcome`, `OutcomeLine` | The authorization itself. |
| **Unexpected line** | A graded line beyond the authorization: recorded on the outcome facts, flagged, never published, never restocked. | `recordUnexpected`, `return_outcomes` | A discrepancy to reconcile. |
| **Expiry** | `authorizedAt` + 30 days (`ReturnAuthorizationTTL`), enforced lazily on the next touch; there is no sweeper. | `expiresAt`, `expireLazily`, `expireAndReject` | A batch job. |
| **Cancellation** | Allowed while `AUTHORIZED`: reason `SHOPPER_REQUESTED` (the shopper) or `EXPIRED` (the lazy expiry). | `Cancel`, `CancelReason` | An outcome or a rejection. |
| **Prices absent** | At least one line carries no declared unit price; KEEP is impossible and the view says so. | `PricesAbsent` | A zero price (which is a known price). |
| **ownerRef** | Whose stock the item was, from the sourcing release fact; echoed on events for restock attribution. | `Line.OwnerRef`, `shared.ValidOwnerRef` | The shopper. |
| **Each** | The unit of every quantity: units of a catalog item, never cartons or pallets. | `Line.Quantity`, `QtyRestockable`, `QtyUnsellable` | A package count. |

## Neighbour terms this context reads

| Term | Definition (owner's) | Owner |
| --- | --- | --- |
| **Order group status** | `PROMISED`, `LOCKED`, `SHIPPED`, `DELIVERED`, `LOST`, `RETURNED`; only SHIPPED and DELIVERED are authorizable. Held locally as `order_group_views`. | customer-ordering |
| **Fulfillment assignment** | Which node released or locked the fulfillment of a group, with the per-item owners. Held locally as `group_origins`. | order-sourcing |
| **Node**, **Service area** | A building with a lifecycle and type, and which region a postal prefix serves. Held locally as `returns_network_nodes` and `service_area_prefixes`. | node-topology |
| **Vendor return advice**, **graded outcome** | The Vendor API's advice a returns centre works from, and the graded outcome it submits back through `VendorReturnOutcomeSubmitted`. | vendor-connectivity |
| **Catalog item**, **listing restock** | The network's own entry for a product, and the restocking of its listings from `ReturnCompleted`. | offer-availability |

## Fleet translation

None at the language level. The warehouse-systems fleet's returns-processing context polls the Vendor API of
vendor-connectivity for return advices and submits graded outcomes; this repository does not model that side
([Integration](/docs/ecosystem/integration#edges-that-do-not-exist)).

## Terms the model leaves open

| Term | What is missing |
| --- | --- |
| Refund | Fees and money are facts to this context, never computed or moved; the refund itself lives outside ([ADR 0001](/docs/adr/0001-customer-returns-bounded-context)). |
| Pickup failure | `PICKUP_FAILED` and its `PackageReturnInitiated` trigger are reserved for the last-mile pickup wave; no transition raises them in v1 ([ADR 0002](/docs/adr/0002-aggregates-and-invariants)). |
| Unexpected-line disposition | What the warehouse fleet's exception flow does with unexpected lines is not modelled here; they stay recorded facts. |
