---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Route -> use case, kept in sync with `apis/openapi.yaml`; routes are registered in
`internal/adapters/inbound/http/server.go` (`NewRouter`), handlers are in `handlers.go`, and
`cmd/api/main.go` wires the `Server`. No authentication (fleet decision): the storefront is
the only caller and names the order in the body; the disposition is decided HERE, never by
the caller.

- `POST /return-authorizations` -> `AuthorizeReturn` (201) — body
  `{orderId, groupNo, lines[]{lineNo, unitPriceCents?}, reason, method?, pickupWindowStart?, pickupWindowEnd?}`;
  422 `validation-failed` (malformed body, unknown lineNo, missing lines), 422
  `group-not-shipped` (the view lags or the group is not SHIPPED/DELIVERED), 422
  `no-return-destination`
- `GET /return-authorizations?orderId=&cursor=&limit=` -> `ListReturnAuthorizations`
  (keyset page `{items, nextCursor}`, newest first, limit 1..100 default 20)
- `GET /return-authorizations/{authorizationId}` -> `GetReturnAuthorization` (an expired
  authorization is lazily cancelled by this read)
- `POST /return-authorizations/{authorizationId}/cancellations` -> `CancelReturn`
- `POST /return-authorizations/{authorizationId}/labels` -> `IssueLabel` — body `{packageNo}`
  1..5; calls the shipping context's reverse-label route (ADR 0005) with the authorization's
  `destinationNodeId` and NO postal prefix (the label owner derives it); 422
  `label-unavailable` while that route is not configured or refuses
- `GET /return-authorizations/{id}/events` is deliberately NOT built (audit = the event stream).
- `GET /healthz` (liveness), `GET /readyz` (drain gate, Postgres ping, Kafka metadata): not business operations.
- Not served by `cmd/api`: the read-only `GET /reports/...` routes (tag `Reports`, `cmd/reports`, `internal/adapters/inbound/reportshttp`, ADR 0012; rules in `.claude/rules/analytics.md`).

## Conventions

- **Errors** are RFC 9457 `application/problem+json`: `type` is a kebab-case slug from
  `problemCatalogue` in `internal/adapters/inbound/http/errors.go` (shared: `not-found` 404,
  `illegal-transition` 409, `concurrent-update` 409, `validation-failed` 422; plus
  `group-not-shipped` 422, `no-return-destination` 422, `label-unavailable` 422,
  `authorization-expired` 422), with `title`, `status`, `detail`, `instance`. An unmapped
  error is an opaque 500 `internal-error`. A new `Err` needs a catalogue row.
- **Idempotency-Key** is required on every write (`idempotency.go`): same key + same request
  replays the first response byte for byte, same key + other request is 409
  `idempotency-key-reused`, a missing key is 400 `idempotency-key-required`, a malformed one
  400 `idempotency-key-invalid`; the key is GLOBAL (migration `0003`) and replays carry
  `Idempotency-Replayed: true`; keys live 24 h in `idempotency_keys`. A new write registers
  through the `write(...)` wrapper in `NewRouter`, never bare `r.Post`.
- **NUL** (U+0000) is rejected before anything else (`nul.go`): 404 in the path, 422 in the
  query or body.
- Bodies are decoded strictly (`decodeJSON`, `response.go`): unknown fields are refused
  (`validation-failed`).
- Optional facts are omitted when they do not apply, never defaulted; arrays are always
  present; timestamps are RFC 3339 UTC; quantities are eaches.
- **PII**: never accept or return an address, a person's name, a phone number, an e-mail or
  free text; `reason` is an enum. `unitPriceCents` is echoed to the caller but never
  published on any event (`internal/architecture/pii_fitness_test.go`).
- **Handlers hold no business logic**: decode, call a use case, encode the view.
- Tests: `handlers_test.go` runs the real router over the memory doubles; the real
  middleware and Postgres are proven by `cmd/api/e2e_integration_test.go` and
  `cmd/api/idempotency_integration_test.go`; behaviour by `features/*.feature`.
