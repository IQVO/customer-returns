// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the agent ecosystem as a third driving adapter over the same
// application-layer read use cases the REST and MCP adapters use. It is built
// on the official A2A Go SDK (a2aproject/a2a-go/v2) and speaks JSON-RPC 2.0
// over HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0009-a2a-inbound-adapter.md, A2A is the DELEGATION channel
// between agents: a client sends a goal-shaped skill request as a structured
// message part and this agent answers from its own state through its own use
// cases. It is not a data-sync channel: facts still travel exclusively as
// CloudEvents on Kafka (ADR 0004), published through the transactional outbox.
//
// This agent is READ-ONLY, exactly like the MCP adapter (ADR 0006): the one
// skill reads customer-returns data. Authorizing, cancelling and linking
// labels stay on REST and the outbox. This package never writes and contains
// no outbound A2A client.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL agent clients can actually dial
// (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Reads is the read-only slice of the application service the A2A skill
// needs: the same GetReturnAuthorization use case the REST and MCP adapters
// expose; *usecases.Service implements it. The adapter holds this and nothing
// that can write (docs/adr/0009): the composition root wires no write seam at
// all.
type Reads interface {
	GetReturnAuthorization(ctx context.Context, id returnauthorization.ID) (views.ReturnAuthorization, error)
}

// Deps is everything the A2A skill needs, injected by the composition root.
type Deps struct {
	Reads Reads
}

// Card builds this agent's Agent Card (ADR 0009). The surface is ONE curated,
// goal-shaped READ skill derived from the MCP tool get_return_authorization
// (ADR 0006): this context's MCP charter is entirely read-only, so the agent
// is read-only too and never widens what the context exposes. The skill
// carries the tag authority:L1 (Agentic Standard v2, ADR 0011): the same level
// as the tool it fronts. The card declares no security schemes on purpose:
// this surface is unauthenticated by the same fleet-wide decision as REST and
// MCP; schemes are added here, not in a redesign, if that decision is ever
// reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "customer-returns",
		Description: "Customer Returns agent of the retail network (read-only): the shopper-facing front door for returning goods. It answers where one return stands (the authorization with its disposition, method, state, lines, reverse labels and expiry). It never changes data: authorizing, cancelling and linking labels stay on the REST API.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/customer-returns",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{contentTypeJSON, "text/plain"},
		DefaultOutputModes: []string{contentTypeJSON, "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillGetReturnAuthorization,
				Name:        "Read one return authorization",
				Description: "Answer one return authorization as this service recorded it: the order and group, the reason code, the disposition decided at authorization (KEEP returnless, NEAREST_RETURNS_CENTER or ORIGIN_NODE), the method (DROP_OFF, or PICKUP while dormant), the state (AUTHORIZED, IN_TRANSIT, RECEIVED, CLOSED_KEEP, CANCELLED, PICKUP_FAILED), the authorized lines with the caller-declared prices and owner refs, the linked reverse-label tracking numbers, the expiry (authorizedAt + 30d) and pricesAbsent. Arguments: authorizationId (required). Answers a 404 problem for an unknown id. Persists nothing.",
				Tags:        []string{"return", "authorization", "read", "authority:L1"},
				InputModes:  []string{contentTypeJSON},
				Examples: []string{
					`{"skill":"get_return_authorization","arguments":{"authorizationId":"ret-0f3a91"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): the curated skill is a transactional request/response read, so there
// is no long-running Task lifecycle to track in this first A2A surface. The
// response carries the caller's contextId (and taskId when the client
// referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler, the only transport this
// agent ships (ADR 0009), mirroring ADR 0006's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
