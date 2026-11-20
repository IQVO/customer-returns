---
id: index
title: Architecture decision records
sidebar_label: ADR index
slug: /
sidebar_position: 0
---

# Architecture decision records

The decisions of `customer-returns`, in the order they were taken. The ADR bodies
are immutable; a changed decision gets a new ADR that supersedes the old one.
The *Status* column is copied from each ADR's own `## Status` section.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](./0001-customer-returns-bounded-context.md) | Customer Returns is a bounded context of the retail network | Accepted (bootstrap) |
| [0002](./0002-aggregates-and-invariants.md) | ReturnAuthorization: aggregate, invariants and the assumptions the model left open | Accepted (2026-11-14) |
| [0003](./0003-consumed-contracts-and-local-read-models.md) | Consumed contracts and local read models | Accepted (2026-11-14) |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-11-14) |
| [0005](./0005-the-reverse-label-route.md) | The reverse-label route: the one in-org call | Accepted (2026-11-14) |
| [0006](./0006-read-only-mcp-server.md) | A read-only MCP server | Accepted (2026-11-14) |
| [0008](./0008-fleet-observability-standard.md) | Adopt the fleet observability standard (logs, metrics, traces) | Accepted (2026-10-26) |
| [0009](./0009-a2a-inbound-adapter.md) | A2A inbound adapter | Accepted (2026-11-14) |
| [0010](./0010-fleet-rest-api-guideline.md) | Adopt the fleet REST API guideline | Accepted (2026-11-01) |
| [0011](./0011-agent-authority-levels-and-complete-mcp-surface.md) | Agent authority levels and complete MCP surface | Accepted (2026-11-02) |
| [0012](./0012-analytics-read-side-and-return-rate-facts.md) | Analytics read side and return-rate facts | Accepted (2026-11-17) |
| [0013](./0013-idempotency-replay-classes.md) | Every problem type has a replay class; a retryable refusal is not stored | Accepted (2026-11-19) |
