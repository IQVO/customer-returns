---
id: overview
title: API overview
slug: /api-reference
sidebar_position: 1
description: "The interfaces of customer-returns (REST, reports, events, MCP, A2A), the endpoint table and the conventions of the contracts."
---

# API overview

:::info Implemented by cmd/api and cmd/reports
`cmd/api` serves the return-authorization routes and the probes (`internal/adapters/inbound/http`) on `HTTP_ADDR`
(default `:8080`); `cmd/reports` serves the two `/reports/*` routes (`internal/adapters/inbound/reportshttp`) on `:8092`.
The contracts are `apis/openapi.yaml` and `apis/asyncapi.yaml`. `apis/openapi.yaml` has 9 operations: the 5
return-authorization operations, the 2 reports and `/healthz` and `/readyz`; the routers register exactly those. See the
[Quickstart](/docs/overview/quickstart) for a walk through the API.
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` (OpenAPI 3.0.3, version 1.0.0) | The pages under *REST* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. The spec names the server `http://localhost:8000/api/customer-returns`, the gateway route in `warehouse-infra` (prefix stripped); `cmd/api` itself serves the routes at the root of `:8080`, and the reports operations carry their own `http://localhost:8092` server. |
| Events | `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0) | Four published and ten consumed event types; summarised on the [event catalogue](/docs/api-reference/events), every field on [Domain events](/docs/ddd/domain-events). |
| MCP | [ADR 0006](/docs/adr/0006-read-only-mcp-server), `internal/adapters/inbound/mcp` | `cmd/mcp` serves Streamable HTTP on `/mcp` (port 8090; through Kong `/api/mcp/customer-returns`). Two read-only tools, no write tool: `get_return_authorization`, `list_return_authorizations`; one resource and one prompt. See [MCP tools](/docs/mcp/tools). No auth, by fleet decision. |
| A2A | [ADR 0009](/docs/adr/0009-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` (port 8093; through Kong `/api/a2a/customer-returns`), the Agent Card at `/.well-known/agent-card.json`. One read-only skill, no write skill: `get_return_authorization` (`authorizationId`). No auth, by fleet decision. |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| Return Authorizations | `POST /return-authorizations` | `authorizeReturn` | `201`, the authorization with its decided disposition (`KEEP` closes at once as `CLOSED_KEEP`) |
| Return Authorizations | `GET /return-authorizations?orderId=&cursor=&limit=` | `listReturnAuthorizations` | `200`, a keyset page, newest first (`limit` 1..100) |
| Return Authorizations | `GET /return-authorizations/{authorizationId}` | `getReturnAuthorization` | `200`, the authorization |
| Return Authorizations | `POST /return-authorizations/{authorizationId}/cancellations` | `cancelReturn` | `200`, `CANCELLED` (`SHOPPER_REQUESTED`, or `EXPIRED` when past `expiresAt`) |
| Return Authorizations | `POST /return-authorizations/{authorizationId}/labels` | `issueReturnLabel` | `200`, the authorization `IN_TRANSIT` with the tracking number linked; `422 label-unavailable` while the reverse-label route is off or refuses |
| Reports | `GET /reports/returns-performance?from=&to=` | `getReturnsPerformanceReport` | `200`, per UTC day: returns authorized and received, time to receive p50/p95, plus the reason Pareto (at most 5) and freshness; `400 invalid-report-range`. Served by `cmd/reports` on `:8092` (ADR 0012), only with the chart's `analytics.enabled` |
| Reports | `GET /reports/freshness` | `getReportFreshness` | `200`, `asOf` and `lagSeconds` of the analytics projection (both `null` before the first event) |
| Health | `GET /healthz`, `GET /readyz` | `healthz`, `readyz` | `200`; `/readyz` answers `503` while draining or when Postgres or Kafka is unreachable |

`GET /return-authorizations/{id}/events` is deliberately not built: the audit trail is the event stream.

## Conventions in the contract

- **`Idempotency-Key`.** Required on every `POST` (1 to 128 characters), implemented by
  `internal/adapters/inbound/http/idempotency.go`. The key is global and the request fingerprint is method + path +
  SHA-256 of the body. The same fingerprint replays the stored answer for 24 h with the response
  header `Idempotency-Replayed: true`; the same key on a different method, path or body is
  `409 idempotency-key-reused`; a missing key is `400 idempotency-key-required` and a key that is not 1 to 128
  printable ASCII characters is `400 idempotency-key-invalid`. What is stored is decided by the replay class of the
  answer: a 2xx and `authorization-expired` (OUTCOME: the lazy cancellation commits with it) are replayed; a refusal
  that depends on state that can change (RETRYABLE: `group-not-shipped`, `no-return-destination`, `label-unavailable`,
  `concurrent-update`, `illegal-transition`, `not-found`, `validation-failed`) is not stored and its key is released, so
  the same key and body executes again; a 5xx is never stored. See
  [ADR 0013](/docs/adr/0013-idempotency-replay-classes) and [ADR 0010](/docs/adr/0010-fleet-rest-api-guideline).
- **Errors** are RFC 9457 `application/problem+json` with `type` = the bare slug (not a URL), `title`, `status`,
  `detail` and `instance`. The slugs are `not-found`, `illegal-transition`, `concurrent-update`, `validation-failed`,
  `group-not-shipped`, `no-return-destination`, `label-unavailable`, `authorization-expired`,
  `idempotency-key-required`, `idempotency-key-invalid`, `idempotency-key-reused`, `dependency-unavailable` and
  `internal-error` (`internal/adapters/inbound/http/errors.go`), plus `invalid-report-range` and `method-not-allowed`
  on `cmd/reports` (`internal/adapters/inbound/reportshttp/body.go`). The full status table is on
  [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).
- **Optimistic concurrency.** The aggregate carries a `version`; a lost race is `409 concurrent-update`, which the
  client resolves by re-fetching and retrying.
- **Prices are caller facts.** `unitPriceCents` is optional per line, stored and echoed, never published; an absent price
  makes `KEEP` impossible and is answered as `pricesAbsent: true`.
- **Request bodies are strict.** Unknown fields, trailing data and an empty body are `422 validation-failed`; a body is
  limited to 4 MiB; a NUL character anywhere is a uniform client error.
- **Auth**: none. The fleet reverted REST and MCP auth, and `TestNoAuthMiddlewareReintroduced` fails CI if auth
  middleware returns under `internal/adapters/inbound`.
- **CloudEvents.** Every Kafka message is CloudEvents 1.0 in structured mode with `content-type:
  application/cloudevents+json; charset=UTF-8`; the Kafka key is the subject (the `authorizationId`).

## A2A skill requests

A2A is the delegation channel ([ADR 0009](/docs/adr/0009-a2a-inbound-adapter)); facts still travel only as CloudEvents on
Kafka. A client sends a message whose first part is a data part (or a text part holding the same JSON):

```json
{"skill": "get_return_authorization", "arguments": {"authorizationId": "ret-0f3a91"}}
```

The reply is one agent message carrying a data part `{"skill", "data" | "problem"}` and a one-line text summary. Domain
and request problems are answered inside the envelope (`problem` with `type`, `title`, `detail`, `status`), never as a
JSON-RPC error: `malformed-skill-request`, `unknown-skill`, `invalid-argument` (all 400), `not-found` (404),
`validation-failed` (422) and `internal-error` (500). Unknown request fields are rejected rather than ignored. The agent
is message-only: no Task lifecycle, no streaming, and the Agent Card declares no security scheme.

Source: `internal/adapters/inbound/a2a/{server,skills,mapping}.go`, `cmd/a2a/router.go`.
