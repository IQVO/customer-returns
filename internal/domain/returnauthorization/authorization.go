// Package returnauthorization is the ReturnAuthorization aggregate: the
// shopper-facing authorization of one order group's return, with the
// disposition decided AT authorization (ADR 0002).
//
// States: AUTHORIZED (initial), IN_TRANSIT, RECEIVED, CLOSED_KEEP (the
// returnless KEEP closes at once), CANCELLED and PICKUP_FAILED. RECEIVED,
// CLOSED_KEEP, CANCELLED and PICKUP_FAILED are terminal.
package returnauthorization

import (
	"errors"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/shared"
)

const (
	// MaxLinesPerReturn is the most lines one return authorizes.
	MaxLinesPerReturn = 50
	// MaxPackagesPerReturn is the most packages (reverse labels) one return links.
	MaxPackagesPerReturn = 5
	// ReturnAuthorizationTTL is how long an authorization stays valid.
	ReturnAuthorizationTTL = 30 * 24 * time.Hour
)

// ID identifies an authorization (authorizationId, "ret-<uuid>").
type ID string

// State is the lifecycle state of an authorization.
type State string

// The six states of an authorization.
const (
	StateAuthorized   State = "AUTHORIZED"
	StateInTransit    State = "IN_TRANSIT"
	StateReceived     State = "RECEIVED"
	StateClosedKeep   State = "CLOSED_KEEP"
	StateCancelled    State = "CANCELLED"
	StatePickupFailed State = "PICKUP_FAILED"
)

// Reason is why the shopper returns the lines: codes only, never free text.
type Reason string

// The five return reasons.
const (
	ReasonDefective      Reason = "DEFECTIVE"
	ReasonNotAsDescribed Reason = "NOT_AS_DESCRIBED"
	ReasonWrongItem      Reason = "WRONG_ITEM"
	ReasonNotNeeded      Reason = "NOT_NEEDED"
	ReasonOther          Reason = "OTHER"
)

// Valid reports whether r is one of the five reasons.
func (r Reason) Valid() bool {
	switch r {
	case ReasonDefective, ReasonNotAsDescribed, ReasonWrongItem, ReasonNotNeeded, ReasonOther:
		return true
	default:
		return false
	}
}

// Disposition is what the shopper does with the goods.
type Disposition string

// The three dispositions.
const (
	// KeepDisposition is the returnless refund: no destination, no label.
	KeepDisposition Disposition = "KEEP"
	// NearestReturnsCenter sends the goods to the region's returns centre.
	NearestReturnsCenter Disposition = "NEAREST_RETURNS_CENTER"
	// OriginNode sends the goods back to the node that fulfilled the group.
	OriginNode Disposition = "ORIGIN_NODE"
)

// Valid reports whether d is one of the three dispositions.
func (d Disposition) Valid() bool {
	switch d {
	case KeepDisposition, NearestReturnsCenter, OriginNode:
		return true
	default:
		return false
	}
}

// Method is how the goods travel back.
type Method string

// The two return methods. PICKUP is activation-gated (dormant in v1).
const (
	MethodDropOff Method = "DROP_OFF"
	MethodPickup  Method = "PICKUP"
)

// Valid reports whether m is one of the two methods.
func (m Method) Valid() bool { return m == MethodDropOff || m == MethodPickup }

// CancelReason is why an authorization was cancelled.
type CancelReason string

// The two cancel reasons.
const (
	// CancelShopperRequested is the shopper cancelling before transit.
	CancelShopperRequested CancelReason = "SHOPPER_REQUESTED"
	// CancelExpired is the lazy expiry of an untouched authorization.
	CancelExpired CancelReason = "EXPIRED"
)

// Line is one authorized order line with its caller-declared paid unit price.
type Line struct {
	LineNo         int
	CatalogItemID  string
	Quantity       int
	UnitPriceCents *int64
	OwnerRef       string
}

// Snapshot is the persisted form of an authorization. Restore validates it.
type Snapshot struct {
	ID                ID
	OrderID           string
	GroupNo           int
	Lines             []Line
	Reason            Reason
	Disposition       Disposition
	Method            Method
	DestinationNodeID string
	OriginNodeID      string
	PickupWindowStart time.Time
	PickupWindowEnd   time.Time
	TrackingNumbers   []string
	State             State
	AuthorizedAt      time.Time
	ExpiresAt         time.Time
	ClosedAt          time.Time
	Version           int64
}

// NewAuthorization is the input of Authorize: everything the use case decided.
type NewAuthorization struct {
	ID                ID
	OrderID           string
	GroupNo           int
	Lines             []Line
	Reason            Reason
	Disposition       Disposition
	Method            Method
	DestinationNodeID string
	OriginNodeID      string
	PickupWindowStart time.Time
	PickupWindowEnd   time.Time
	AuthorizedAt      time.Time
}

// ReturnAuthorization is the aggregate root.
type ReturnAuthorization struct {
	id                ID
	orderID           string
	groupNo           int
	lines             []Line
	reason            Reason
	disposition       Disposition
	method            Method
	destinationNodeID string
	originNodeID      string
	pickupStart       time.Time
	pickupEnd         time.Time
	trackingNumbers   []string
	state             State
	authorizedAt      time.Time
	expiresAt         time.Time
	closedAt          time.Time
	version           int64
	events            []shared.Event
}

// ErrNoReturnDestination is what Authorize returns when no destination is
// known for the goods (problem type no-return-destination, 422).
var ErrNoReturnDestination = errors.New("no return destination")

// Authorize builds a fresh authorization, deciding nothing itself: the use
// case ran Decide with the read models and passes the outcome in. KEEP closes
// the return at once (state CLOSED_KEEP); every other disposition starts
// AUTHORIZED. Raises ReturnAuthorized.
func Authorize(in NewAuthorization, now time.Time) (*ReturnAuthorization, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	authorizedAt := shared.UTC(now)
	expiresAt := authorizedAt.Add(ReturnAuthorizationTTL)
	method := in.Method
	if method == "" {
		method = MethodDropOff
	}
	state := StateAuthorized
	if in.Disposition == KeepDisposition {
		state = StateClosedKeep
	}
	a, err := build(Snapshot{
		ID: in.ID, OrderID: in.OrderID, GroupNo: in.GroupNo, Lines: in.Lines,
		Reason: in.Reason, Disposition: in.Disposition, Method: method,
		DestinationNodeID: in.DestinationNodeID, OriginNodeID: in.OriginNodeID,
		PickupWindowStart: in.PickupWindowStart, PickupWindowEnd: in.PickupWindowEnd,
		TrackingNumbers: []string{}, State: state,
		AuthorizedAt: authorizedAt, ExpiresAt: expiresAt,
		ClosedAt: closedKeepAt(state, authorizedAt), Version: 1,
	})
	if err != nil {
		return nil, shared.Invalid("%v", err)
	}
	a.events = append(a.events, authorizedEvent(a, authorizedAt))
	return a, nil
}

func closedKeepAt(state State, at time.Time) time.Time {
	if state == StateClosedKeep {
		return at
	}
	return time.Time{}
}

// Restore rebuilds a persisted authorization, validating every invariant.
func Restore(s Snapshot) (*ReturnAuthorization, error) {
	a, err := build(s)
	if err != nil {
		return nil, shared.Corrupt("%v", err)
	}
	return a, nil
}

func build(s Snapshot) (*ReturnAuthorization, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, err
	}
	return &ReturnAuthorization{
		id: s.ID, orderID: s.OrderID, groupNo: s.GroupNo, lines: cloneLines(s.Lines),
		reason: s.Reason, disposition: s.Disposition, method: s.Method,
		destinationNodeID: s.DestinationNodeID, originNodeID: s.OriginNodeID,
		pickupStart: shared.UTC(s.PickupWindowStart), pickupEnd: shared.UTC(s.PickupWindowEnd),
		trackingNumbers: append([]string{}, s.TrackingNumbers...), state: s.State,
		authorizedAt: shared.UTC(s.AuthorizedAt), expiresAt: shared.UTC(s.ExpiresAt),
		closedAt: shared.UTC(s.ClosedAt), version: s.Version,
	}, nil
}

func cloneLines(in []Line) []Line {
	out := make([]Line, len(in))
	copy(out, in)
	return out
}
