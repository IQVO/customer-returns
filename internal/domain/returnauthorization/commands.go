package returnauthorization

import (
	"time"

	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// OutcomeLine is one line of a returns centre's graded outcome.
type OutcomeLine struct {
	CatalogItemID  string
	OwnerRef       string
	QtyRestockable int
	QtyUnsellable  int
}

// ID is the authorization's id.
func (a *ReturnAuthorization) ID() ID { return a.id }

// OrderID is the order the return belongs to.
func (a *ReturnAuthorization) OrderID() string { return a.orderID }

// GroupNo is the order group the return belongs to.
func (a *ReturnAuthorization) GroupNo() int { return a.groupNo }

// Lines returns the authorized lines.
func (a *ReturnAuthorization) Lines() []Line { return cloneLines(a.lines) }

// Reason is the shopper's return reason code.
func (a *ReturnAuthorization) Reason() Reason { return a.reason }

// Disposition is what the shopper does with the goods.
func (a *ReturnAuthorization) Disposition() Disposition { return a.disposition }

// Method is how the goods travel back.
func (a *ReturnAuthorization) Method() Method { return a.method }

// DestinationNodeID is the returns node ("" for KEEP).
func (a *ReturnAuthorization) DestinationNodeID() string { return a.destinationNodeID }

// OriginNodeID is the node that fulfilled the group ("" until a lock fact).
func (a *ReturnAuthorization) OriginNodeID() string { return a.originNodeID }

// PickupWindow is the promised doorstep window (zero unless PICKUP).
func (a *ReturnAuthorization) PickupWindow() (time.Time, time.Time) {
	return a.pickupStart, a.pickupEnd
}

// TrackingNumbers returns the linked reverse-label tracking numbers.
func (a *ReturnAuthorization) TrackingNumbers() []string {
	return append([]string{}, a.trackingNumbers...)
}

// State is the lifecycle state.
func (a *ReturnAuthorization) State() State { return a.state }

// AuthorizedAt is the authorization instant.
func (a *ReturnAuthorization) AuthorizedAt() time.Time { return a.authorizedAt }

// ExpiresAt is authorizedAt + ReturnAuthorizationTTL.
func (a *ReturnAuthorization) ExpiresAt() time.Time { return a.expiresAt }

// ClosedAt is the terminal-state instant (zero while open).
func (a *ReturnAuthorization) ClosedAt() time.Time { return a.closedAt }

// Version is the optimistic-concurrency version.
func (a *ReturnAuthorization) Version() int64 { return a.version }

// PricesAbsent reports whether any line carries no unit price.
func (a *ReturnAuthorization) PricesAbsent() bool {
	for _, l := range a.lines {
		if l.UnitPriceCents == nil {
			return true
		}
	}
	return false
}

// Snapshot is the persisted form (the four-places rule: struct, New, Restore,
// this builder).
func (a *ReturnAuthorization) Snapshot() Snapshot {
	return Snapshot{
		ID: a.id, OrderID: a.orderID, GroupNo: a.groupNo, Lines: cloneLines(a.lines),
		Reason: a.reason, Disposition: a.disposition, Method: a.method,
		DestinationNodeID: a.destinationNodeID, OriginNodeID: a.originNodeID,
		PickupWindowStart: a.pickupStart, PickupWindowEnd: a.pickupEnd,
		TrackingNumbers: a.TrackingNumbers(), State: a.state,
		AuthorizedAt: a.authorizedAt, ExpiresAt: a.expiresAt, ClosedAt: a.closedAt,
		Version: a.version,
	}
}

// Events drains the events raised since the last drain.
func (a *ReturnAuthorization) Events() []shared.Event {
	out := a.events
	a.events = nil
	return out
}

// LinkLabel links one reverse-label tracking number (at most
// MaxPackagesPerReturn) and raises ReturnLabelIssued. Valid while AUTHORIZED
// with a non-KEEP disposition.
func (a *ReturnAuthorization) LinkLabel(trackingNumber string, now time.Time) error {
	if a.state != StateAuthorized {
		return shared.Illegal("%s: label only while AUTHORIZED", a.state)
	}
	if a.disposition == KeepDisposition {
		return shared.Illegal("KEEP links no label")
	}
	if !shared.ValidID(trackingNumber) {
		return shared.Invalid("trackingNumber %q is not a valid tracking number", trackingNumber)
	}
	for _, n := range a.trackingNumbers {
		if n == trackingNumber {
			return shared.Illegal("trackingNumber %q already linked", trackingNumber)
		}
	}
	if len(a.trackingNumbers) >= MaxPackagesPerReturn {
		return shared.Invalid("trackingNumbers exceed MaxPackagesPerReturn %d", MaxPackagesPerReturn)
	}
	a.trackingNumbers = append(a.trackingNumbers, trackingNumber)
	a.version++
	a.events = append(a.events, labelIssuedEvent(a, trackingNumber, shared.UTC(now)))
	return nil
}

// MarkInTransit moves AUTHORIZED to IN_TRANSIT. It raises no event: transit is
// a local fact until the returns centre's outcome arrives.
func (a *ReturnAuthorization) MarkInTransit() error {
	if a.state != StateAuthorized {
		return shared.Illegal("%s: transit only from AUTHORIZED", a.state)
	}
	if a.disposition == KeepDisposition {
		return shared.Illegal("KEEP never transits")
	}
	if len(a.trackingNumbers) == 0 {
		return shared.Illegal("transit requires at least one linked label")
	}
	a.state = StateInTransit
	a.version++
	return nil
}

// ApplyOutcome records the returns centre's graded outcome, moves the
// authorization to RECEIVED and raises ReturnCompleted. Valid from AUTHORIZED
// or IN_TRANSIT (a package may reach the centre before any transit fact).
func (a *ReturnAuthorization) ApplyOutcome(lines []OutcomeLine, nodeID string, now time.Time) error {
	if a.state != StateAuthorized && a.state != StateInTransit {
		return shared.Illegal("%s: outcome only while open", a.state)
	}
	if a.disposition == KeepDisposition {
		return shared.Illegal("KEEP receives no outcome")
	}
	if !shared.ValidID(nodeID) {
		return shared.Invalid("nodeId %q is not a valid id", nodeID)
	}
	if err := checkOutcome(lines, a.authorizedQty()); err != nil {
		return err
	}
	at := shared.UTC(now)
	a.state = StateReceived
	a.closedAt = at
	a.version++
	a.events = append(a.events, completedEvent(a, lines, nodeID, at))
	return nil
}

func (a *ReturnAuthorization) authorizedQty() map[string]int {
	qty := make(map[string]int, len(a.lines))
	for _, l := range a.lines {
		qty[l.CatalogItemID] += l.Quantity
	}
	return qty
}

func checkOutcome(lines []OutcomeLine, authorized map[string]int) error {
	if len(lines) == 0 {
		return shared.Invalid("at least one outcome line is required")
	}
	seen := make(map[string]bool, len(lines))
	for i, l := range lines {
		if !shared.ValidID(l.CatalogItemID) {
			return shared.Invalid("lines[%d].catalogItemId %q is not a valid id", i, l.CatalogItemID)
		}
		if seen[l.CatalogItemID] {
			return shared.Invalid("lines[%d].catalogItemId %q is repeated", i, l.CatalogItemID)
		}
		seen[l.CatalogItemID] = true
		if l.QtyRestockable < 0 || l.QtyUnsellable < 0 {
			return shared.Invalid("lines[%d]: quantities are negative", i)
		}
		if l.QtyRestockable+l.QtyUnsellable < 1 {
			return shared.Invalid("lines[%d]: at least one unit is required", i)
		}
		if l.QtyRestockable+l.QtyUnsellable > authorized[l.CatalogItemID] {
			return shared.Invalid("lines[%d]: outcome exceeds the authorized quantity", i)
		}
	}
	return nil
}

// Cancel cancels the authorization (shopper, before IN_TRANSIT; or the lazy
// expiry) and raises ReturnCancelled.
func (a *ReturnAuthorization) Cancel(reason CancelReason, now time.Time) error {
	if reason != CancelShopperRequested && reason != CancelExpired {
		return shared.Invalid("cancel reason %q is not a cancel reason code", reason)
	}
	if a.state != StateAuthorized {
		return shared.Illegal("%s: cancel only while AUTHORIZED", a.state)
	}
	at := shared.UTC(now)
	a.state = StateCancelled
	a.closedAt = at
	a.version++
	a.events = append(a.events, cancelledEvent(a, reason, at))
	return nil
}

// MarkPickupFailed records the last-mile carrier giving up on the doorstep
// pickup (the third failed attempt). It raises no event: the fact was the
// last-mile context's.
func (a *ReturnAuthorization) MarkPickupFailed(now time.Time) error {
	if a.state != StateAuthorized {
		return shared.Illegal("%s: pickup failure only while AUTHORIZED", a.state)
	}
	if a.method != MethodPickup {
		return shared.Illegal("method %s has no pickup to fail", a.method)
	}
	a.state = StatePickupFailed
	a.closedAt = shared.UTC(now)
	a.version++
	return nil
}
