package returnauthorization

// Mutation-driven tests: each one pins a behaviour that a surviving gremlins
// mutant used to change (version bumps, quantity and size boundaries, the
// terminal-state invariants of Restore).

import (
	"errors"
	"strconv"
	"testing"

	"github.com/IQVO/customer-returns/internal/domain/shared"
)

func TestEveryTransitionBumpsTheVersionByOne(t *testing.T) {
	t.Run("authorize starts at 1, label, transit and outcome add one each", func(t *testing.T) {
		a := newAuth(t, NearestReturnsCenter, MethodDropOff)
		if a.Version() != 1 {
			t.Fatalf("fresh version = %d, want 1", a.Version())
		}
		if err := a.LinkLabel("trk-1", now); err != nil {
			t.Fatalf("LinkLabel: %v", err)
		}
		if a.Version() != 2 {
			t.Fatalf("version after LinkLabel = %d, want 2", a.Version())
		}
		if err := a.MarkInTransit(); err != nil {
			t.Fatalf("MarkInTransit: %v", err)
		}
		if a.Version() != 3 {
			t.Fatalf("version after MarkInTransit = %d, want 3", a.Version())
		}
		out := []OutcomeLine{{CatalogItemID: "sku-100", QtyRestockable: 2}}
		if err := a.ApplyOutcome(out, "node-rc-1", now); err != nil {
			t.Fatalf("ApplyOutcome: %v", err)
		}
		if a.Version() != 4 {
			t.Fatalf("version after ApplyOutcome = %d, want 4", a.Version())
		}
	})
	t.Run("cancel adds one", func(t *testing.T) {
		a := newAuth(t, NearestReturnsCenter, MethodDropOff)
		if err := a.Cancel(CancelShopperRequested, now); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if a.Version() != 2 {
			t.Fatalf("version after Cancel = %d, want 2", a.Version())
		}
	})
	t.Run("pickup failure adds one", func(t *testing.T) {
		a := newAuth(t, NearestReturnsCenter, MethodPickup)
		if err := a.MarkPickupFailed(now); err != nil {
			t.Fatalf("MarkPickupFailed: %v", err)
		}
		if a.Version() != 2 {
			t.Fatalf("version after MarkPickupFailed = %d, want 2", a.Version())
		}
	})
}

func TestApplyOutcomeQuantityBoundaries(t *testing.T) {
	// newAuth authorizes quantity 2 of sku-100.
	cases := []struct {
		name         string
		restockable  int
		unsellable   int
		wantAccepted bool
	}{
		{"all restockable, zero unsellable", 2, 0, true},
		{"all unsellable, zero restockable", 0, 2, true},
		{"exactly one unit restockable", 1, 0, true},
		{"exactly one unit unsellable", 0, 1, true},
		{"split exactly the authorized quantity", 1, 1, true},
		{"nothing received", 0, 0, false},
		{"negative restockable", -1, 3, false},
		{"negative unsellable", 3, -1, false},
		{"sum beyond authorized, difference within", 2, 1, false},
		{"both beyond authorized", 2, 2, false},
		{"one unit beyond authorized", 3, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newAuth(t, NearestReturnsCenter, MethodDropOff)
			out := []OutcomeLine{{CatalogItemID: "sku-100", QtyRestockable: c.restockable, QtyUnsellable: c.unsellable}}
			err := a.ApplyOutcome(out, "node-rc-1", now)
			if c.wantAccepted {
				if err != nil {
					t.Fatalf("ApplyOutcome(%d,%d) = %v, want accepted", c.restockable, c.unsellable, err)
				}
				if a.State() != StateReceived {
					t.Fatalf("state = %s, want RECEIVED", a.State())
				}
				return
			}
			if !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("ApplyOutcome(%d,%d) err = %v, want a validation error", c.restockable, c.unsellable, err)
			}
			if a.State() != StateAuthorized || a.Version() != 1 {
				t.Fatalf("a refused outcome must not change state/version, got %s/%d", a.State(), a.Version())
			}
		})
	}
}

func TestAuthorizeLineAndPriceBoundaries(t *testing.T) {
	build := func(lines []Line) NewAuthorization {
		return NewAuthorization{
			ID: "ret-0002", OrderID: "ord-1", GroupNo: 1, Lines: lines,
			Reason: ReasonDefective, Disposition: NearestReturnsCenter, Method: MethodDropOff,
			DestinationNodeID: "node-rc-1", AuthorizedAt: now,
		}
	}
	linesN := func(n int) []Line {
		out := make([]Line, n)
		for i := range out {
			out[i] = Line{LineNo: i + 1, CatalogItemID: "sku-" + strconv.Itoa(i+1), Quantity: 1}
		}
		return out
	}
	if _, err := Authorize(build(linesN(MaxLinesPerReturn)), now); err != nil {
		t.Fatalf("exactly MaxLinesPerReturn lines must be accepted: %v", err)
	}
	if _, err := Authorize(build(linesN(MaxLinesPerReturn+1)), now); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("MaxLinesPerReturn+1 lines err = %v, want a validation error", err)
	}
	if _, err := Authorize(build(nil), now); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("no lines err = %v, want a validation error", err)
	}

	free := []Line{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 1, UnitPriceCents: price(0)}}
	a, err := Authorize(build(free), now)
	if err != nil {
		t.Fatalf("a zero unit price is a valid price: %v", err)
	}
	if a.PricesAbsent() {
		t.Fatal("a zero price is present, not absent")
	}
	negative := []Line{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 1, UnitPriceCents: price(-1)}}
	if _, err := Authorize(build(negative), now); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("a negative price err = %v, want a validation error", err)
	}
}

func TestCheckTrackingAcceptsExactlyMaxPackages(t *testing.T) {
	numbers := make([]string, MaxPackagesPerReturn)
	for i := range numbers {
		numbers[i] = "trk-" + strconv.Itoa(i+1)
	}
	if err := checkTracking(numbers); err != nil {
		t.Fatalf("exactly MaxPackagesPerReturn tracking numbers must pass: %v", err)
	}
	// And the aggregate restores with all five linked.
	a := newAuth(t, NearestReturnsCenter, MethodDropOff)
	snap := a.Snapshot()
	snap.TrackingNumbers = numbers
	restored, err := Restore(snap)
	if err != nil {
		t.Fatalf("Restore with %d tracking numbers: %v", len(numbers), err)
	}
	if got := len(restored.TrackingNumbers()); got != MaxPackagesPerReturn {
		t.Fatalf("restored tracking numbers = %d, want %d", got, MaxPackagesPerReturn)
	}
}

func TestRestoreAcceptsEveryCoherentLifecycleSnapshot(t *testing.T) {
	received := newAuth(t, NearestReturnsCenter, MethodDropOff)
	if err := received.LinkLabel("trk-1", now); err != nil {
		t.Fatalf("LinkLabel: %v", err)
	}
	if err := received.MarkInTransit(); err != nil {
		t.Fatalf("MarkInTransit: %v", err)
	}
	inTransit := received.Snapshot()
	if err := received.ApplyOutcome([]OutcomeLine{{CatalogItemID: "sku-100", QtyRestockable: 2}}, "node-rc-1", now); err != nil {
		t.Fatalf("ApplyOutcome: %v", err)
	}
	cancelled := newAuth(t, NearestReturnsCenter, MethodDropOff)
	if err := cancelled.Cancel(CancelExpired, now); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	pickupFailed := newAuth(t, OriginNode, MethodPickup)
	if err := pickupFailed.MarkPickupFailed(now); err != nil {
		t.Fatalf("MarkPickupFailed: %v", err)
	}
	cases := []struct {
		name string
		snap Snapshot
		want State
	}{
		{"in transit", inTransit, StateInTransit},
		{"received", received.Snapshot(), StateReceived},
		{"cancelled", cancelled.Snapshot(), StateCancelled},
		{"pickup failed", pickupFailed.Snapshot(), StatePickupFailed},
		{"authorized", newAuth(t, OriginNode, MethodDropOff).Snapshot(), StateAuthorized},
		{"closed keep", newAuth(t, KeepDisposition, MethodDropOff).Snapshot(), StateClosedKeep},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Restore(c.snap)
			if err != nil {
				t.Fatalf("Restore(%s) = %v, want a coherent snapshot accepted", c.name, err)
			}
			if got.State() != c.want {
				t.Fatalf("restored state = %s, want %s", got.State(), c.want)
			}
		})
	}
}

func TestRestoreRefusesIncoherentTerminalSnapshots(t *testing.T) {
	// Every snapshot below is otherwise valid (valid tracking numbers, valid
	// disposition rules): only the terminal-state rules are broken.
	linked := func() *ReturnAuthorization {
		a := newAuth(t, NearestReturnsCenter, MethodDropOff)
		if err := a.LinkLabel("trk-1", now); err != nil {
			t.Fatalf("LinkLabel: %v", err)
		}
		return a
	}
	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"received without closedAt", func(s *Snapshot) { s.State = StateReceived }},
		{"cancelled without closedAt", func(s *Snapshot) { s.State = StateCancelled }},
		{"authorized with closedAt", func(s *Snapshot) { s.ClosedAt = now }},
		{"in transit without a label", func(s *Snapshot) {
			s.State = StateInTransit
			s.TrackingNumbers = nil
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := linked().Snapshot()
			c.mutate(&snap)
			if _, err := Restore(snap); !errors.Is(err, shared.ErrInvalidSnapshot) {
				t.Fatalf("Restore(%s) err = %v, want ErrInvalidSnapshot", c.name, err)
			}
		})
	}
}
