package kafka_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/inbound/kafka"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/memory"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

var now = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

type harness struct {
	store  *memory.Store
	clock  *memory.Clock
	order  *kafka.Consumer
	topo   *kafka.Consumer
	src    *kafka.Consumer
	vendor *kafka.Consumer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := memory.NewStore()
	clock := &memory.Clock{}
	clock.Set(now)
	svc := usecases.New(usecases.Deps{
		UoW: memory.NewUnitOfWork(store), Authorizations: memory.NewAuthorizationRepository(store),
		Groups: memory.NewGroupViewRepository(store), Network: memory.NewNetworkViewRepository(store),
		Outcomes: memory.NewOutcomeFacts(store), Labels: memory.NewLabelIssuer(store),
		Outbox: memory.NewOutbox(store), Inbox: memory.NewInbox(store),
		Clock: clock, IDs: memory.NewSequentialIDs(),
	}, usecases.Config{Logger: slog.New(slog.DiscardHandler)})
	logger := slog.New(slog.DiscardHandler)
	return &harness{
		store: store, clock: clock,
		order:  kafka.NewOrderConsumer([]string{"localhost:9092"}, "g", svc, logger),
		topo:   kafka.NewTopologyConsumer([]string{"localhost:9092"}, "g", svc, logger),
		src:    kafka.NewSourcingConsumer([]string{"localhost:9092"}, "g", svc, logger),
		vendor: kafka.NewVendorConsumer([]string{"localhost:9092"}, "g", svc, logger),
	}
}

// cloudEvent builds one structured-mode CloudEvent of typ with data.
func cloudEvent(id, typ, subject string, data any) []byte {
	body, _ := json.Marshal(data)
	envelope := map[string]any{
		"specversion": "1.0", "id": id, "source": "/retailnetwork/elsewhere",
		"type": typ, "subject": subject, "time": now.Format(time.RFC3339),
		"datacontenttype": "application/json", "data": json.RawMessage(body),
	}
	out, _ := json.Marshal(envelope)
	return out
}

func (h *harness) group(orderID string, groupNo int) network.OrderGroup {
	t := &testing.T{}
	g, err := h.store.GroupSaver().Group(context.Background(), orderID, groupNo)
	if err != nil {
		t.Fatalf("group: %v", err)
	}
	return g
}

func TestOrderPlacedProjectsGroups(t *testing.T) {
	h := newHarness(t)
	msg := cloudEvent("e-1", "com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced", "ord-1", map[string]any{
		"orderId": "ord-1", "placedAt": now, "destinationPostalPrefix": "013",
		"groups": []map[string]any{{
			"groupNo": 1, "destinationPostalPrefix": "013", "witnessNodeId": "node-fc-1",
			"lines": []map[string]any{{"lineNo": 1, "catalogItemId": "sku-100", "quantity": 2}},
		}},
	})
	if err := h.order.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	g := h.group("ord-1", 1)
	if g.Status != network.GroupPromised || g.DestinationPostalPrefix != "013" || len(g.Lines) != 1 {
		t.Fatalf("group = %+v", g)
	}
	// a replay is idempotent (inbox)
	if err := h.order.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if g2 := h.group("ord-1", 1); g2.UpdatedAt != g.UpdatedAt {
		t.Fatalf("replay changed the row: %v vs %v", g2.UpdatedAt, g.UpdatedAt)
	}
}

func TestTerminalNoticeFamiliesUpdateStatus(t *testing.T) {
	h := newHarness(t)
	// ordered steps: a slice, never a map (fleet rule)
	families := []struct {
		family string
		want   network.GroupStatus
	}{
		{"GROUP_SHIPPED", network.GroupShipped},
		{"GROUP_DELIVERED", network.GroupDelivered},
		{"GROUP_LOST", network.GroupLost},
		{"GROUP_RETURNED", network.GroupReturned},
	}
	for _, step := range families {
		family, want := step.family, step.want
		msg := cloudEvent("e-"+family, "com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided", "ord-1", map[string]any{
			"orderId": "ord-1", "groupNo": 3, "family": family, "notifier": "NOBODY",
			"policyVersion": 0, "noticeKey": "k-" + family, "decidedAt": now,
			"fact": map[string]any{"status": want},
		})
		if err := h.order.HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: %v", family, err)
		}
		if got := h.group("ord-1", 3).Status; got != want {
			t.Fatalf("%s: status = %s, want %s", family, got, want)
		}
	}
	// a non-terminal family is ignored
	msg := cloudEvent("e-eta", "com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided", "ord-1", map[string]any{
		"orderId": "ord-1", "groupNo": 3, "family": "ETA_WINDOW_ASSIGNED", "notifier": "NOBODY",
		"policyVersion": 0, "noticeKey": "k-eta", "decidedAt": now,
		"fact": map[string]any{"etaStart": now, "etaEnd": now},
	})
	if err := h.order.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("eta family: %v", err)
	}
	if got := h.group("ord-1", 3).Status; got != network.GroupReturned {
		t.Fatalf("eta family changed the status: %s", got)
	}
}

func TestNodeFactsProjectLifecycle(t *testing.T) {
	h := newHarness(t)
	steps := []struct {
		typ  string
		want network.NodeLifecycle
	}{
		{"com.retailnetwork.logistics.node-topology.node.NodeRegistered", network.NodePending},
		{"com.retailnetwork.logistics.node-topology.node.NodeActivated", network.NodeActive},
		{"com.retailnetwork.logistics.node-topology.node.NodeSuspended", network.NodeSuspended},
		{"com.retailnetwork.logistics.node-topology.node.NodeRetired", network.NodeRetired},
	}
	for i, s := range steps {
		msg := cloudEvent("e-node-"+s.typ[:40]+string(rune('a'+i)), s.typ, "node-rc-1", map[string]any{
			"nodeId": "node-rc-1", "nodeType": "RETURNS_CENTER", "regionId": "region-se",
		})
		if err := h.topo.HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: %v", s.typ, err)
		}
		node, err := h.store.NetworkSaver().ReturnsCenterForRegion(context.Background(), "region-se")
		if s.want == network.NodeActive {
			if err != nil || node.NodeID != "node-rc-1" {
				t.Fatalf("active lookup: %+v %v", node, err)
			}
		}
	}
	// once retired the region has no active returns centre
	if _, err := h.store.NetworkSaver().ReturnsCenterForRegion(context.Background(), "region-se"); err == nil {
		t.Fatal("retired node still active")
	}
}

func TestServiceAreaAssignedKeepsHighestVersion(t *testing.T) {
	h := newHarness(t)
	feed := func(id, prefix, region string, version int64) {
		t.Helper()
		msg := cloudEvent(id, "com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned", prefix, map[string]any{
			"postalPrefix": prefix, "deliveryStationNodeId": "node-ds-1", "regionId": region,
			"assignmentVersion": version,
		})
		if err := h.topo.HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("feed %s: %v", id, err)
		}
	}
	feed("e-sa-1", "013", "region-se", 2)
	feed("e-sa-2", "013", "region-ne", 1) // stale: must not win
	region, err := h.store.NetworkSaver().RegionOfPrefix(context.Background(), "013")
	if err != nil || region != "region-se" {
		t.Fatalf("region = %q %v, want region-se", region, err)
	}
}

func TestOriginFactsMergeNodeAndOwners(t *testing.T) {
	h := newHarness(t)
	release := cloudEvent("e-rel", "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode", "asg-1", map[string]any{
		"orderId": "ord-1", "groupNo": 1, "nodeId": "node-fc-1",
		"lines": []map[string]any{
			{"catalogItemId": "sku-100", "ownerRef": "sel-42", "quantity": 2},
			{"catalogItemId": "sku-200", "quantity": 1},
		},
	})
	if err := h.src.HandleMessage(context.Background(), release); err != nil {
		t.Fatalf("release: %v", err)
	}
	locked := cloudEvent("e-lock", "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked", "asg-1", map[string]any{
		"orderId": "ord-1", "groupNo": 1, "nodeId": "node-fc-9", "lockedAt": now,
	})
	if err := h.src.HandleMessage(context.Background(), locked); err != nil {
		t.Fatalf("locked: %v", err)
	}
	origin, err := h.store.NetworkSaver().Origin(context.Background(), "ord-1", 1)
	if err != nil {
		t.Fatalf("origin: %v", err)
	}
	if origin.NodeID != "node-fc-9" {
		t.Fatalf("node = %s (the lock is authoritative)", origin.NodeID)
	}
	if origin.OwnerOf("sku-100") != "sel-42" || origin.OwnerOf("sku-200") != "" {
		t.Fatalf("owners = %v", origin.OwnersByItem)
	}
}

func TestVendorOutcomeCompletesAuthorization(t *testing.T) {
	h := newHarness(t)
	seedAuthorization(t, h)
	msg := cloudEvent("e-out", "com.retailnetwork.partner.vendor-connectivity.returnoutcome.VendorReturnOutcomeSubmitted", "ret-1", map[string]any{
		"authorizationId": "ret-1", "nodeId": "node-rc-1", "receivedAt": now,
		"lines": []map[string]any{
			{"catalogItemId": "sku-100", "ownerRef": "sel-42", "qtyRestockable": 1, "qtyUnsellable": 1},
			{"catalogItemId": "sku-999", "qtyRestockable": 5},
		},
	})
	if err := h.vendor.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("outcome: %v", err)
	}
	repo := memory.NewAuthorizationRepository(h.store)
	a, err := repo.Get(context.Background(), "ret-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if a.State() != returnauthorization.StateReceived {
		t.Fatalf("state = %s", a.State())
	}
	// sku-999 was never authorized: recorded unexpected, never restocked
	facts := h.store.OutcomeFactRows()
	if len(facts) != 1 || len(facts[0].Lines) != 2 {
		t.Fatalf("facts = %+v", facts)
	}
	foundUnexpected := false
	for _, l := range facts[0].Lines {
		if l.CatalogItemID == "sku-999" && l.Unexpected {
			foundUnexpected = true
		}
	}
	if !foundUnexpected {
		t.Fatalf("no unexpected flag on sku-999: %+v", facts[0].Lines)
	}
	events := h.store.Events()
	completed := false
	for _, e := range events {
		if e.EventName() == "ReturnCompleted" {
			completed = true
			c := e.(returnauthorization.Completed)
			if len(c.Lines) != 1 {
				t.Fatalf("completed lines = %+v (sku-999 must be excluded)", c.Lines)
			}
		}
	}
	if !completed {
		t.Fatal("no ReturnCompleted event")
	}
}

func TestVendorOutcomeUnknownAuthorizationIsAcknowledged(t *testing.T) {
	h := newHarness(t)
	msg := cloudEvent("e-out-x", "com.retailnetwork.partner.vendor-connectivity.returnoutcome.VendorReturnOutcomeSubmitted", "ret-none", map[string]any{
		"authorizationId": "ret-none", "nodeId": "node-rc-1", "receivedAt": now,
		"lines": []map[string]any{{"catalogItemId": "sku-100", "qtyRestockable": 1}},
	})
	if err := h.vendor.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("outcome: %v", err)
	}
	if len(h.store.OutcomeFactRows()) != 1 {
		t.Fatal("the unknown outcome must still be recorded as a fact")
	}
	if len(h.store.Events()) != 0 {
		t.Fatal("no event may be published for an unknown authorization")
	}
}

func seedAuthorization(t *testing.T, h *harness) {
	t.Helper()
	a, err := returnauthorization.Authorize(returnauthorization.NewAuthorization{
		ID: "ret-1", OrderID: "ord-1", GroupNo: 1,
		Lines:  []returnauthorization.Line{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2, OwnerRef: "sel-42"}},
		Reason: returnauthorization.ReasonDefective, Disposition: returnauthorization.NearestReturnsCenter,
		Method: returnauthorization.MethodDropOff, DestinationNodeID: "node-rc-1",
		AuthorizedAt: now,
	}, now)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	a.Events()
	if err := memory.NewAuthorizationRepository(h.store).Save(context.Background(), a); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestUnknownTypeIsSkipped(t *testing.T) {
	h := newHarness(t)
	msg := cloudEvent("e-unknown", "com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderCancelled", "ord-1", map[string]any{
		"orderId": "ord-1", "reason": "X", "cancelledAt": now, "groupNos": []int{1},
	})
	if err := h.order.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("unknown type must be skipped, not failed: %v", err)
	}
}

func TestBadPayloadIsPoison(t *testing.T) {
	h := newHarness(t)
	msg := cloudEvent("e-bad", "com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced", "ord-1", map[string]any{
		"groups": []map[string]any{},
	})
	if err := h.order.HandleMessage(context.Background(), msg); err == nil {
		t.Fatal("a payload that can never be applied must fail (dead-letter path)")
	}
}
