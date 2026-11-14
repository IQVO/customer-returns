// Package views carries the read-side answers of the use cases: the shape the
// REST, MCP and A2A adapters serialize. Optional fields are omitted when they
// do not apply -- absence is the fact, never a default value.
package views

import (
	"time"

	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// ReturnAuthorization is one authorization's view.
type ReturnAuthorization struct {
	AuthorizationID   returnauthorization.ID
	OrderID           string
	GroupNo           int
	Reason            returnauthorization.Reason
	Disposition       returnauthorization.Disposition
	Method            returnauthorization.Method
	State             returnauthorization.State
	Lines             []Line
	DestinationNodeID string
	OriginNodeID      string
	PickupWindowStart time.Time
	PickupWindowEnd   time.Time
	TrackingNumbers   []string
	AuthorizedAt      time.Time
	ExpiresAt         time.Time
	ClosedAt          time.Time
	Version           int64
	PricesAbsent      bool
}

// Line is one authorized line of the view.
type Line struct {
	LineNo         int
	CatalogItemID  string
	Quantity       int
	UnitPriceCents *int64
	OwnerRef       string
}

// AuthorizationPage is one keyset page of an order's authorizations.
type AuthorizationPage struct {
	Items      []ReturnAuthorization
	NextCursor string
}

// FromAuthorization projects the aggregate into its view.
func FromAuthorization(a *returnauthorization.ReturnAuthorization) ReturnAuthorization {
	lines := make([]Line, 0, len(a.Lines()))
	for _, l := range a.Lines() {
		lines = append(lines, Line{
			LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity,
			UnitPriceCents: l.UnitPriceCents, OwnerRef: l.OwnerRef,
		})
	}
	s, e := a.PickupWindow()
	return ReturnAuthorization{
		AuthorizationID: a.ID(), OrderID: a.OrderID(), GroupNo: a.GroupNo(),
		Reason: a.Reason(), Disposition: a.Disposition(), Method: a.Method(),
		State: a.State(), Lines: lines,
		DestinationNodeID: a.DestinationNodeID(), OriginNodeID: a.OriginNodeID(),
		PickupWindowStart: s, PickupWindowEnd: e,
		TrackingNumbers: a.TrackingNumbers(), AuthorizedAt: a.AuthorizedAt(),
		ExpiresAt: a.ExpiresAt(), ClosedAt: a.ClosedAt(),
		Version: a.Version(), PricesAbsent: a.PricesAbsent(),
	}
}
