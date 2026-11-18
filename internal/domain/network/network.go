// Package network holds the two event-fed read models of the customer-returns
// context (ADR 0003): the OrderGroupView (what may be returned) and the
// ReturnsNetworkView (where a return may go). They are local projections of
// sibling facts; CR never calls a sibling to decide.
package network

import "time"

// GroupStatus is the order group's status as CO published it.
type GroupStatus string

// The statuses CR cares about (CO's statuses; PROMISED is the placed default).
const (
	GroupPromised  GroupStatus = "PROMISED"
	GroupShipped   GroupStatus = "SHIPPED"
	GroupDelivered GroupStatus = "DELIVERED"
	GroupLost      GroupStatus = "LOST"
	GroupReturned  GroupStatus = "RETURNED"
)

// Authorizable reports whether a group in this status may be returned.
func (s GroupStatus) Authorizable() bool { return s == GroupShipped || s == GroupDelivered }

// GroupLine is one order line of the group, as placed.
type GroupLine struct {
	LineNo        int
	CatalogItemID string
	Quantity      int
}

// OrderGroup is one (orderId, groupNo) row of the OrderGroupView.
type OrderGroup struct {
	OrderID                 string
	GroupNo                 int
	Status                  GroupStatus
	DestinationPostalPrefix string
	WitnessNodeID           string
	Lines                   []GroupLine
	UpdatedAt               time.Time
}

// NodeLifecycle is the node state CR tracks (only ACTIVE nodes are usable).
type NodeLifecycle string

// The node lifecycles CR sees (registered nodes start PENDING).
const (
	NodePending   NodeLifecycle = "PENDING"
	NodeActive    NodeLifecycle = "ACTIVE"
	NodeSuspended NodeLifecycle = "SUSPENDED"
	NodeRetired   NodeLifecycle = "RETIRED"
)

// NodeType echoes node-topology's node types (RETURNS_CENTER is the one CR
// routes returns to).
type NodeType string

// The node types CR distinguishes.
const (
	TypeReturnsCenter     NodeType = "RETURNS_CENTER"
	TypeFulfillmentCenter NodeType = "FULFILLMENT_CENTER"
	TypeDeliveryStation   NodeType = "DELIVERY_STATION"
)

// Node is one row of the node half of the ReturnsNetworkView. NodeRegistered
// carries the descriptive attributes (Type, RegionID); the three status facts
// carry only the node id and move Lifecycle. Type and RegionID are empty on a
// row a status fact created before the registration was seen.
type Node struct {
	NodeID    string
	Type      NodeType
	RegionID  string
	Lifecycle NodeLifecycle
	// LifecycleAt is the instant of the status fact that set Lifecycle; the
	// zero time while no status fact was applied.
	LifecycleAt time.Time
}

// PrefixAssignment is the prefix half of the ReturnsNetworkView: which region
// a postal prefix belongs to (highest assignmentVersion wins).
type PrefixAssignment struct {
	PostalPrefix      string
	RegionID          string
	AssignmentVersion int64
}

// GroupOrigin is the origin half of the ReturnsNetworkView: which node
// fulfilled the order group, and whose stock each catalog item took (the
// ownerRef of the release/lock fact; "" when the line was operator-owned).
type GroupOrigin struct {
	OrderID      string
	GroupNo      int
	NodeID       string
	OwnersByItem map[string]string
	UpdatedAt    time.Time
}

// OwnerOf returns the owner ref of the catalog item, "" when unknown or
// operator-owned.
func (o GroupOrigin) OwnerOf(catalogItemID string) string {
	return o.OwnersByItem[catalogItemID]
}

// RegionOf returns the region of the postal prefix, "" when unknown.
func (v *View) RegionOf(prefix string) string {
	return v.prefixes[prefix]
}

// View is the in-memory shape of the ReturnsNetworkView used by tests; the
// Postgres twin answers the same questions with SQL.
type View struct {
	prefixes map[string]string
}

// NewView builds a view over prefix→region assignments.
func NewView(prefixes map[string]string) *View {
	return &View{prefixes: prefixes}
}
