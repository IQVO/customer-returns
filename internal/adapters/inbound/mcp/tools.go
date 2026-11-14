package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// Reads is the read-only slice of the application service: EXACTLY the two
// read use cases the REST adapter exposes (GET /return-authorizations/{id} and
// GET /return-authorizations); *usecases.Service implements it. The adapter
// holds this and nothing that can write (docs/adr/0006): the composition root
// (cmd/mcp) wires no write seam at all.
type Reads interface {
	GetReturnAuthorization(ctx context.Context, id returnauthorization.ID) (views.ReturnAuthorization, error)
	ListReturnAuthorizations(ctx context.Context, req requests.ListAuthorizations) (views.AuthorizationPage, error)
}

// Deps is everything the MCP tools need, injected by the composition root.
type Deps struct {
	Reads Reads
}

// authorizationIDInput mirrors GET /return-authorizations/{id}: the path
// parameter.
type authorizationIDInput struct {
	AuthorizationID string `json:"authorizationId" jsonschema:"the return authorization id, e.g. ret-0f3a91"`
}

// listInput mirrors GET /return-authorizations: the orderId query parameter
// plus the optional keyset page.
type listInput struct {
	OrderID string `json:"orderId" jsonschema:"the order whose return authorizations to list, e.g. ord-51a0c9"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"optional keyset cursor from the previous page's nextCursor; omitted means the first page"`
	Limit   int    `json:"limit,omitempty" jsonschema:"optional page size, 1..100; omitted means 20"`
}

// registerTools adds the two read-only tools, one per read GET of
// apis/openapi.yaml. Every tool is annotated read-only, idempotent and
// closed-world (the annotation literal is inlined per tool so the fleet's
// static authority sensor can prove ReadOnlyHint) and declares its authority
// level L1 under com.warehouse/authority (Agentic Standard v2, ADR 0011):
// each is a pure read of this context's own state. TestToolSurface pins the
// set and fails the build on any write-verb name (docs/adr/0006).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false

	addTool(server, &mcp.Tool{
		Name: "get_return_authorization",
		Description: "Read one return authorization: the order and group it belongs to, the reason code, the disposition decided at " +
			"authorization (KEEP returnless, NEAREST_RETURNS_CENTER or ORIGIN_NODE), the method (DROP_OFF, or PICKUP while that slice " +
			"is dormant), the state (AUTHORIZED, IN_TRANSIT, RECEIVED, CLOSED_KEEP, CANCELLED, PICKUP_FAILED), the authorized lines " +
			"with the caller-declared unit prices and owner refs, the linked reverse-label tracking numbers, the expiry (authorizedAt " +
			"+ 30d) and pricesAbsent (true when a price was absent, so KEEP could never be chosen). Read-only; an unknown id fails " +
			"with not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getReturnAuthorization)

	addTool(server, &mcp.Tool{
		Name: "list_return_authorizations",
		Description: "List a customer order's return authorizations, newest first, keyset-paged (nextCursor of a page feeds the " +
			"next call's cursor). Every item is the same shape as get_return_authorization. Read-only; orderId is required.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listReturnAuthorizations)
}

func (d Deps) getReturnAuthorization(ctx context.Context, in authorizationIDInput) (any, error) {
	view, err := d.Reads.GetReturnAuthorization(ctx, returnauthorization.ID(in.AuthorizationID))
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return authorizationPayload(view), nil
}

func (d Deps) listReturnAuthorizations(ctx context.Context, in listInput) (any, error) {
	page, err := d.Reads.ListReturnAuthorizations(ctx, requests.ListAuthorizations{
		OrderID: in.OrderID, Limit: in.Limit,
	})
	if err != nil {
		return nil, mapError(ctx, err)
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, authorizationPayload(item))
	}
	return map[string]any{"items": items, "nextCursor": page.NextCursor}, nil
}

func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
