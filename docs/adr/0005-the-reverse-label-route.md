# 5. The reverse-label route: the one in-org call

## Status

Accepted (2026-11-14).

## Context

A physical return needs a label the shopper can hand to a carrier. The ship-to — the one piece of PII a label needs — lives in the shipping context, and the network's PII rule (wave-4 design, F25) says this context never reads it. The shipping context therefore owns reverse labels the way it owns outbound labels, and this context calls it over the one in-org route the network allows (the pattern vendor-connectivity already uses for `POST /labels`).

## Decision

- **The call.** `POST /return-labels` on the shipping context (its ADR 0015, migration 0005): body `{returnAuthorizationId, orderId, packageNo, destinationPostalPrefix}`; answer `{trackingNumber, carrier}`. The client is `internal/adapters/outbound/labels` — the ONLY outbound HTTP client of this context — and implements `ports.LabelIssuer`.
- **Config-gated, tolerant.** `SHIPPING_LABELS_URL` (default empty) enables the route; with it unset — or on any transport failure, non-200 answer, or a body without a tracking number — the client answers `ports.ErrLabelUnavailable`, the label use case answers `422 label-unavailable`, and NOTHING else happens: no 500, no retry storm, no outbox row. The authorize-and-link flow stays available and honest until the route and its config ship.
- **Idempotent by construction.** The client sends `Idempotency-Key: <returnAuthorizationId>:<packageNo>`, restating the pair the shipping context dedupes RETURN labels on; a retry is a replay.
- **Outside the unit of work.** The HTTP call runs BEFORE the transaction: the use case asks for the label, then ONE transaction re-reads the aggregate, links the tracking number (`LinkLabel`, ≤ 5) and appends `ReturnLabelIssued`. A failure between the call and the commit is healed by the shipping context's dedupe on the retry.
- **What this context keeps.** The tracking number and the destination node id — never the address, never the label PDF. The shopper-facing tracking view is THIS context's authorization (stated in the shipping context's ADR); the reverse label does not start a package journey and scans of reverse packages are counted-and-acknowledged unknowns there.
- **The pickup stop groups by construction (F3).** The shipping context computes the reverse label's stop key with the same secret over the same ship-to, so a later PICKUP groups with the original delivery stop; this context knows nothing of that mechanism.

## Consequences

- This context has exactly one synchronous seam, config-gated and tolerant; everything else is events.
- When the route answers anything but 200, the shopper sees `422 label-unavailable` and may retry later with the same `Idempotency-Key`; the authorization itself is unaffected.
- The e2e integration test drives a stub of the route (issuing `RN-RET-*` tracking numbers, answering 503 for one package) to pin both sides of the contract.

## Addendum 2026-11-16: the route answers 201

The Decision and the Consequences above name 200 as the answer that carries a label. The route as the shipping context shipped it (its ADR 0017 and migration 0007, the numbers that replaced the 0015 and 0005 named above) answers `201 Created` with a `ReturnLabel` body, and a replay of the same `Idempotency-Key` answers that first response again: `201` plus `Idempotency-Replayed: true`, never `200`. Read "non-200" and "anything but 200" above as "anything but 201": `201` is the one success the client accepts, and every other answer, `200` included, stays `ports.ErrLabelUnavailable`. Nothing else of the Decision changes: the client still reads only `trackingNumber` and `carrier`, both required properties of `ReturnLabel`, and every error the route documents (`409`, `422`, `503`) still maps to `422 label-unavailable`.
