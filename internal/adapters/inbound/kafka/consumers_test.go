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

// cloudEvent builds one structured-mode CloudEvent of typ with data. It is for
// the payloads a producer does NOT publish (a type this context ignores, a
// malformed payload); a consumed fact comes from producerEvent.
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

// producerEvent returns a producer's own example (pinned_examples_test.go,
// read from the pinned spec) with a new CloudEvents id and the named `data`
// fields replaced: the shape
// stays the producer's, only the ids a scenario needs change. Tests never
// invent a consumed payload.
func producerEvent(t *testing.T, example, id string, set map[string]any) []byte {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(example), &envelope); err != nil {
		t.Fatalf("producer example: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(envelope["data"], &data); err != nil {
		t.Fatalf("producer example data: %v", err)
	}
	for k, v := range set {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("producer example field %s: %v", k, err)
		}
		data[k] = raw
	}
	envelope["data"], _ = json.Marshal(data)
	envelope["id"], _ = json.Marshal(id)
	out, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("producer example: %v", err)
	}
	return out
}

func (h *harness) node(t *testing.T, nodeID string) network.Node {
	t.Helper()
	n, err := h.store.NetworkSaver().Node(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("node %s: %v", nodeID, err)
	}
	return n
}

func TestOrderPlacedProjectsGroups(t *testing.T) {
	h := newHarness(t)
	msg := producerEvent(t, exampleCustomerOrderingCustomerOrderPlaced, "e-1", map[string]any{"orderId": "ord-1"})
	if err := h.order.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	g := h.group("ord-1", 1)
	// The prefix is the ORDER's: customer-ordering publishes none on a group.
	if g.Status != network.GroupPromised || g.DestinationPostalPrefix != "787" || g.WitnessNodeID != "fc-aus-1" {
		t.Fatalf("group = %+v", g)
	}
	if len(g.Lines) != 1 || g.Lines[0] != (network.GroupLine{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}) {
		t.Fatalf("lines = %+v", g.Lines)
	}
	// a replay is idempotent (inbox)
	if err := h.order.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if g2 := h.group("ord-1", 1); g2.UpdatedAt != g.UpdatedAt {
		t.Fatalf("replay changed the row: %v vs %v", g2.UpdatedAt, g.UpdatedAt)
	}
}

// customer-ordering's contract has ONE ShopperNoticeDecided example, of the
// ETA_WINDOW_ASSIGNED family. The terminal families below are that example
// with the family and the fact the producer's encoder writes for them
// (customer-ordering internal/adapters/outbound/kafka/encoder.go,
// noticeFactData: `status` only).
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
		msg := producerEvent(t, exampleCustomerOrderingShopperNoticeDecided, "e-"+family, map[string]any{
			"orderId": "ord-1", "groupNo": 3, "family": family, "fact": map[string]any{"status": want},
		})
		if err := h.order.HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: %v", family, err)
		}
		if got := h.group("ord-1", 3).Status; got != want {
			t.Fatalf("%s: status = %s, want %s", family, got, want)
		}
	}
	// a non-terminal family is ignored: the producer's example as published,
	// pointed at the same group
	msg := producerEvent(t, exampleCustomerOrderingShopperNoticeDecided, "e-eta", map[string]any{"orderId": "ord-1", "groupNo": 3})
	if err := h.order.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("eta family: %v", err)
	}
	if got := h.group("ord-1", 3).Status; got != network.GroupReturned {
		t.Fatalf("eta family changed the status: %s", got)
	}
}

// The node facts as node-topology publishes them: NodeRegistered carries the
// type and the region, the three status facts carry the node id and an
// instant. A returns centre is a destination only while ACTIVE.
func TestNodeFactsProjectLifecycle(t *testing.T) {
	h := newHarness(t)
	registered := producerEvent(t, exampleNodeTopologyNodeRegistered, "e-node-reg", map[string]any{
		"nodeId": "node-rc-1", "nodeType": "RETURNS_CENTER", "subtype": nil,
	})
	if err := h.topo.HandleMessage(context.Background(), registered); err != nil {
		t.Fatalf("NodeRegistered: %v", err)
	}
	if n := h.node(t, "node-rc-1"); n.Type != network.TypeReturnsCenter || n.RegionID != "region-se" || n.Lifecycle != network.NodePending {
		t.Fatalf("registered node = %+v", n)
	}
	steps := []struct {
		example string
		want    network.NodeLifecycle
	}{
		{exampleNodeTopologyNodeActivated, network.NodeActive},
		{exampleNodeTopologyNodeSuspended, network.NodeSuspended},
		{exampleNodeTopologyNodeRetired, network.NodeRetired},
	}
	for i, s := range steps {
		msg := producerEvent(t, s.example, "e-node-status-"+string(rune('a'+i)), map[string]any{"nodeId": "node-rc-1"})
		if err := h.topo.HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: %v", s.want, err)
		}
		// only the lifecycle moved: the status facts carry no type and no region
		if n := h.node(t, "node-rc-1"); n.Type != network.TypeReturnsCenter || n.RegionID != "region-se" || n.Lifecycle != s.want {
			t.Fatalf("after %s the node = %+v", s.want, n)
		}
		node, err := h.store.NetworkSaver().ReturnsCenterForRegion(context.Background(), "region-se")
		if s.want == network.NodeActive {
			if err != nil || node.NodeID != "node-rc-1" {
				t.Fatalf("active lookup: %+v %v", node, err)
			}
		} else if err == nil {
			t.Fatalf("a %s centre is still a destination: %+v", s.want, node)
		}
	}
}

func TestServiceAreaAssignedKeepsHighestVersion(t *testing.T) {
	h := newHarness(t)
	feed := func(id, prefix, region string, version int64) {
		t.Helper()
		msg := producerEvent(t, exampleNodeTopologyServiceAreaAssigned, id, map[string]any{
			"postalPrefix": prefix, "regionId": region, "assignmentVersion": version,
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
	release := producerEvent(t, exampleOrderSourcingFulfillmentReleasedToNode, "e-rel", map[string]any{
		"orderId": "ord-1", "groupNo": 1, "nodeId": "node-fc-1",
		"lines": []map[string]any{
			{"catalogItemId": "sku-100", "listingId": "lst-sku-100-node-fc-1-sel-42", "ownerRef": "sel-42", "quantity": 2},
			{"catalogItemId": "sku-200", "listingId": "lst-sku-200-node-fc-1", "quantity": 1},
		},
	})
	if err := h.src.HandleMessage(context.Background(), release); err != nil {
		t.Fatalf("release: %v", err)
	}
	locked := producerEvent(t, exampleOrderSourcingFulfillmentLocked, "e-lock", map[string]any{
		"orderId": "ord-1", "groupNo": 1, "nodeId": "node-fc-9",
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
	msg := producerEvent(t, exampleVendorConnectivityVendorReturnOutcomeSubmitted, "e-out", map[string]any{
		"authorizationId": "ret-1", "nodeId": "node-rc-1",
		"lines": []map[string]any{
			{"catalogItemId": "sku-100", "ownerRef": "sel-42", "qtyRestockable": 1, "qtyUnsellable": 1},
			{"catalogItemId": "sku-999", "qtyRestockable": 5, "qtyUnsellable": 0},
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
	msg := producerEvent(t, exampleVendorConnectivityVendorReturnOutcomeSubmitted, "e-out-x", map[string]any{
		"authorizationId": "ret-none", "nodeId": "node-rc-1",
		"lines": []map[string]any{{"catalogItemId": "sku-100", "qtyRestockable": 1, "qtyUnsellable": 0}},
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
