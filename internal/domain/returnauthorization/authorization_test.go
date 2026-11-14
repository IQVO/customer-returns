package returnauthorization

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/shared"
)

var now = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func price(c int64) *int64 { return &c }

func newAuth(t *testing.T, d Disposition, m Method) *ReturnAuthorization {
	t.Helper()
	a, err := Authorize(NewAuthorization{
		ID: "ret-0001", OrderID: "ord-1", GroupNo: 1,
		Lines:  []Line{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2, UnitPriceCents: price(400), OwnerRef: "sel-42"}},
		Reason: ReasonDefective, Disposition: d, Method: m,
		DestinationNodeID: destinationFor(d), OriginNodeID: "node-fc-1",
		PickupWindowStart: pickupStart(m), PickupWindowEnd: pickupEnd(m),
		AuthorizedAt: now,
	}, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	return a
}

func destinationFor(d Disposition) string {
	if d == KeepDisposition {
		return ""
	}
	if d == NearestReturnsCenter {
		return "node-rc-1"
	}
	return "node-fc-1"
}

func pickupStart(m Method) time.Time {
	if m != MethodPickup {
		return time.Time{}
	}
	return now.Add(24 * time.Hour)
}

func pickupEnd(m Method) time.Time {
	if m != MethodPickup {
		return time.Time{}
	}
	return now.Add(48 * time.Hour)
}

func TestAuthorizeRaisesAuthorizedAndComputesExpiry(t *testing.T) {
	a := newAuth(t, NearestReturnsCenter, MethodDropOff)
	if a.State() != StateAuthorized {
		t.Fatalf("state = %s", a.State())
	}
	if !a.ExpiresAt().Equal(now.Add(ReturnAuthorizationTTL)) {
		t.Fatalf("expiresAt = %s", a.ExpiresAt())
	}
	evs := a.Events()
	if len(evs) != 1 || evs[0].EventName() != "ReturnAuthorized" {
		t.Fatalf("events = %v", evs)
	}
	if evs[0].SubjectID() != "ret-0001" || evs[0].Entity() != "returnauthorization" {
		t.Fatalf("subject/entity = %s/%s", evs[0].SubjectID(), evs[0].Entity())
	}
}

func TestAuthorizeKeepClosesAtOnce(t *testing.T) {
	a := newAuth(t, KeepDisposition, MethodDropOff)
	if a.State() != StateClosedKeep {
		t.Fatalf("state = %s", a.State())
	}
	if !a.ClosedAt().Equal(now) {
		t.Fatalf("closedAt = %s", a.ClosedAt())
	}
	if err := a.LinkLabel("trk-1", now); err == nil {
		t.Fatal("KEEP linked a label")
	}
}

func TestLinkLabelLimitsAndDuplicates(t *testing.T) {
	a := newAuth(t, OriginNode, MethodDropOff)
	a.Events()
	for i := 0; i < MaxPackagesPerReturn; i++ {
		if err := a.LinkLabel(idN("trk-", i+1), now); err != nil {
			t.Fatalf("LinkLabel %d: %v", i+1, err)
		}
	}
	if err := a.LinkLabel("trk-6", now); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("sixth label err = %v", err)
	}
	if err := a.LinkLabel("trk-1", now); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("duplicate err = %v", err)
	}
	evs := a.Events()
	if len(evs) != MaxPackagesPerReturn {
		t.Fatalf("events = %d", len(evs))
	}
	for _, e := range evs {
		if e.EventName() != "ReturnLabelIssued" {
			t.Fatalf("event = %s", e.EventName())
		}
	}
}

func idN(prefix string, n int) string {
	return prefix + string(rune('0'+n))
}

func TestLifecycleAuthorizeTransitReceive(t *testing.T) {
	a := newAuth(t, NearestReturnsCenter, MethodDropOff)
	if err := a.MarkInTransit(); err == nil {
		t.Fatal("transit without a label")
	}
	if err := a.LinkLabel("trk-9", now); err != nil {
		t.Fatalf("LinkLabel: %v", err)
	}
	a.Events()
	if err := a.MarkInTransit(); err != nil {
		t.Fatalf("MarkInTransit: %v", err)
	}
	err := a.ApplyOutcome([]OutcomeLine{{CatalogItemID: "sku-100", OwnerRef: "sel-42", QtyRestockable: 1, QtyUnsellable: 1}}, "node-rc-1", now)
	if err != nil {
		t.Fatalf("ApplyOutcome: %v", err)
	}
	if a.State() != StateReceived || a.ClosedAt().IsZero() {
		t.Fatalf("state/closedAt = %s/%s", a.State(), a.ClosedAt())
	}
	evs := a.Events()
	if len(evs) != 1 || evs[0].EventName() != "ReturnCompleted" {
		t.Fatalf("events = %v", evs)
	}
	c := evs[0].(Completed)
	if c.NodeID != "node-rc-1" || c.Lines[0].QtyRestockable != 1 {
		t.Fatalf("completed = %+v", c)
	}
}

func TestApplyOutcomeDirectFromAuthorized(t *testing.T) {
	a := newAuth(t, OriginNode, MethodDropOff)
	err := a.ApplyOutcome([]OutcomeLine{{CatalogItemID: "sku-100", QtyUnsellable: 2}}, "node-fc-1", now)
	if err != nil {
		t.Fatalf("ApplyOutcome from AUTHORIZED: %v", err)
	}
	if a.State() != StateReceived {
		t.Fatalf("state = %s", a.State())
	}
}

func TestApplyOutcomeRejectsOverQuantity(t *testing.T) {
	a := newAuth(t, OriginNode, MethodDropOff)
	err := a.ApplyOutcome([]OutcomeLine{{CatalogItemID: "sku-100", QtyRestockable: 3}}, "node-fc-1", now)
	if !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelBeforeTransitOnly(t *testing.T) {
	a := newAuth(t, OriginNode, MethodDropOff)
	a.Events()
	if err := a.Cancel(CancelShopperRequested, now); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if a.State() != StateCancelled {
		t.Fatalf("state = %s", a.State())
	}
	evs := a.Events()
	if len(evs) != 1 || evs[0].EventName() != "ReturnCancelled" {
		t.Fatalf("events = %v", evs)
	}
	if err := a.Cancel(CancelExpired, now); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("second cancel err = %v", err)
	}
}

func TestExpireLazily(t *testing.T) {
	a := newAuth(t, OriginNode, MethodDropOff)
	if a.Expired(now.Add(ReturnAuthorizationTTL)) {
		t.Fatal("not expired at the boundary")
	}
	later := now.Add(ReturnAuthorizationTTL + time.Minute)
	if !a.Expired(later) {
		t.Fatal("expired after the boundary")
	}
	if err := a.Cancel(CancelExpired, later); err != nil {
		t.Fatalf("Cancel(EXPIRED): %v", err)
	}
	if a.State() != StateCancelled {
		t.Fatalf("state = %s", a.State())
	}
}

func TestPickupLifecycle(t *testing.T) {
	a := newAuth(t, NearestReturnsCenter, MethodPickup)
	if a.Method() != MethodPickup {
		t.Fatal("method")
	}
	s, e := a.PickupWindow()
	if !s.Equal(now.Add(24*time.Hour)) || !e.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("window = %s..%s", s, e)
	}
	if err := a.MarkPickupFailed(now.Add(49 * time.Hour)); err != nil {
		t.Fatalf("MarkPickupFailed: %v", err)
	}
	if a.State() != StatePickupFailed {
		t.Fatalf("state = %s", a.State())
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	a := newAuth(t, NearestReturnsCenter, MethodPickup)
	if err := a.LinkLabel("trk-2", now); err != nil {
		t.Fatalf("LinkLabel: %v", err)
	}
	a.Events()
	if err := a.MarkInTransit(); err != nil {
		t.Fatalf("MarkInTransit: %v", err)
	}
	restored, err := Restore(a.Snapshot())
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := restored.Snapshot(); !sameSnapshot(got, a.Snapshot()) {
		t.Fatalf("round trip changed the snapshot:\n%+v\n%+v", got, a.Snapshot())
	}
}

func sameSnapshot(x, y Snapshot) bool {
	if x.ID != y.ID || x.OrderID != y.OrderID || x.GroupNo != y.GroupNo || x.State != y.State ||
		x.Disposition != y.Disposition || x.Method != y.Method || x.Version != y.Version ||
		x.DestinationNodeID != y.DestinationNodeID || x.OriginNodeID != y.OriginNodeID ||
		!x.AuthorizedAt.Equal(y.AuthorizedAt) || !x.ExpiresAt.Equal(y.ExpiresAt) ||
		!x.ClosedAt.Equal(y.ClosedAt) || len(x.Lines) != len(y.Lines) || len(x.TrackingNumbers) != len(y.TrackingNumbers) {
		return false
	}
	for i := range x.Lines {
		if x.Lines[i] != y.Lines[i] {
			return false
		}
	}
	for i := range x.TrackingNumbers {
		if x.TrackingNumbers[i] != y.TrackingNumbers[i] {
			return false
		}
	}
	return true
}
