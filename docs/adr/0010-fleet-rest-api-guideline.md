# ADR 0010: Adopt the fleet REST API guideline

## Status

Accepted (2026-11-01).

## Context

The fleet REST API guideline (warehouse-docs, "Warehouse REST API Guideline")
is mandatory for every `apis/openapi.yaml`: URLs name things and never actions,
every `POST` and `PATCH` is idempotent, key reuse is `409`, and every
collection is a keyset-paged `{items, nextCursor}` envelope. It is enforced by
the `rest-guideline` Spectral rule (R1-R8) in the `api-lint` job. customer-returns
adopts it from its first contract: the routes were born nouns (the commands are
transition resources), the idempotency key is global with `409` reuse, and the
one collection is keyset-paged.

## Decision

- **Noun routes (R1, R2).** The two commands are transition resources from the
  first contract: `POST /return-authorizations/{authorizationId}/cancellations`
  and `POST /return-authorizations/{authorizationId}/labels` (guideline
  1.3d / 1.4); there is no `:verb` route and never was, so no alias and no
  retired-route test is needed.
- **Idempotency (R3-R6, R8).** Every `POST` was already behind
  `RequireIdempotencyKey`, whose key row commits in the SAME transaction as the
  aggregate and its outbox rows (the use case joins it through `TxBinder`). The
  change is the contract: the key is **global** (migration `0003` narrows the
  primary key from `(route, key)` to `(key)`, keeping the earliest row of any
  key stored under two routes); the fingerprint is method + path (the `route`
  column) + SHA-256 of the body; a different fingerprint is
  `409 idempotency-key-reused` (was `422`); a missing key is
  `400 idempotency-key-required`, a key that is not 1 to 128 printable ASCII
  characters is `400 idempotency-key-invalid`; a replay carries
  `Idempotency-Replayed: true` (the first execution never does). 2xx and 4xx are
  stored and replayed, a 5xx or panic rolls back and is never stored.
- **Pagination (R7).** customer-returns has no list endpoint; the one flagged
  GET is exempt (below). No `page` package or `unknown-query-parameter` check is
  needed because no collection takes `limit`/`cursor`.

## Exemptions

- `GET /return-authorizations/{authorizationId}` is a single aggregate view,
  and `GET /healthz` / `GET /readyz` are probes; the one collection
  (`GET /return-authorizations?orderId=`) is keyset-paged with no exemption.

## Consumers

Inside this repo nothing calls these routes over HTTP (MCP and A2A call use
cases directly). Tests, BDD features, README and the docs pages use the same
routes. Outside: the storefront-bff allowlist adds `POST
/return-authorizations` and the GETs in its own PR (the wave-4 seam list).

## Consequences

Clients must send `Idempotency-Key` on every `POST` (already
required here), treat `409 idempotency-key-reused` as a client bug, and read
`Idempotency-Replayed`.
