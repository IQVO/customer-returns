// Package requests carries the typed inputs of the use cases (what the inbound
// adapters decoded) and of the consumers (what the Kafka handlers parsed).
package requests

import (
	"time"

	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// AuthorizeReturn is POST /return-authorizations: the storefront's request to
// authorize one order group's return. unitPriceCents is caller-declared (the
// storefront is the only price knower); an absent price means KEEP is never
// chosen (ADR 0002).
type AuthorizeReturn struct {
	OrderID     string
	GroupNo     int
	Lines       []AuthorizeLine
	Reason      returnauthorization.Reason
	Method      returnauthorization.Method
	PickupStart time.Time
	PickupEnd   time.Time
}

// AuthorizeLine picks one order line by lineNo with its declared unit price.
type AuthorizeLine struct {
	LineNo         int
	UnitPriceCents *int64
}

// ListAuthorizations is GET /return-authorizations?orderId= (keyset).
type ListAuthorizations struct {
	OrderID        string
	AfterCreatedAt time.Time
	AfterID        returnauthorization.ID
	Limit          int
}

// IssueLabel is POST /return-authorizations/{id}/labels.
type IssueLabel struct {
	AuthorizationID returnauthorization.ID
	PackageNo       int
}

// CancelReturn is POST /return-authorizations/{id}/cancellations.
type CancelReturn struct {
	AuthorizationID returnauthorization.ID
}

// OrderPlaced is the parsed CustomerOrderPlaced. The destination postal prefix
// is a fact of the ORDER (customer-ordering publishes one per order, never one
// per group): every group of the order shares it.
type OrderPlaced struct {
	OrderID                 string
	DestinationPostalPrefix string
	PlacedAt                time.Time
	Groups                  []PlacedGroup
}

// PlacedGroup is one group of an OrderPlaced.
type PlacedGroup struct {
	GroupNo       int
	WitnessNodeID string
	Lines         []network.GroupLine
}

// NoticeDecided is the parsed ShopperNoticeDecided (terminal families only
// reach the use case).
type NoticeDecided struct {
	OrderID string
	GroupNo int
	Family  string
	Status  network.GroupStatus
}

// NodeRegistered is the parsed node-topology NodeRegistered: the one node fact
// that carries the node's descriptive attributes. PostalCode is the postal
// code of the node's building as node-topology published it (not personal
// data: it locates a warehouse, never a shopper); the use case keeps only the
// label prefix derived from it.
type NodeRegistered struct {
	NodeID     string
	NodeType   network.NodeType
	RegionID   string
	PostalCode string
}

// NodeStatus is the parsed NodeActivated, NodeSuspended or NodeRetired. The
// producer publishes only the node id (plus the instant) on these three, so
// the fact names a lifecycle and nothing else. At is the CloudEvents time of
// the fact, the instant the status changed.
type NodeStatus struct {
	NodeID    string
	Lifecycle network.NodeLifecycle
	At        time.Time
}

// PrefixFact is the parsed ServiceAreaAssigned.
type PrefixFact struct {
	PostalPrefix      string
	RegionID          string
	AssignmentVersion int64
}

// OriginFact is what a FulfillmentReleasedToNode / FulfillmentLocked says about
// ONE order group. A merged release (one purchase order for several groups)
// parses into one OriginFact per member group.
type OriginFact struct {
	OrderID string
	GroupNo int
	NodeID  string
	Locked  bool
	Owners  map[string]string
}

// OutcomeSubmitted is the parsed VendorReturnOutcomeSubmitted.
type OutcomeSubmitted struct {
	AuthorizationID returnauthorization.ID
	NodeID          string
	ReceivedAt      time.Time
	Lines           []OutcomeLine
}

// OutcomeLine is one line of a submitted outcome.
type OutcomeLine struct {
	CatalogItemID  string
	OwnerRef       string
	QtyRestockable int
	QtyUnsellable  int
}

// IssueReturnLabel is the request body of the shipping context's future
// POST /return-labels route (the ONE in-org call this context makes, ADR 0005).
type IssueReturnLabel struct {
	ReturnAuthorizationID   returnauthorization.ID
	OrderID                 string
	PackageNo               int
	DestinationPostalPrefix string
}

// ReturnLabel is the reverse label the shipping context issued.
type ReturnLabel struct {
	TrackingNumber string
	Carrier        string
}

// OutcomeFact is one recorded outcome with its per-line unexpected flags.
type OutcomeFact struct {
	AuthorizationID returnauthorization.ID
	NodeID          string
	ReceivedAt      time.Time
	Lines           []OutcomeFactLine
}

// OutcomeFactLine is one line of the recorded outcome.
type OutcomeFactLine struct {
	CatalogItemID  string
	OwnerRef       string
	QtyRestockable int
	QtyUnsellable  int
	Unexpected     bool
}
