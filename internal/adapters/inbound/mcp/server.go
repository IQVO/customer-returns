// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer read use cases the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0006-read-only-mcp-server.md the surface is READ-ONLY: every
// tool reads customer-returns data (get_return_authorization,
// get_shipment_labels, get_inbound_shipment); onboarding a seller, the plan
// lifecycle, packing cartons, labelling and shipping stay on REST and on the
// outbox, and governance_test.go fails the build on a write-verb tool name
// and on a write seam referenced from this package. This package depends
// inward on the application layer and the domain only, never on an outbound
// adapter or the REST adapter. The composition root (cmd/mcp) wires concrete
// repositories into the use cases. There is no auth of any kind (fleet-wide
// revert 2026-09-11): access control is the in-cluster ClusterIP boundary.
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "customer-returns-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Customer Returns of the retail network (read-only): the shopper-facing front door for returning goods. " +
	"Read one return authorization with get_return_authorization: the reason code, the disposition decided at authorization " +
	"(KEEP returnless, NEAREST_RETURNS_CENTER or ORIGIN_NODE), the method (DROP_OFF, or PICKUP while that slice is dormant), the state " +
	"(AUTHORIZED, IN_TRANSIT, RECEIVED, CLOSED_KEEP, CANCELLED, PICKUP_FAILED), the authorized lines with the caller-declared prices and " +
	"owner refs, the linked reverse-label tracking numbers and the expiry (authorizedAt + 30d). List an order's authorizations, newest " +
	"first, with list_return_authorizations. The return authorization resource " +
	"(warehouse://customer-returns/return-authorizations/{authorizationId}) loads one authorization without a tool call, and the " +
	"review_return_authorization prompt hands you this context's operator SOP. Nothing here changes data: authorizing, cancelling and " +
	"linking labels are REST operations of customer-returns."

// NewServer builds the MCP server for this bounded context with every tool,
// resource and workflow prompt registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	deps.registerResources(server)
	deps.registerPrompts(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	// Stateless (Agentic Standard v2, AS-4): no Mcp-Session-Id is issued, so
	// the server also answers the 2026-07-28 revision's per-request
	// handshake (server/discover) while legacy initialize clients keep
	// working. Nothing here relies on cross-call session state.
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
}
