package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

// authorizationURITemplate is the ONE resource this server offers (Agentic
// Standard v2, AS-3): the return authorization read model, loadable without a
// tool call.
const authorizationURITemplate = "warehouse://customer-returns/return-authorizations/{authorizationId}"

// registerResources adds the resource template (a read; resources cannot
// write by construction).
func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: authorizationURITemplate,
		Name:        "return-authorization",
		Description: "One return authorization: disposition, method, state, lines with prices and owner refs, linked reverse labels, expiry.",
		MIMEType:    mimeTypeJSON,
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id := resourceAuthorizationID(req.Params.URI)
		view, err := d.Reads.GetReturnAuthorization(context.Background(), returnauthorization.ID(id))
		if err != nil {
			return nil, mapError(context.Background(), err)
		}
		encoded, err := json.Marshal(authorizationPayload(view))
		if err != nil {
			return nil, mapError(context.Background(), err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: mimeTypeJSON,
				Text:     string(encoded),
			}},
		}, nil
	})
}

func resourceAuthorizationID(uri string) string {
	at := strings.LastIndex(uri, "/")
	if at < 0 || at+1 >= len(uri) {
		return ""
	}
	return uri[at+1:]
}

// authorizationPayload renders the view as the map the tool result carries
// (camelCase like the REST contract; optional fields omitted when they do not
// apply, arrays always present).
func authorizationPayload(v views.ReturnAuthorization) map[string]any {
	lines := make([]map[string]any, 0, len(v.Lines))
	for _, l := range v.Lines {
		line := map[string]any{
			"lineNo": l.LineNo, "catalogItemId": l.CatalogItemID, "quantity": l.Quantity,
		}
		if l.UnitPriceCents != nil {
			line["unitPriceCents"] = *l.UnitPriceCents
		}
		if l.OwnerRef != "" {
			line["ownerRef"] = l.OwnerRef
		}
		lines = append(lines, line)
	}
	out := map[string]any{
		"authorizationId": string(v.AuthorizationID), "orderId": v.OrderID, "groupNo": v.GroupNo,
		"reason": string(v.Reason), "disposition": string(v.Disposition), "method": string(v.Method),
		"state": string(v.State), "lines": lines, "trackingNumbers": v.TrackingNumbers,
		"authorizedAt": rfc3339(v.AuthorizedAt), "expiresAt": rfc3339(v.ExpiresAt),
		"version": v.Version, "pricesAbsent": v.PricesAbsent,
	}
	if v.DestinationNodeID != "" {
		out["destinationNodeId"] = v.DestinationNodeID
	}
	if v.OriginNodeID != "" {
		out["originNodeId"] = v.OriginNodeID
	}
	if !v.PickupWindowStart.IsZero() {
		out["pickupWindowStart"] = rfc3339(v.PickupWindowStart)
		out["pickupWindowEnd"] = rfc3339(v.PickupWindowEnd)
	}
	if !v.ClosedAt.IsZero() {
		out["closedAt"] = rfc3339(v.ClosedAt)
	}
	if v.TrackingNumbers == nil {
		out["trackingNumbers"] = []string{}
	}
	return out
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
