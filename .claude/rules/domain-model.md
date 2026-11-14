# Domain model: ubiquitous language, aggregates, events, use cases

Source of truth: warehouse-docs `network/model/model.yaml` (context `customer-returns`) and
the wave-4 network design. Invariants, transitions and every assumption the model left open
are in `docs/adr/0002-aggregates-and-invariants.md` (assumptions A1-A10).

## Ubiquitous Language (use these exact names)

- **Return authorization** - the aggregate: the shopper's permission to return ONE order
  group. Identified by `authorizationId` (`ret-<uuid>`), covering `orderId` + `groupNo`.
- **Order group** - the customer-ordering fact CR returns: a group of an order that shipped
  (or was delivered) together. Its postal PREFIX (never the ship-to) picks the region.
- **Reason** - an enum (`DEFECTIVE`, `NOT_AS_DESCRIBED`, `WRONG_ITEM`, `NOT_NEEDED`,
  `OTHER`); free text is never accepted (the PII rule).
- **Disposition** - what happens to the goods, decided by CR's pure `decide`:
  `KEEP` (returnless), `NEAREST_RETURNS_CENTER` (the region's ACTIVE returns centre) or
  `ORIGIN_NODE` (the node that fulfilled the group). The caller never chooses it.
- **Returnless threshold** - `ReturnlessThresholdCents` (1500 cents): a return at or under
  it, with every price known and method DROP_OFF, is KEEP.
- **Method** - `DROP_OFF` (default) or `PICKUP`; PICKUP requires a window inside
  `[authorizedAt, expiresAt]` and is never KEEP.
- **Reverse label** - the label the shipping context issues for the return
  (`trackingNumber`, carrier `RN-GROUND`); CR links it and publishes `ReturnLabelIssued`,
  at most `MaxPackagesPerReturn = 5` packages.
- **Outcome** - the returns centre's graded receipt of what came back, per line
  `qtyRestockable` / `qtyUnsellable`; lines beyond the authorization are recorded as
  `unexpected` facts and never restocked.
- **OrderGroupView / ReturnsNetworkView** - the local, event-fed read models
  (`order_group_views`, `returns_network_nodes` + `service_area_prefixes` +
  `group_origins`); never read live from a sibling.
- **Expiry** - `authorizedAt + 30d`, checked lazily on the next touch; no sweeper.

## Aggregates

- **ReturnAuthorization** (`internal/domain/returnauthorization`, the ONLY aggregate):
  `AUTHORIZED -> IN_TRANSIT -> RECEIVED | CLOSED_KEEP | CANCELLED`, plus `PICKUP_FAILED`
  (reserved for the last-mile pickup wave). `Authorize` (decides+raises), `LinkLabel`,
  `MarkInTransit` (no event), `ApplyOutcome` -> RECEIVED, `Cancel` (shopper, while
  AUTHORIZED), lazy expiry. Bounds: 1..`MaxLinesPerReturn = 50` lines, quantity >= 1,
  <= 5 tracking numbers.
- `internal/domain/network`: the read-model value types (`OrderGroup`, `GroupStatus`,
  `Node`, `NodeLifecycle`, `PrefixAssignment`, `GroupOrigin`) and the `decide` inputs.
  `internal/domain/shared`: `Event`, `ErrValidation`, `ErrIllegalTransition`, `MaxInt32`.

Every aggregate has `Snapshot()`, a validating `Restore(Snapshot)` (`checkSnapshot`) and an
optimistic `Version()`; `now` and ids are passed in. `ErrNotFound` and `ErrConcurrentUpdate`
are port errors (`internal/application/ports/errors.go`). The domain imports nothing outside
`internal/domain`. One command per Save (the optimistic version moves by one).

## Domain events

Four events, all on `retailnetwork.customer-returns.events`, wire shape in
`apis/asyncapi.yaml` and goldens in `internal/adapters/outbound/kafka/testdata/` (ADR 0004):

- `ReturnAuthorized` (`Authorized`) - every disposition, KEEP included; no unit price ever.
- `ReturnLabelIssued` (`LabelIssued`) - once per linked reverse label.
- `ReturnCompleted` (`Completed`) - the graded outcome, authorized lines only; the fact
  offer-availability consumes to raise availability.
- `ReturnCancelled` (`Cancelled`) - reason `SHOPPER_REQUESTED` or `EXPIRED`.

## Key use cases (`internal/application/usecases`)

One method of `Service` per command and query; commands run in ONE `ports.UnitOfWork`
(load, domain call, save, outbox append).

- Authorize (`authorize.go`): `AuthorizeReturn` - checks the group SHIPPED/DELIVERED,
  resolves lines + prices + ownerRefs, decides the disposition, saves, publishes.
- Labels (`labels.go`): `IssueLabel` - the ONE in-org HTTP call (ADR 0005) OUTSIDE the unit
  of work, then one atomic link+append; tolerant `422 label-unavailable`.
- Queries + cancel (`queries.go`): `GetReturnAuthorization`, `ListReturnAuthorizations`
  (keyset), `CancelReturn`, the lazy expiry.
- Outcomes (`outcome.go`): `OnOutcomeSubmitted` - first fit completes, unexpected lines
  stay facts, unknown/closed is recorded and acknowledged.
- Consumer policies (`consumers.go`): `OnOrderPlaced`, `OnNoticeDecided` (order-tolerant),
  `OnNodeFact`, `OnPrefixFact`, `OnOriginFact`.
- Ports (`internal/application/ports/ports.go`) are interfaces only; the memory test
  doubles in `internal/adapters/outbound/memory` back the unit tests and the godog
  scenarios, the Postgres adapters are proven by
  `internal/adapters/outbound/postgres/repositories_integration_test.go`.
