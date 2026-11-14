package returnauthorization

import (
	"time"

	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// EventLine is the wire form of one line on ReturnAuthorized: no price (the
// price is the storefront's fact, never CR's to publish).
type EventLine struct {
	LineNo        int
	CatalogItemID string
	Quantity      int
	OwnerRef      string
}

// Authorized is raised by Authorize for every disposition (KEEP included).
type Authorized struct {
	shared.Header
	AuthorizationID   ID
	OrderID           string
	GroupNo           int
	Reason            Reason
	Disposition       Disposition
	Method            Method
	Lines             []EventLine
	DestinationNodeID string
	OriginNodeID      string
	PickupStart       time.Time
	PickupEnd         time.Time
	ExpiresAt         time.Time
	AuthorizedAt      time.Time
}

// entityName is the single entity segment of every event of this context.
const entityName = "returnauthorization"

// EventName implements shared.Event.
func (Authorized) EventName() string { return "ReturnAuthorized" }

// Entity implements shared.Event.
func (Authorized) Entity() string { return entityName }

// SubjectID implements shared.Event.
func (e Authorized) SubjectID() string { return string(e.AuthorizationID) }

// LabelIssued is raised once per linked reverse label.
type LabelIssued struct {
	shared.Header
	AuthorizationID   ID
	TrackingNumber    string
	DestinationNodeID string
	IssuedAt          time.Time
}

// EventName implements shared.Event.
func (LabelIssued) EventName() string { return "ReturnLabelIssued" }

// Entity implements shared.Event.
func (LabelIssued) Entity() string { return entityName }

// SubjectID implements shared.Event.
func (e LabelIssued) SubjectID() string { return string(e.AuthorizationID) }

// CompletedLine is one graded line of the restock fact.
type CompletedLine struct {
	CatalogItemID  string
	OwnerRef       string
	QtyRestockable int
	QtyUnsellable  int
}

// Completed is the restock fact the offer context consumes.
type Completed struct {
	shared.Header
	AuthorizationID ID
	Outcome         string
	NodeID          string
	Lines           []CompletedLine
	ReceivedAt      time.Time
}

// EventName implements shared.Event.
func (Completed) EventName() string { return "ReturnCompleted" }

// Entity implements shared.Event.
func (Completed) Entity() string { return entityName }

// SubjectID implements shared.Event.
func (e Completed) SubjectID() string { return string(e.AuthorizationID) }

// Cancelled is raised by Cancel (shopper or lazy expiry).
type Cancelled struct {
	shared.Header
	AuthorizationID ID
	Reason          CancelReason
	CancelledAt     time.Time
}

// EventName implements shared.Event.
func (Cancelled) EventName() string { return "ReturnCancelled" }

// Entity implements shared.Event.
func (Cancelled) Entity() string { return entityName }

// SubjectID implements shared.Event.
func (e Cancelled) SubjectID() string { return string(e.AuthorizationID) }

func authorizedEvent(a *ReturnAuthorization, at time.Time) Authorized {
	lines := make([]EventLine, 0, len(a.lines))
	for _, l := range a.lines {
		lines = append(lines, EventLine{
			LineNo: l.LineNo, CatalogItemID: l.CatalogItemID,
			Quantity: l.Quantity, OwnerRef: l.OwnerRef,
		})
	}
	return Authorized{
		Header:          shared.Header{Version: a.version, At: at},
		AuthorizationID: a.id, OrderID: a.orderID, GroupNo: a.groupNo,
		Reason: a.reason, Disposition: a.disposition, Method: a.method,
		Lines: lines, DestinationNodeID: a.destinationNodeID, OriginNodeID: a.originNodeID,
		PickupStart: a.pickupStart, PickupEnd: a.pickupEnd,
		ExpiresAt: a.expiresAt, AuthorizedAt: a.authorizedAt,
	}
}

func labelIssuedEvent(a *ReturnAuthorization, trackingNumber string, at time.Time) LabelIssued {
	return LabelIssued{
		Header:          shared.Header{Version: a.version, At: at},
		AuthorizationID: a.id, TrackingNumber: trackingNumber,
		DestinationNodeID: a.destinationNodeID, IssuedAt: at,
	}
}

func completedEvent(a *ReturnAuthorization, lines []OutcomeLine, nodeID string, at time.Time) Completed {
	out := make([]CompletedLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, CompletedLine(l))
	}
	return Completed{
		Header:          shared.Header{Version: a.version, At: at},
		AuthorizationID: a.id, Outcome: "RECEIVED", NodeID: nodeID,
		Lines: out, ReceivedAt: at,
	}
}

func cancelledEvent(a *ReturnAuthorization, reason CancelReason, at time.Time) Cancelled {
	return Cancelled{
		Header:          shared.Header{Version: a.version, At: at},
		AuthorizationID: a.id, Reason: reason, CancelledAt: at,
	}
}
