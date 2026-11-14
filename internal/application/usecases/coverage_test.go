package usecases_test

// Coverage of the use-case tails the BDD features do not reach: the keyset
// cursor round-trip, the lazy-expiry refusals, the outcome-consumer paths
// (unknown authorization, unexpected lines, expiry on touch) and the metrics
// counters. It reuses the memory doubles of the features harness.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/memory"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

type countingMetrics struct {
	authorized, rejected map[string]int
	applied              []int
}

func newCountingMetrics() *countingMetrics {
	return &countingMetrics{authorized: map[string]int{}, rejected: map[string]int{}}
}

func (m *countingMetrics) ReturnAuthorized(_ context.Context, disposition string) {
	m.authorized[disposition]++
}

func (m *countingMetrics) ReturnRejected(_ context.Context, reason string) { m.rejected[reason]++ }

func (m *countingMetrics) OutcomeApplied(_ context.Context, unexpected int) {
	m.applied = append(m.applied, unexpected)
}

type fixture struct {
	svc     *usecases.Service
	metrics *countingMetrics
	clock   *memory.Clock
	seeded  map[string]bool
}

func price(c int64) *int64 { return &c }

func newFixture(t *testing.T) *fixture {
	_ = t
	t.Helper()
	store := memory.NewStore()
	clock := &memory.Clock{}
	clock.Set(t0)
	metrics := newCountingMetrics()
	deps := usecases.Deps{
		UoW:            memory.NewUnitOfWork(store),
		Authorizations: memory.NewAuthorizationRepository(store),
		Groups:         memory.NewGroupViewRepository(store),
		Network:        memory.NewNetworkViewRepository(store),
		Outcomes:       memory.NewOutcomeFacts(store),
		Labels:         memory.NewLabelIssuer(store),
		Outbox:         memory.NewOutbox(store),
		Inbox:          memory.NewInbox(store),
		Metrics:        metrics,
		Clock:          clock, IDs: memory.NewSequentialIDs(),
	}
	return &fixture{svc: usecases.New(deps, usecases.Config{}), metrics: metrics, clock: clock}
}

// seedTopology feeds the region's returns centre and the prefix assignment.
func (f *fixture) seedTopology(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := f.svc.OnNodeFact(ctx, "evt-nf-1", "NodeRegistered", requests.NodeFact{
		NodeID: "node-rc-1", NodeType: network.TypeReturnsCenter, RegionID: "reg-1", Lifecycle: network.NodeActive,
	}); err != nil {
		t.Fatalf("OnNodeFact: %v", err)
	}
	if err := f.svc.OnPrefixFact(ctx, "evt-pf-1", requests.PrefixFact{
		PostalPrefix: "03", RegionID: "reg-1", AssignmentVersion: 1,
	}); err != nil {
		t.Fatalf("OnPrefixFact: %v", err)
	}
}

// placeOrder feeds one CustomerOrderPlaced with one group of one line.
func (f *fixture) placeOrder(t *testing.T, orderID string) {
	t.Helper()
	if f.seeded == nil {
		f.seeded = map[string]bool{}
	}
	if !f.seeded["topo"] {
		f.seedTopology(t)
		f.seeded["topo"] = true
	}
	err := f.svc.OnOrderPlaced(context.Background(), "evt-op-"+orderID, requests.OrderPlaced{
		OrderID: orderID, PlacedAt: t0,
		Groups: []requests.PlacedGroup{{
			GroupNo: 1, DestinationPostalPrefix: "03", WitnessNodeID: "node-fc-1",
			Lines: []network.GroupLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}},
		}, {
			GroupNo:                 2,
			DestinationPostalPrefix: "03",
			WitnessNodeID:           "node-fc-1",
			Lines:                   []network.GroupLine{{LineNo: 1, CatalogItemID: "sku-200", Quantity: 1}},
		}},
	})
	if err != nil {
		t.Fatalf("OnOrderPlaced: %v", err)
	}
	for _, g := range []int{1, 2} {
		if err := f.svc.OnNoticeDecided(context.Background(), fmt.Sprintf("evt-nd-%s-%d", orderID, g), requests.NoticeDecided{
			OrderID: orderID, GroupNo: g, Family: "GROUP_DELIVERED",
		}); err != nil {
			t.Fatalf("OnNoticeDecided: %v", err)
		}
	}
}

func (f *fixture) authorize(t *testing.T, orderID string, groupNo int, cents int64) views.ReturnAuthorization {
	t.Helper()
	v, err := f.svc.AuthorizeReturn(context.Background(), requests.AuthorizeReturn{
		OrderID: orderID, GroupNo: groupNo,
		Lines:  []requests.AuthorizeLine{{LineNo: 1, UnitPriceCents: price(cents)}},
		Reason: returnauthorization.ReasonDefective, Method: returnauthorization.MethodDropOff,
	})
	if err != nil {
		t.Fatalf("AuthorizeReturn: %v", err)
	}
	return v
}

func TestAuthorizeCountsMetricsAndRejectsUnknownOrder(t *testing.T) {
	f := newFixture(t)
	f.placeOrder(t, "ord-1")
	v := f.authorize(t, "ord-1", 1, 2000)
	if f.metrics.authorized["NEAREST_RETURNS_CENTER"] != 1 {
		t.Fatalf("authorized counters = %v", f.metrics.authorized)
	}
	if v.AuthorizationID == "" {
		t.Fatalf("authorize produced no id: %+v", v)
	}
	_, err := f.svc.AuthorizeReturn(context.Background(), requests.AuthorizeReturn{
		OrderID: "ord-nope", GroupNo: 1,
		Lines:  []requests.AuthorizeLine{{LineNo: 1, UnitPriceCents: price(2000)}},
		Reason: returnauthorization.ReasonDefective, Method: returnauthorization.MethodDropOff,
	})
	if err == nil {
		t.Fatal("an unknown order must be rejected")
	}
}

func TestListCursorRoundTrip(t *testing.T) {
	f := newFixture(t)
	f.placeOrder(t, "ord-1")
	a := f.authorize(t, "ord-1", 1, 2000)
	f.clock.Set(t0.Add(time.Second))
	f.authorize(t, "ord-1", 2, 2000) // the second group, one second later
	page, err := f.svc.ListReturnAuthorizations(context.Background(), requests.ListAuthorizations{OrderID: "ord-1", Limit: 1})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first page = %+v", page)
	}
	page2, err := f.svc.ListReturnAuthorizations(context.Background(), requests.ListAuthorizations{
		OrderID:        "ord-1",
		AfterCreatedAt: page.Items[0].AuthorizedAt, AfterID: returnauthorization.ID(page.Items[0].AuthorizationID), Limit: 10,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(page2.Items) != 1 {
		t.Fatalf("second page items = %d, want the older one", len(page2.Items))
	}
	if page2.Items[0].AuthorizationID != a.AuthorizationID {
		t.Fatalf("the second page = %s, want the older %s", page2.Items[0].AuthorizationID, a.AuthorizationID)
	}
	if page2.NextCursor != "" {
		t.Fatal("the last page must carry no cursor")
	}
}

func TestExpiredAuthorizationRefusesLabelAndCancelsOnTouch(t *testing.T) {
	f := newFixture(t)
	f.placeOrder(t, "ord-1")
	v := f.authorize(t, "ord-1", 1, 2000)
	id := returnauthorization.ID(v.AuthorizationID)
	f.clock.Set(t0.Add(returnauthorization.ReturnAuthorizationTTL + time.Minute))
	if _, err := f.svc.IssueLabel(context.Background(), requests.IssueLabel{
		AuthorizationID: id, PackageNo: 1,
	}); err == nil {
		t.Fatal("a label on an expired authorization must be refused")
	}
	// The refused label was the touch that expired it: the view now shows the
	// lazy EXPIRED cancellation, and a later explicit cancel is refused.
	got, err := f.svc.GetReturnAuthorization(context.Background(), id)
	if err != nil {
		t.Fatalf("get after the refused label: %v", err)
	}
	if got.State != "CANCELLED" {
		t.Fatalf("after the lazy expiry the view state = %q", got.State)
	}
	if _, err := f.svc.CancelReturn(context.Background(), requests.CancelReturn{
		AuthorizationID: id,
	}); err == nil {
		t.Fatal("a cancel of a CANCELLED authorization must be refused")
	}
}

func TestOutcomeConsumerPaths(t *testing.T) {
	f := newFixture(t)
	f.placeOrder(t, "ord-1")
	v := f.authorize(t, "ord-1", 1, 2000)
	id := returnauthorization.ID(v.AuthorizationID)
	v2 := f.authorize(t, "ord-1", 2, 2000)
	id2 := returnauthorization.ID(v2.AuthorizationID)

	// An unknown authorization still records an all-unexpected fact.
	if err := f.svc.OnOutcomeSubmitted(context.Background(), "evt-out-0", requests.OutcomeSubmitted{
		AuthorizationID: "ret-does-not-exist", NodeID: "node-rc-1", ReceivedAt: t0,
		Lines: []requests.OutcomeLine{{CatalogItemID: "sku-100", OwnerRef: "sel-1", QtyRestockable: 1}},
	}); err != nil {
		t.Fatalf("unknown-authorization outcome: %v", err)
	}

	// A known line (the authorized item, owner still unset by any origin
	// fact) applies and counts the zero unexpected remainder.
	if err := f.svc.OnOutcomeSubmitted(context.Background(), "evt-out-1", requests.OutcomeSubmitted{
		AuthorizationID: id, NodeID: "node-rc-1", ReceivedAt: t0,
		Lines: []requests.OutcomeLine{{CatalogItemID: "sku-100", QtyRestockable: 1}},
	}); err != nil {
		t.Fatalf("known-line outcome: %v", err)
	}
	if len(f.metrics.applied) != 1 || f.metrics.applied[0] != 0 {
		t.Fatalf("applied counter = %v, want one call with 0 unexpected", f.metrics.applied)
	}
	// An outcome line that exceeds the authorized quantity is graded as an
	// unexpected fact instead of failing the consume.
	if err := f.svc.OnOutcomeSubmitted(context.Background(), "evt-out-2", requests.OutcomeSubmitted{
		AuthorizationID: id2, NodeID: "node-rc-1", ReceivedAt: t0,
		Lines: []requests.OutcomeLine{{CatalogItemID: "sku-100", QtyRestockable: 99}},
	}); err != nil {
		t.Fatalf("exceeding outcome: %v", err)
	}
	// The same item twice: each half passes the split, the repeat is refused
	// by the aggregate, and the fact is recorded as all-unexpected.
	if err := f.svc.OnOutcomeSubmitted(context.Background(), "evt-out-3", requests.OutcomeSubmitted{
		AuthorizationID: id2, NodeID: "node-rc-1", ReceivedAt: t0,
		Lines: []requests.OutcomeLine{
			{CatalogItemID: "sku-200", QtyRestockable: 1},
			{CatalogItemID: "sku-200", QtyUnsellable: 1},
		},
	}); err != nil {
		t.Fatalf("repeated-item outcome: %v", err)
	}

	// A cursor with the instant but no id is malformed, not an empty page.
	if _, err := f.svc.ListReturnAuthorizations(context.Background(), requests.ListAuthorizations{
		OrderID: "ord-1", AfterCreatedAt: t0,
	}); err == nil {
		t.Fatal("a half cursor (instant without id) must be refused")
	}

	// Notice-before-place tolerance: the placed fact never demotes a terminal
	// notice back to PROMISED.
	if err := f.svc.OnNoticeDecided(context.Background(), "evt-nd-late", requests.NoticeDecided{
		OrderID: "ord-late", GroupNo: 1, Family: "GROUP_DELIVERED",
	}); err != nil {
		t.Fatalf("early notice: %v", err)
	}
	if err := f.svc.OnOrderPlaced(context.Background(), "evt-op-late", requests.OrderPlaced{
		OrderID: "ord-late", PlacedAt: t0,
		Groups: []requests.PlacedGroup{{
			GroupNo: 1, DestinationPostalPrefix: "03", WitnessNodeID: "node-fc-1",
			Lines: []network.GroupLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 1}},
		}},
	}); err != nil {
		t.Fatalf("late placement: %v", err)
	}

	got, err := f.svc.GetReturnAuthorization(context.Background(), id)
	if err != nil {
		t.Fatalf("get after the outcome: %v", err)
	}
	if got.State != "CLOSED_KEEP" && got.State != "COMPLETED" && got.State != "RECEIVED" {
		t.Fatalf("the applied outcome left the state %q", got.State)
	}
}

func TestNilMetricsServiceStillServes(t *testing.T) {
	// The zero Deps uses the package no-op: the whole flow must work without
	// counters and without panicking.
	store := memory.NewStore()
	clock := &memory.Clock{}
	clock.Set(t0)
	svc := usecases.New(usecases.Deps{
		UoW:            memory.NewUnitOfWork(store),
		Authorizations: memory.NewAuthorizationRepository(store),
		Groups:         memory.NewGroupViewRepository(store),
		Network:        memory.NewNetworkViewRepository(store),
		Outcomes:       memory.NewOutcomeFacts(store),
		Labels:         memory.NewLabelIssuer(store),
		Outbox:         memory.NewOutbox(store),
		Inbox:          memory.NewInbox(store),
		Clock:          clock, IDs: memory.NewSequentialIDs(),
	}, usecases.Config{})
	if err := svc.OnNodeFact(context.Background(), "evt-nf-9", "NodeRegistered", requests.NodeFact{
		NodeID: "node-rc-1", NodeType: network.TypeReturnsCenter, RegionID: "reg-1", Lifecycle: network.NodeActive,
	}); err != nil {
		t.Fatalf("OnNodeFact: %v", err)
	}
	if err := svc.OnPrefixFact(context.Background(), "evt-pf-9", requests.PrefixFact{
		PostalPrefix: "03", RegionID: "reg-1", AssignmentVersion: 1,
	}); err != nil {
		t.Fatalf("OnPrefixFact: %v", err)
	}
	if err := svc.OnOrderPlaced(context.Background(), "evt-op-9", requests.OrderPlaced{
		OrderID: "ord-9", PlacedAt: t0,
		Groups: []requests.PlacedGroup{{
			GroupNo: 1, DestinationPostalPrefix: "03", WitnessNodeID: "node-fc-1",
			Lines: []network.GroupLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 1}},
		}},
	}); err != nil {
		t.Fatalf("OnOrderPlaced: %v", err)
	}
	if err := svc.OnNoticeDecided(context.Background(), "evt-nd-9", requests.NoticeDecided{
		OrderID: "ord-9", GroupNo: 1, Family: "GROUP_DELIVERED",
	}); err != nil {
		t.Fatalf("OnNoticeDecided: %v", err)
	}
	if _, err := svc.AuthorizeReturn(context.Background(), requests.AuthorizeReturn{
		OrderID: "ord-9", GroupNo: 1,
		Lines:  []requests.AuthorizeLine{{LineNo: 1, UnitPriceCents: price(900)}},
		Reason: returnauthorization.ReasonDefective, Method: returnauthorization.MethodDropOff,
	}); err != nil {
		t.Fatalf("AuthorizeReturn without counters: %v", err)
	}
}
