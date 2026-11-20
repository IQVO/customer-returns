---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 2
description: "The two read-only MCP tools of customer-returns, each declaring authority L1, plus the return-authorization resource and the review SOP prompt."
---

# MCP tools

`cmd/mcp` serves a read-only [Model Context Protocol](https://modelcontextprotocol.io) server over **Streamable HTTP
only** ([ADR 0006](/docs/adr/0006-read-only-mcp-server)). It is unauthenticated like the rest of the fleet: the
in-cluster ClusterIP boundary is the access control.

| | |
| --- | --- |
| Listen address | `MCP_ADDR`, default `:8090` |
| Endpoint | `/mcp` (also mounted at `/`) |
| Probe | `GET /healthz` returns `200 {"status":"ok"}` |
| Needs | `DATABASE_URL` only; it dials no Kafka and registers no write seam |
| Gateway | `/api/mcp/customer-returns` in `warehouse-infra` (`helm-values/customer-returns.yaml`, `mcp.route.path`) |
| Deployed by | the chart behind `mcp.enabled` (default off, on in `warehouse-infra`) |

Source: `cmd/mcp/main.go`, `cmd/mcp/router.go`, `internal/adapters/inbound/mcp/{server,tools,resources,prompts,errors}.go`.

## Annotations and authority

Both tools carry `readOnlyHint: true`, `idempotentHint: true` and `openWorldHint: false`. There is no write tool:
`TestToolSurface` (`internal/adapters/inbound/mcp/governance_test.go`, with its write-verb sensor) pins the set and fails
on a write-named tool, and `TestMCPAdapterDependencyRule` limits the adapter to the application and domain layers. The
adapter holds only the two read use cases (`Reads` in `tools.go`), so a tool call adds no `outbox_events` row.

Every tool also declares its **agent authority level** `L1` (Agentic Standard v2,
[ADR 0011](/docs/adr/0011-agent-authority-levels-and-complete-mcp-surface)): a pure read of this context's own state.
The declaration travels in `tools/list` as `_meta` under the key `com.warehouse/authority`; it is pinned by
`TestEveryToolIsReadOnlyAnnotatedL1` and by the static sensor `TestEveryMCPToolDeclaresAuthority`
(`internal/architecture`). The A2A skill carries the equivalent tag `authority:L1`.

| Tool | Required input | Optional input | Annotation | Authority |
| --- | --- | --- | --- | --- |
| `get_return_authorization` | `authorizationId` | none | read-only | `L1` |
| `list_return_authorizations` | `orderId` | `cursor`, `limit` | read-only | `L1` |

The tools are bound to the same use cases as `GET /return-authorizations/{authorizationId}` and
`GET /return-authorizations`. A failure is an `isError` tool result whose text is `<slug>: <detail>` with the REST
slug (`not-found`, `validation-failed`); an untyped error is logged (`mcp tool failed with an unexpected error`) and
answered as `internal-error: an unexpected internal error occurred`.

## `get_return_authorization`

Reads one return authorization.

- **Input:** `authorizationId` (string, required, for example `ret-0f3a91`).
- **Output:** `authorizationId`, `orderId`, `groupNo`, `reason`, `disposition` (`KEEP`, `NEAREST_RETURNS_CENTER`,
  `ORIGIN_NODE`), `method`, `state` (`AUTHORIZED`, `IN_TRANSIT`, `RECEIVED`, `CLOSED_KEEP`, `CANCELLED`,
  `PICKUP_FAILED`), `lines` (`lineNo`, `catalogItemId`, `quantity`, the caller-declared price and owner ref when
  known), `trackingNumbers`, `authorizedAt`, `expiresAt` (`authorizedAt` + 30 days), `version` and `pricesAbsent`; plus
  `destinationNodeId`, `originNodeId` and the pickup window when they apply.
- **Errors:** an unknown id is `not-found`. The read never writes: an authorization past `expiresAt` is answered as
  stored (still `AUTHORIZED`) until a command touches it (see the known inconsistency below).

## `list_return_authorizations`

Lists one order's return authorizations, newest first.

- **Input:** `orderId` (required, for example `ord-51a0c9`); `limit` (optional, 1..100, default 20); `cursor`
  (optional, see the known inconsistency below).
- **Output:** `items` (each the same shape as `get_return_authorization`) and `nextCursor`.

## Transport

The Streamable HTTP handler is **stateless** ([ADR 0011](/docs/adr/0011-agent-authority-levels-and-complete-mcp-surface)):
no `Mcp-Session-Id` is issued, the server answers the 2026-07-28 revision's per-request handshake (`server/discover`,
no `initialize` needed) and a legacy `initialize` still answers `200`. Pinned by the raw-POST tests in
`internal/adapters/inbound/mcp/stateless_test.go`.

## Resource: one return authorization

`warehouse://customer-returns/return-authorizations/{authorizationId}` (`application/json`, name
`return-authorization`) is the one resource of this server: one authorization (method, state, lines with prices and
owner refs, linked reverse labels, expiry), built from the same read use case as `get_return_authorization`. Bounded by
construction: one authorization, JSON text, never a dump of a table. An unknown id is an error
(`TestResourceUnknownIdIsAnError`).

## Prompt: `review_return_authorization`

The one prompt of this server is the operator SOP for this context: read the authorization and its order's history,
interpret disposition, state and expiry, and know when to escalate (stale transits) to a human. It takes one required
argument (`authorizationId`); it names tools and never calls them, and nothing it names can write (the write operations
of this context are REST-only). `prompts/get` returns the SOP text with the id interpolated.

## Known inconsistencies

- `list_return_authorizations` accepts `cursor` in its input schema, but the handler passes only `orderId` and `limit`
  to the use case (`internal/adapters/inbound/mcp/tools.go`), so every call answers the first page and the cursor is
  ignored. Paging through more than one page works over REST (`GET /return-authorizations?cursor=`) only. Fixing it is
  a code change, not a docs one.
- `apis/openapi.yaml` (and so the generated `getReturnAuthorization` page) says an expired authorization is lazily
  cancelled on the read, but `GetReturnAuthorization` (`internal/application/usecases/queries.go`) only loads and maps
  it; `expireLazily` runs on `IssueLabel`, `CancelReturn` and the outcome consumer only. The spec is a contract file, so
  correcting it is a reviewed change of its own.
- `cmd/mcp` logs `environment` as `dev` when `ENVIRONMENT` is unset, while the telemetry resource says `local`
  ([Configuration](/docs/operations/configuration)).
