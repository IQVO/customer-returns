package returnauthorization

import (
	"fmt"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// checkSnapshot enforces every invariant of the aggregate (ADR 0002). It is
// the single validator behind Authorize and Restore.
func checkSnapshot(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) {
		return fmt.Errorf("authorizationId %q is not a valid id", s.ID)
	}
	if !shared.ValidID(s.OrderID) {
		return fmt.Errorf("orderId %q is not a valid id", s.OrderID)
	}
	if s.GroupNo < 1 {
		return fmt.Errorf("groupNo %d is below 1", s.GroupNo)
	}
	if !s.Reason.Valid() {
		return fmt.Errorf("reason %q is not a return reason code", s.Reason)
	}
	if !s.Disposition.Valid() {
		return fmt.Errorf("disposition %q is not a disposition", s.Disposition)
	}
	if !s.Method.Valid() {
		return fmt.Errorf("method %q is not a return method", s.Method)
	}
	if err := checkLines(s.Lines); err != nil {
		return err
	}
	switch s.State {
	case StateAuthorized, StateInTransit, StateReceived, StateClosedKeep, StateCancelled, StatePickupFailed:
	default:
		return fmt.Errorf("state %q is not a authorization state", s.State)
	}
	if s.AuthorizedAt.IsZero() {
		return fmt.Errorf("authorizedAt is required")
	}
	if !s.ExpiresAt.Equal(s.AuthorizedAt.Add(ReturnAuthorizationTTL)) {
		return fmt.Errorf("expiresAt must be authorizedAt + %s", ReturnAuthorizationTTL)
	}
	if err := checkDispositionRules(s); err != nil {
		return err
	}
	if err := checkTracking(s.TrackingNumbers); err != nil {
		return err
	}
	return checkTerminalRules(s)
}

func checkLines(lines []Line) error {
	if len(lines) < 1 {
		return fmt.Errorf("at least one line is required")
	}
	if len(lines) > MaxLinesPerReturn {
		return fmt.Errorf("%d lines exceed MaxLinesPerReturn %d", len(lines), MaxLinesPerReturn)
	}
	seen := make(map[int]bool, len(lines))
	for i, l := range lines {
		if l.LineNo < 1 {
			return fmt.Errorf("lines[%d].lineNo %d is below 1", i, l.LineNo)
		}
		if seen[l.LineNo] {
			return fmt.Errorf("lines[%d].lineNo %d is repeated", i, l.LineNo)
		}
		seen[l.LineNo] = true
		if !shared.ValidID(l.CatalogItemID) {
			return fmt.Errorf("lines[%d].catalogItemId %q is not a valid id", i, l.CatalogItemID)
		}
		if l.Quantity < 1 {
			return fmt.Errorf("lines[%d].quantity %d is below 1", i, l.Quantity)
		}
		if l.UnitPriceCents != nil && *l.UnitPriceCents < 0 {
			return fmt.Errorf("lines[%d].unitPriceCents %d is negative", i, *l.UnitPriceCents)
		}
		if l.OwnerRef != "" && !shared.ValidOwnerRef(l.OwnerRef) {
			return fmt.Errorf("lines[%d].ownerRef %q is not a valid owner ref", i, l.OwnerRef)
		}
	}
	return nil
}

func checkDispositionRules(s Snapshot) error {
	switch s.Disposition {
	case KeepDisposition:
		if s.DestinationNodeID != "" {
			return fmt.Errorf("KEEP has no destination node")
		}
		if s.State != StateClosedKeep && s.State != StateCancelled {
			return fmt.Errorf("KEEP closes at once, state %q is not CLOSED_KEEP", s.State)
		}
	case NearestReturnsCenter, OriginNode:
		if !shared.ValidID(s.DestinationNodeID) {
			return fmt.Errorf("disposition %s requires a destination node", s.Disposition)
		}
		if s.State == StateClosedKeep {
			return fmt.Errorf("state CLOSED_KEEP is only for KEEP")
		}
	}
	if s.Disposition == OriginNode && s.OriginNodeID != "" && s.OriginNodeID != s.DestinationNodeID {
		return fmt.Errorf("ORIGIN_NODE destination must be the origin node")
	}
	if s.Method == MethodPickup {
		if s.Disposition == KeepDisposition {
			return fmt.Errorf("PICKUP requires a disposition other than KEEP")
		}
		if s.PickupWindowStart.IsZero() || s.PickupWindowEnd.IsZero() {
			return fmt.Errorf("PICKUP requires a pickup window")
		}
		if !s.PickupWindowStart.Before(s.PickupWindowEnd) {
			return fmt.Errorf("pickup window start must be before end")
		}
		if s.PickupWindowStart.Before(s.AuthorizedAt) || s.PickupWindowEnd.After(s.ExpiresAt) {
			return fmt.Errorf("pickup window must be inside [authorizedAt, expiresAt]")
		}
	}
	if s.Method == MethodDropOff && (!s.PickupWindowStart.IsZero() || !s.PickupWindowEnd.IsZero()) {
		return fmt.Errorf("DROP_OFF carries no pickup window")
	}
	return nil
}

func checkTracking(numbers []string) error {
	if len(numbers) > MaxPackagesPerReturn {
		return fmt.Errorf("%d tracking numbers exceed MaxPackagesPerReturn %d", len(numbers), MaxPackagesPerReturn)
	}
	seen := make(map[string]bool, len(numbers))
	for i, n := range numbers {
		if !shared.ValidID(n) {
			return fmt.Errorf("trackingNumbers[%d] %q is not a valid tracking number", i, n)
		}
		if seen[n] {
			return fmt.Errorf("trackingNumbers[%d] %q is repeated", i, n)
		}
		seen[n] = true
	}
	return nil
}

func checkTerminalRules(s Snapshot) error {
	terminal := s.State == StateReceived || s.State == StateClosedKeep ||
		s.State == StateCancelled || s.State == StatePickupFailed
	if terminal && s.ClosedAt.IsZero() {
		return fmt.Errorf("state %s requires closedAt", s.State)
	}
	if !terminal && !s.ClosedAt.IsZero() {
		return fmt.Errorf("state %s must not carry closedAt", s.State)
	}
	if s.State == StateInTransit && len(s.TrackingNumbers) == 0 {
		return fmt.Errorf("IN_TRANSIT requires at least one linked label")
	}
	if s.State == StateReceived && s.Disposition == KeepDisposition {
		return fmt.Errorf("KEEP never reaches RECEIVED")
	}
	return nil
}

// Expired reports whether the authorization's TTL has passed at now.
func (a *ReturnAuthorization) Expired(now time.Time) bool {
	if a.state != StateAuthorized {
		return false
	}
	return now.After(a.expiresAt)
}
