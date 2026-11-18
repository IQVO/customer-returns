// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// sentinels of errors.go.
//
// Optimistic concurrency: every Save of an aggregate expects the stored version
// to be aggregate.Version()-1 (ADR 0002); a create (Version() 1) expects that
// the id is not stored. Any mismatch, including an update of a row that no
// longer exists, is ErrConcurrentUpdate. Inside a UnitOfWork every repository
// joins the transaction carried by the ctx the unit handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// AuthorizationRepository persists ReturnAuthorization aggregates.
type AuthorizationRepository interface {
	// Get returns the authorization, or ErrNotFound.
	Get(ctx context.Context, id returnauthorization.ID) (*returnauthorization.ReturnAuthorization, error)
	// Save persists the authorization under the concurrency rule of the
	// package comment.
	Save(ctx context.Context, a *returnauthorization.ReturnAuthorization) error
	// ListByOrder returns the order's authorizations, newest first
	// (keyset-paged after the (afterCreatedAt, afterID) row the caller last
	// saw), at most limit rows.
	ListByOrder(ctx context.Context, orderID string, afterCreatedAt time.Time, afterID returnauthorization.ID, limit int) ([]*returnauthorization.ReturnAuthorization, error)
}

// GroupViewRepository stores the OrderGroupView read model (ADR 0003): the
// per-(orderId, groupNo) projection of customer-ordering's placed and notice
// facts. Upserts carry no version.
type GroupViewRepository interface {
	// Group returns the order group, or ErrNotFound.
	Group(ctx context.Context, orderID string, groupNo int) (network.OrderGroup, error)
	// SaveGroup inserts or replaces the group row.
	SaveGroup(ctx context.Context, g network.OrderGroup) error
}

// NetworkViewRepository stores the ReturnsNetworkView read model (ADR 0003):
// the node, prefix and origin projections of node-topology's and
// order-sourcing's facts.
type NetworkViewRepository interface {
	// RegisterNode upserts the descriptive attributes of the node (n.Type,
	// n.RegionID, n.LabelPostalPrefix) from a NodeRegistered fact. A node never seen starts
	// PENDING; a node already held keeps its lifecycle and its status
	// instant (n.Lifecycle and n.LifecycleAt are not read), so a registration
	// applied again never demotes an ACTIVE, SUSPENDED or RETIRED node.
	RegisterNode(ctx context.Context, n network.Node) error
	// SetNodeLifecycle applies a status fact that happened at the instant at:
	// it moves the lifecycle and nothing else. A fact older than the one that
	// set the current lifecycle is a no-op. A node never seen gets a
	// lifecycle-only row (no type, no region) that the registration completes.
	SetNodeLifecycle(ctx context.Context, nodeID string, lifecycle network.NodeLifecycle, at time.Time) error
	// Node returns the node row, or ErrNotFound.
	Node(ctx context.Context, nodeID string) (network.Node, error)
	// SavePrefix upserts the prefix row (the caller keeps the highest
	// assignmentVersion; the repository treats a stale write as a no-op).
	SavePrefix(ctx context.Context, p network.PrefixAssignment) error
	// SaveOrigin upserts the group-origin row.
	SaveOrigin(ctx context.Context, o network.GroupOrigin) error
	// ReturnsCenterForRegion returns the region's ACTIVE returns centre with
	// the lowest nodeId, or ErrNotFound when the region has none.
	ReturnsCenterForRegion(ctx context.Context, regionID string) (network.Node, error)
	// RegionOfPrefix returns the region the postal prefix is assigned to, or
	// ErrNotFound when no assignment was seen.
	RegionOfPrefix(ctx context.Context, prefix string) (string, error)
	// Origin returns the group's origin row, or ErrNotFound.
	Origin(ctx context.Context, orderID string, groupNo int) (network.GroupOrigin, error)
}

// OutcomeFacts records the raw outcome a vendor submitted (ADR 0003): one row
// per VendorReturnOutcomeSubmitted, lines beyond the authorization included,
// flagged unexpected -- facts for the audit trail, never re-decided.
type OutcomeFacts interface {
	// Record appends the outcome fact.
	Record(ctx context.Context, f requests.OutcomeFact) error
}

// LabelIssuer is the ONE in-org call this context makes (the reverse-label
// route of the shipping context, ADR 0005): CR asks for the shopper's reverse
// label and links the tracking number it gets back.
type LabelIssuer interface {
	// IssueReturnLabel asks for the reverse label of one package. The port
	// returns ErrLabelUnavailable while the route is not live (or is not
	// configured): the caller answers 422 label-unavailable, never a 500.
	IssueReturnLabel(ctx context.Context, in requests.IssueReturnLabel) (requests.ReturnLabel, error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls, so the aggregates and their raised events commit or roll back
// together. The adapter encodes each event into its CloudEvents envelope (the
// id is minted once, at append) and stores one row per event in order.
// Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// ProcessedEvents is the consumers' inbox (table processed_events).
type ProcessedEvents interface {
	// Claim records the CloudEvents id and reports whether it was new. A false
	// answer means the event was processed before and must be skipped. Inside a
	// UnitOfWork the claim commits or rolls back with the effect.
	Claim(ctx context.Context, eventID, eventType string, at time.Time) (bool, error)
}

// UnitOfWork runs fn as ONE atomic unit: every repository and outbox call fn
// makes with the ctx it is handed commits together when fn returns nil, or
// rolls back together when fn returns an error (or panics).
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock is the service's notion of "now".
type Clock interface {
	Now() time.Time
}

// ReturnMetrics is the Tier 2 business counter of the context (ADR 0008):
// every authorize attempt counted once by outcome, every applied return
// outcome counted with its unexpected-line count. It is fire-and-forget; the
// zero Deps value gets a no-op (usecases.Deps.Metrics).
type ReturnMetrics interface {
	// ReturnAuthorized counts an authorize that produced a disposition.
	ReturnAuthorized(ctx context.Context, disposition string)
	// ReturnRejected counts an authorize attempt that answered a 422; reason
	// is the problem slug of apis/openapi.yaml (internal-error for any other).
	ReturnRejected(ctx context.Context, reason string)
	// OutcomeApplied counts a graded outcome applied to an authorization.
	OutcomeApplied(ctx context.Context, unexpectedLines int)
}

// IDGenerator mints aggregate ids.
type IDGenerator interface {
	// NewID returns prefix + "-" + a random UUID, e.g. "ret-3f6c...".
	NewID(prefix string) string
}
