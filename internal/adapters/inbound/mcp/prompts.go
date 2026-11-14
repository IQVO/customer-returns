package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// reviewReturnSOP is the operator standard-operating-procedure the
// review_return_authorization prompt hands to the model (Agentic Standard
// v2, AS-3). Per the charter, prompts encode how to interpret results, when
// to escalate and what "done" means; this one names tools but NEVER calls
// them, and it cannot write: every tool it names is read-only (ADR 0006) and
// every write of this context is a REST operation.
const reviewReturnSOP = `You are reviewing one return authorization of the retail network. Use only the MCP tools of customer-returns; never assume state — read it.

Procedure:
1. Call get_return_authorization(authorizationId = AUTHORIZATION). The view is this service's own recorded facts: the reason code, the disposition decided at authorization, the method, the state, the authorized lines with the caller-declared unit prices, the linked reverse-label tracking numbers and the expiry (authorizedAt + 30 days).
2. For the order's history, call list_return_authorizations(orderId = the authorization's orderId): re-authorizations after a PICKUP_FAILED and repeated KEEP returns of the same order both show there.

Interpretation:
- disposition KEEP with pricesAbsent true is impossible by construction: KEEP requires every price. pricesAbsent true means a physical return (NEAREST_RETURNS_CENTER or ORIGIN_NODE) was chosen because a price was unknown.
- state AUTHORIZED with expiresAt in the past is a lazy-expiry candidate: the next touch cancels it as EXPIRED, no sweeper runs.
- destinationNodeId is the returns centre (NEAREST_RETURNS_CENTER) or the fulfilling node (ORIGIN_NODE); it is omitted for KEEP.

Escalate to the human returns operator when: a PICKUP_FAILED authorization has no re-authorization, or an IN_TRANSIT return's outcome has not arrived within a week. Authorizing, cancelling, linking labels and recording outcomes are REST operations — nothing in this server can perform them, so do not promise them.

Done means: you have reported the authorization's disposition, state, lines and expiry as recorded, its order's authorization history, and named the escalation when the state warrants it.`

// registerPrompts adds the workflow prompt (the operator SOP of this
// context). Read-only by construction: a prompt returns text.
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "review_return_authorization",
		Description: "Standard operating procedure for reviewing one return authorization: read the authorization and its order's history, interpret the disposition and state, and know when to escalate to the human returns operator.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "authorizationId",
			Description: "the return authorization id to review, e.g. ret-0f3a91",
			Required:    true,
		}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		id := strings.TrimSpace(req.Params.Arguments["authorizationId"])
		if id == "" {
			id = "<authorizationId>"
		}
		return &mcp.GetPromptResult{
			Description: "How to review a return authorization: read it and its order's history, interpret disposition, state and expiry, escalate stale transits.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: strings.ReplaceAll(reviewReturnSOP, "AUTHORIZATION", id)},
			}},
		}, nil
	})
}
