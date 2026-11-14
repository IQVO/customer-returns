package returnauthorization

// Branch-coverage table of the invariant guards, the pickup-failure
// transitions, price absence and snapshot corruption paths that the happy-path
// tests do not reach.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/shared"
)

func TestAuthorizeRejectsInvalidInputs(t *testing.T) {
	base := func() NewAuthorization {
		return NewAuthorization{
			ID: "ret-0001", OrderID: "ord-1", GroupNo: 1,
			Lines:  []Line{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2, UnitPriceCents: price(400)}},
			Reason: ReasonDefective, Disposition: NearestReturnsCenter, Method: MethodDropOff,
			DestinationNodeID: "node-rc-1",
			AuthorizedAt:      now,
		}
	}
	cases := []struct {
		name   string
		mutate func(*NewAuthorization)
	}{
		{"no lines", func(n *NewAuthorization) { n.Lines = nil }},
		{"line no below one", func(n *NewAuthorization) { n.Lines[0].LineNo = 0 }},
		{"repeated line no", func(n *NewAuthorization) {
			n.Lines = append(n.Lines, Line{LineNo: 1, CatalogItemID: "sku-200", Quantity: 1})
		}},
		{"bad item id", func(n *NewAuthorization) { n.Lines[0].CatalogItemID = "sku 100" }},
		{"quantity below one", func(n *NewAuthorization) { n.Lines[0].Quantity = 0 }},
		{"negative price", func(n *NewAuthorization) { n.Lines[0].UnitPriceCents = price(-1) }},
		{"bad owner ref", func(n *NewAuthorization) { n.Lines[0].OwnerRef = "sel 42" }},
		{"keep with destination", func(n *NewAuthorization) {
			n.Disposition = KeepDisposition
			n.DestinationNodeID = "node-rc-1"
		}},
		{"nearest without destination", func(n *NewAuthorization) { n.DestinationNodeID = "" }},
		{"origin mismatch", func(n *NewAuthorization) {
			n.Disposition = OriginNode
			n.OriginNodeID = "node-fc-2"
		}},
		{"pickup keep", func(n *NewAuthorization) {
			n.Disposition = KeepDisposition
			n.DestinationNodeID = ""
			n.Method = MethodPickup
			n.PickupWindowStart = now.Add(time.Hour)
			n.PickupWindowEnd = now.Add(2 * time.Hour)
		}},
		{"pickup without window", func(n *NewAuthorization) { n.Method = MethodPickup }},
		{"pickup window inverted", func(n *NewAuthorization) {
			n.Method = MethodPickup
			n.PickupWindowStart = now.Add(2 * time.Hour)
			n.PickupWindowEnd = now.Add(time.Hour)
		}},
		{"pickup window outside ttl", func(n *NewAuthorization) {
			n.Method = MethodPickup
			n.PickupWindowStart = now.Add(-time.Hour)
			n.PickupWindowEnd = now.Add(2 * time.Hour)
		}},
		{"drop off with window", func(n *NewAuthorization) {
			n.PickupWindowStart = now.Add(time.Hour)
			n.PickupWindowEnd = now.Add(2 * time.Hour)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := base()
			c.mutate(&n)
			if _, err := Authorize(n, now); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("Authorize(%s) err = %v, want a validation error", c.name, err)
			}
		})
	}
}

func TestAuthorizeRejectsTooManyLinesAndPackages(t *testing.T) {
	n := NewAuthorization{
		ID: "ret-many", OrderID: "ord-1", GroupNo: 1,
		Lines:  make([]Line, MaxLinesPerReturn+1),
		Reason: ReasonDefective, Disposition: NearestReturnsCenter, Method: MethodDropOff,
		DestinationNodeID: "node-rc-1", AuthorizedAt: now,
	}
	for i := range n.Lines {
		n.Lines[i] = Line{LineNo: i + 1, CatalogItemID: "sku-100", Quantity: 1}
	}
	if _, err := Authorize(n, now); err == nil {
		t.Fatal("MaxLinesPerReturn+1 lines must be refused")
	}
}

func TestPickupFailureTransitions(t *testing.T) {
	a := newAuth(t, NearestReturnsCenter, MethodPickup)
	if err := a.MarkPickupFailed(now); err != nil {
		t.Fatalf("MarkPickupFailed on AUTHORIZED pickup: %v", err)
	}
	if a.State() != StatePickupFailed {
		t.Fatalf("state = %s, want PICKUP_FAILED", a.State())
	}
	if err := a.MarkPickupFailed(now); err == nil {
		t.Fatal("a second pickup failure on PICKUP_FAILED must be refused")
	}
	dropOff := newAuth(t, NearestReturnsCenter, MethodDropOff)
	if err := dropOff.MarkPickupFailed(now); err == nil {
		t.Fatal("a drop-off return has no pickup to fail")
	}
}

func TestPricesAbsentBranches(t *testing.T) {
	a := newAuth(t, NearestReturnsCenter, MethodDropOff)
	if a.PricesAbsent() {
		t.Fatal("a fully priced authorization is not price-absent")
	}
	b, err := Authorize(NewAuthorization{
		ID: "ret-2", OrderID: "ord-1", GroupNo: 1,
		Lines:  []Line{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}},
		Reason: ReasonDefective, Disposition: NearestReturnsCenter, Method: MethodDropOff,
		DestinationNodeID: "node-rc-1", AuthorizedAt: now,
	}, now)
	if err != nil {
		t.Fatalf("an absent price authorizes (never KEEP): %v", err)
	}
	if !b.PricesAbsent() {
		t.Fatal("the nil price must read as absent")
	}
}

func TestRestoreRejectsCorruptSnapshots(t *testing.T) {
	a := newAuth(t, NearestReturnsCenter, MethodDropOff)
	snap := a.Snapshot()
	snap.State = State("NOPE")
	if _, err := Restore(snap); err == nil {
		t.Fatal("an unknown state must be refused at restore")
	}
	snap2 := a.Snapshot()
	snap2.Lines = nil
	if _, err := Restore(snap2); err == nil {
		t.Fatal("a snapshot without lines must be refused at restore")
	}
}

func TestTerminalRulesRefuseIncoherentSnapshots(t *testing.T) {
	a := newAuth(t, NearestReturnsCenter, MethodDropOff)
	// KEEP must close at once: a live KEEP state is incoherent.
	keepLive := a.Snapshot()
	keepLive.Disposition = KeepDisposition
	keepLive.DestinationNodeID = ""
	if _, err := Restore(keepLive); err == nil {
		t.Fatal("a live KEEP state must be refused")
	}
	// CLOSED_KEEP belongs to KEEP alone.
	closedKeep := a.Snapshot()
	closedKeep.State = StateClosedKeep
	if _, err := Restore(closedKeep); err == nil {
		t.Fatal("CLOSED_KEEP on a NEAREST_RETURNS_CENTER return must be refused")
	}
	// KEEP with a destination node contradicts itself.
	keepWithDest := a.Snapshot()
	keepWithDest.Disposition = KeepDisposition
	keepWithDest.State = StateCancelled
	if _, err := Restore(keepWithDest); err == nil {
		t.Fatal("KEEP with a destination node must be refused")
	}
}

func TestCheckTrackingRefuses(t *testing.T) {
	numbers := make([]string, MaxPackagesPerReturn+1)
	for i := range numbers {
		numbers[i] = "trk-000" + string(rune('a'+i))
	}
	if err := checkTracking(numbers); err == nil {
		t.Fatal("MaxPackagesPerReturn+1 tracking numbers must be refused")
	}
	if err := checkTracking([]string{"trk-1", "trk-1"}); err == nil {
		t.Fatal("a repeated tracking number must be refused")
	}
	if err := checkTracking([]string{"not an id"}); err == nil {
		t.Fatal("an invalid tracking id must be refused")
	}
	if err := checkTracking(nil); err != nil {
		t.Fatalf("no tracking numbers is fine: %v", err)
	}
}

func TestValidOwnerRefShapes(t *testing.T) {
	for _, ok := range []string{"sel-42", "sel-1"} {
		if !shared.ValidOwnerRef(ok) {
			t.Fatalf("%q must be a valid owner ref", ok)
		}
	}
	for _, bad := range []string{"", "sel 42", "sel/42", strings.Repeat("x", 100)} {
		if shared.ValidOwnerRef(bad) {
			t.Fatalf("%q must not be a valid owner ref", bad)
		}
	}
}
