# 2. ReturnAuthorization: aggregate, invariants and the assumptions the model left open

## Status

Accepted (2026-11-14).

## Context

The model names one aggregate and its invariants but not how ids are minted, what a repeated command answers, how the disposition is decided or which bounds apply. This ADR records what the code does and the assumptions it rests on. The assumptions are numbered; code and docs cite them as `ADR 0002 A<n>`.

## Decision

### The aggregate (in `internal/domain/returnauthorization`, no import outside `internal/domain`)

- **ReturnAuthorization**, root id `ret-<uuid>` (minted by `ids.UUID` with the `ret` prefix). Fields: `orderId`, `groupNo`, `lines[]{lineNo, catalogItemId, quantity, unitPriceCents *int64, ownerRef}`, `reason` (enum), `disposition`, `method`, `destinationNodeId` ("" for KEEP), `originNodeId` ("" until a sourcing fact named the node), `pickupWindowStart/End` (zero unless PICKUP), `trackingNumbers []string`, `state`, `authorizedAt`, `expiresAt`, `closedAt`, `version`.
- Every aggregate has `Snapshot()`, a validating `Restore(Snapshot)` (`checkSnapshot`) and an optimistic `Version()`; `now` and ids are passed in. `ErrNotFound` and `ErrConcurrentUpdate` are port errors (`internal/application/ports/errors.go`).
- `internal/domain/network` holds the read-model value types (`OrderGroup`, `GroupStatus`, `Node`, `PrefixAssignment`, `GroupOrigin`); `shared` holds `Event`, `ErrValidation`, `ErrIllegalTransition`.

### State machine

`AUTHORIZED → IN_TRANSIT → RECEIVED | CLOSED_KEEP | CANCELLED`, plus `PICKUP_FAILED` (no transition raises it yet; the last-mile pickup wave's `PackageReturnInitiated` will). KEEP closes at once (`AUTHORIZED → CLOSED_KEEP` directly). `MarkInTransit` raises no event (transit is observed, not commanded, in v1).

### Invariants and their errors

| Error | Rule | Problem slug |
| --- | --- | --- |
| `shared.ErrValidation` | 1 to `MaxLinesPerReturn = 50` lines; `quantity ≥ 1`; a known reason, disposition and method; `expiresAt = authorizedAt + ReturnAuthorizationTTL (30d)` | `validation-failed` 422 |
| `ErrNoReturnDestination` | `decide` found neither a returns centre for the region nor an origin node (and KEEP did not apply) | `no-return-destination` 422 |
| — (KEEP rule) | KEEP ⇒ no destination, no label, `CLOSED_KEEP` at once | schema CHECKs |
| — (PICKUP rule) | PICKUP ⇒ window inside `[authorizedAt, expiresAt]`, both bounds, disposition ≠ KEEP | schema CHECKs |
| `ErrMaxPackages` (`LinkLabel`) | at most `MaxPackagesPerReturn = 5` tracking numbers | `validation-failed` 422 |
| `shared.ErrIllegalTransition` | cancel only while AUTHORIZED; label only an open physical authorization; outcome only while AUTHORIZED/IN_TRANSIT | `illegal-transition` 409 |

The shared slugs are `not-found` 404, `illegal-transition` 409, `concurrent-update` 409 and `validation-failed` 422. The table lives in `problemCatalogue` (`internal/adapters/inbound/http/errors.go`); an unmapped error is an opaque 500 `internal-error`.

### `decide` (the pure disposition function, `decide.go`)

KEEP iff method ≠ PICKUP and every line has a price and `total ≤ ReturnlessThresholdCents = 1500` cents; else NEAREST_RETURNS_CENTER when the region of the group's destination prefix has an ACTIVE RETURNS_CENTER; else ORIGIN_NODE when a sourcing fact named the fulfilling node; else `ErrNoReturnDestination`. `decide` takes only what the read models resolved — it performs no I/O, so the authorize use case is one unit of work.

### Assumptions

- **A1 Ids.** `authorizationId` is minted by the service (`ret-<uuid>`); `orderId` and `groupNo` name the caller's group; `lineNo` resolves against the placed group (an unknown lineNo is `ErrUnknownLine` → `validation-failed`).
- **A2 Prices are caller facts.** `unitPriceCents` per line is declared by the storefront (the only price knower), stored on the aggregate and the view, echoed to the caller, and NEVER published on any event (the no-money rule). A missing price makes KEEP impossible and is surfaced as `pricesAbsent: true`.
- **A3 Repeated authorization.** Two authorizations for the same group are distinct aggregates (a re-authorization after PICKUP_FAILED or a cancellation is a new id); the REST `Idempotency-Key` replays the first response of a request that did succeed.
- **A4 Expiry is lazy.** `expiresAt = authorizedAt + 30d`; every read, label and outcome call re-checks it first and, once passed, cancels the authorization (`ReturnCancelled` reason `EXPIRED`) and answers `422 authorization-expired`. There is no sweeper.
- **A5 One label per package.** The shipping context dedupes RETURN labels per `(returnAuthorizationId, packageNo)`; `LinkLabel` appends the tracking number, at most five. A duplicate tracking number is not re-linked (the aggregate treats it as already linked).
- **A6 The outcome is first-fit.** The first graded outcome that fits an open authorization completes it (`RECEIVED`, `ReturnCompleted` with the authorized lines only); later outcomes and outcomes for unknown/closed authorizations are recorded on the outcome facts and acknowledged — never a consumer error.
- **A7 Unexpected lines stay facts.** Lines beyond the authorization (an unexpected return) are recorded on `return_outcomes` flagged `unexpected`, never published, never restocked by offer-availability; the warehouse fleet's exception flow owns them.
- **A8 Integer ceiling.** Quantities and prices are `int`/`int64` and bounded by `shared.MaxInt32` where the domain accepts caller input, so no value reaches Postgres as an overflow.
- **A9 Cancellation is the shopper's.** `Cancel` is allowed while AUTHORIZED (before anything is in transit) with reason `SHOPPER_REQUESTED`; the lazy expiry is the only other cancellation cause in v1.
- **A10 OwnerRef.** The line's `ownerRef` comes from the group's sourcing release fact (whose stock the line took); "" means operator-owned or unknown and is omitted on the wire.

## Amendment (2026-11-20): the as-built state machine and expiry answers

- **A11 As-built transitions and expiry answers.** The state machine line above (and ADR 0001's one-line summary of it) is the designed shape; the transitions the code allows are exactly: `AUTHORIZED → CLOSED_KEEP` (KEEP, at authorization), `AUTHORIZED → CANCELLED` (`Cancel`), `AUTHORIZED → RECEIVED` and `IN_TRANSIT → RECEIVED` (`ApplyOutcome`), `AUTHORIZED → IN_TRANSIT` (`MarkInTransit`, needs a linked label, never KEEP) and `AUTHORIZED → PICKUP_FAILED` (`MarkPickupFailed`, PICKUP only). Nothing leaves `IN_TRANSIT` but `ApplyOutcome`, and there is no `IN_TRANSIT → PICKUP_FAILED`. `MarkInTransit` and `MarkPickupFailed` are kept in `internal/domain/returnauthorization/commands.go` but no use case or adapter calls them in v1 (reserved for a transit feed and the last-mile pickup wave), so `IN_TRANSIT` and `PICKUP_FAILED` are not reached yet. A4 as built: a read never cancels (it reports the remaining state); `IssueLabel` cancels an expired authorization (`EXPIRED`) and answers `422 authorization-expired`, the only route that does; `CancelReturn` cancels an expired authorization with reason `EXPIRED` and answers `200` (the expiry is the cancellation; A9's `SHOPPER_REQUESTED` applies before the TTL); the `OnOutcomeSubmitted` consumer expires it first and records the outcome as a fact (A6). Proven by `TestCancelReturnOnAnExpiredAuthorizationCancelsAsExpiredAndSucceeds` and `TestIssueLabelOnAnExpiredAuthorizationCommitsTheCancellationThenRefuses`.

## Consequences

The schema CHECKs (migration `0001`) mirror A2's no-destination-for-KEEP rule, the PICKUP window rule and the terminal-state `closed_at` rule, so a restored snapshot that breaks an invariant cannot be persisted. The goldens in `internal/adapters/outbound/kafka/testdata` pin each event's wire shape.
