package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/inbound/kafka"
	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

// Every consumed type, decoded through the real handler from the payload its
// producer documents (producer_examples_test.go). The examples are fed as
// published, byte for byte: a required-field check or a wire struct that does
// not match what the producer sends fails here, not in a dead-letter topic.

// feedExample hands the producer's example to the consumer that owns its
// topic and fails when the consumer has no handler for the example's type or
// cannot apply it.
func feedExample(t *testing.T, c *kafka.Consumer, example string) {
	t.Helper()
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(example), &envelope); err != nil {
		t.Fatalf("producer example: %v", err)
	}
	if _, ok := c.Handlers[envelope.Type]; !ok {
		t.Fatalf("the %s consumer has no handler for the producer's type %q", c.Name, envelope.Type)
	}
	if err := c.HandleMessage(context.Background(), []byte(example)); err != nil {
		t.Fatalf("the producer's own example of %s is not applied: %v", envelope.Type, err)
	}
}

func exampleTime(t *testing.T, rfc3339 string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestProducerExampleCustomerOrderPlaced(t *testing.T) {
	h := newHarness(t)
	feedExample(t, h.order, exampleCustomerOrderingCustomerOrderPlaced)
	g, err := h.store.GroupSaver().Group(context.Background(), "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10", 1)
	if err != nil {
		t.Fatalf("the placed group is not in the view: %v", err)
	}
	// destinationPostalPrefix is published on the ORDER; the group has none.
	if g.Status != network.GroupPromised || g.DestinationPostalPrefix != "787" || g.WitnessNodeID != "fc-aus-1" {
		t.Fatalf("group = %+v", g)
	}
	if len(g.Lines) != 1 || g.Lines[0] != (network.GroupLine{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}) {
		t.Fatalf("lines = %+v", g.Lines)
	}
}

func TestProducerExampleShopperNoticeDecided(t *testing.T) {
	h := newHarness(t)
	// The producer's one example is a non-terminal family: acknowledged, and
	// no order group row is invented for it.
	feedExample(t, h.order, exampleCustomerOrderingShopperNoticeDecided)
	if groups := h.store.Groups(); len(groups) != 0 {
		t.Fatalf("an ETA notice created rows: %+v", groups)
	}
}

func TestProducerExamplesNodeLifecycle(t *testing.T) {
	h := newHarness(t)
	feedExample(t, h.topo, exampleNodeTopologyNodeRegistered)
	want := network.Node{NodeID: "node-0f3a91", Type: network.TypeFulfillmentCenter, RegionID: "region-se", Lifecycle: network.NodePending}
	if got := h.node(t, "node-0f3a91"); got != want {
		t.Fatalf("after NodeRegistered the node = %+v, want %+v", got, want)
	}
	// The three status examples carry the node id and an instant, nothing
	// else: each moves the lifecycle and leaves type and region alone.
	steps := []struct {
		example   string
		lifecycle network.NodeLifecycle
		at        string // the CloudEvents `time` of the example
	}{
		{exampleNodeTopologyNodeActivated, network.NodeActive, "2026-10-22T14:00:00Z"},
		{exampleNodeTopologyNodeSuspended, network.NodeSuspended, "2026-10-23T08:30:00Z"},
		{exampleNodeTopologyNodeRetired, network.NodeRetired, "2026-11-30T23:59:00Z"},
	}
	for _, s := range steps {
		feedExample(t, h.topo, s.example)
		want.Lifecycle, want.LifecycleAt = s.lifecycle, exampleTime(t, s.at)
		if got := h.node(t, "node-0f3a91"); got != want {
			t.Fatalf("after the %s fact the node = %+v, want %+v", s.lifecycle, got, want)
		}
	}
}

// Order tolerance (ADR 0003): a status fact whose registration this view
// never saw is a well-formed fact, never a dead letter. It keeps a
// lifecycle-only row; the registration completes it without demoting it.
func TestProducerExampleNodeStatusBeforeItsRegistration(t *testing.T) {
	h := newHarness(t)
	feedExample(t, h.topo, exampleNodeTopologyNodeActivated)
	at := exampleTime(t, "2026-10-22T14:00:00Z")
	if got, want := h.node(t, "node-0f3a91"), (network.Node{NodeID: "node-0f3a91", Lifecycle: network.NodeActive, LifecycleAt: at}); got != want {
		t.Fatalf("lifecycle-only row = %+v, want %+v", got, want)
	}
	feedExample(t, h.topo, exampleNodeTopologyNodeRegistered)
	want := network.Node{NodeID: "node-0f3a91", Type: network.TypeFulfillmentCenter, RegionID: "region-se", Lifecycle: network.NodeActive, LifecycleAt: at}
	if got := h.node(t, "node-0f3a91"); got != want {
		t.Fatalf("after the late registration the node = %+v, want %+v", got, want)
	}
}

// A status fact older than the one already applied changes nothing: the
// dead-lettered NodeActivated replayed after a newer NodeSuspended.
func TestStaleNodeStatusFactIsANoOp(t *testing.T) {
	h := newHarness(t)
	feedExample(t, h.topo, exampleNodeTopologyNodeRegistered)
	feedExample(t, h.topo, exampleNodeTopologyNodeSuspended) // 2026-10-23
	feedExample(t, h.topo, exampleNodeTopologyNodeActivated) // 2026-10-22, replayed late
	if got := h.node(t, "node-0f3a91"); got.Lifecycle != network.NodeSuspended {
		t.Fatalf("the older NodeActivated undid the newer status: %+v", got)
	}
}

func TestProducerExamplesServiceAreaAssigned(t *testing.T) {
	h := newHarness(t)
	feedExample(t, h.topo, exampleNodeTopologyServiceAreaAssigned)
	feedExample(t, h.topo, exampleNodeTopologyServiceAreaAssignedCarrierHandoff)
	region, err := h.store.NetworkSaver().RegionOfPrefix(context.Background(), "01310")
	if err != nil || region != "region-se" {
		t.Fatalf("region of 01310 = %q %v, want region-se", region, err)
	}
}

func TestProducerExampleFulfillmentReleasedToNode(t *testing.T) {
	h := newHarness(t)
	feedExample(t, h.src, exampleOrderSourcingFulfillmentReleasedToNode)
	origin, err := h.store.NetworkSaver().Origin(context.Background(), "ord-51a0c9", 1)
	if err != nil {
		t.Fatalf("origin: %v", err)
	}
	if origin.NodeID != "node-fc-ws-sor-01" || origin.OwnerOf("cat-000456") != "sel-0042" || origin.OwnerOf("cat-000123") != "" {
		t.Fatalf("origin = %+v", origin)
	}
}

// A merged release is ONE event for several order groups: orderId and groupNo
// name the carrier group only, `members` lists every merged group. Each
// member group gets its origin; none is lost behind the carrier.
func TestProducerExampleFulfillmentReleasedToNodeMerged(t *testing.T) {
	h := newHarness(t)
	feedExample(t, h.src, exampleOrderSourcingFulfillmentReleasedToNodeMerged)
	for _, orderID := range []string{"ord-51a0c9", "ord-6b2e07"} {
		origin, err := h.store.NetworkSaver().Origin(context.Background(), orderID, 1)
		if err != nil {
			t.Fatalf("origin of member %s: %v", orderID, err)
		}
		if origin.NodeID != "node-fc-ws-sor-01" {
			t.Fatalf("origin of member %s = %+v", orderID, origin)
		}
	}
}

// In a merged release a line names its member (`assignmentId`): the owner of a
// line decorates the group that line belongs to and no other. The producer's
// merged example has operator-owned lines only, so this is that example with
// the two lines given distinct items and owners (the line shape is the
// producer's releasedLine: catalogItemId, listingId, ownerRef, quantity,
// assignmentId).
func TestMergedReleaseOwnersFollowTheMemberOfEachLine(t *testing.T) {
	h := newHarness(t)
	msg := producerEvent(t, exampleOrderSourcingFulfillmentReleasedToNodeMerged, "e-merged-owners", map[string]any{
		"lines": []map[string]any{
			{"catalogItemId": "cat-000123", "listingId": "lst-000123-fc-ws-sor-01-sel-0042", "ownerRef": "sel-0042", "quantity": 2, "assignmentId": "asg-7f3c2d10"},
			{"catalogItemId": "cat-000456", "listingId": "lst-000456-fc-ws-sor-01-sel-0077", "ownerRef": "sel-0077", "quantity": 3, "assignmentId": "asg-2c9d41aa"},
		},
	})
	if err := h.src.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("merged release: %v", err)
	}
	carrier, err := h.store.NetworkSaver().Origin(context.Background(), "ord-51a0c9", 1)
	if err != nil {
		t.Fatalf("carrier origin: %v", err)
	}
	follower, err := h.store.NetworkSaver().Origin(context.Background(), "ord-6b2e07", 1)
	if err != nil {
		t.Fatalf("follower origin: %v", err)
	}
	if carrier.OwnerOf("cat-000123") != "sel-0042" || carrier.OwnerOf("cat-000456") != "" {
		t.Fatalf("carrier owners = %v", carrier.OwnersByItem)
	}
	if follower.OwnerOf("cat-000456") != "sel-0077" || follower.OwnerOf("cat-000123") != "" {
		t.Fatalf("follower owners = %v", follower.OwnersByItem)
	}
}

func TestProducerExamplesFulfillmentLocked(t *testing.T) {
	cases := []struct {
		name, example, orderID string
	}{
		{"single", exampleOrderSourcingFulfillmentLocked, "ord-51a0c9"},
		{"follower of a merged release", exampleOrderSourcingFulfillmentLockedFollower, "ord-6b2e07"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			feedExample(t, h.src, c.example)
			origin, err := h.store.NetworkSaver().Origin(context.Background(), c.orderID, 1)
			if err != nil || origin.NodeID != "node-fc-ws-sor-01" {
				t.Fatalf("origin = %+v %v", origin, err)
			}
		})
	}
}

func TestProducerExampleVendorReturnOutcomeSubmitted(t *testing.T) {
	h := newHarness(t)
	feedExample(t, h.vendor, exampleVendorConnectivityVendorReturnOutcomeSubmitted)
	// No authorization ret-01J9Z6K4M2 exists here: the outcome is recorded as a
	// fact with every line the vendor sent, owner included.
	facts := h.store.OutcomeFactRows()
	if len(facts) != 1 || facts[0].AuthorizationID != "ret-01J9Z6K4M2" || facts[0].NodeID != "n-1" ||
		!facts[0].ReceivedAt.Equal(exampleTime(t, "2026-11-14T12:30:00Z")) {
		t.Fatalf("facts = %+v", facts)
	}
	lines := facts[0].Lines
	if len(lines) != 2 || lines[0].CatalogItemID != "ci-100" || lines[0].QtyRestockable != 2 || lines[0].QtyUnsellable != 1 ||
		lines[1].OwnerRef != "seller-7" || lines[1].QtyUnsellable != 3 {
		t.Fatalf("lines = %+v", lines)
	}
}

// A fact this context cannot read is dead-lettered BEFORE the inbox is
// touched: the claim of an event id is made inside the unit of work that
// applies the fact (usecases.consume), and a payload the handler refuses never
// reaches it. So a dead letter was never claimed in processed_events, and the
// same event id delivered again once it can be read IS applied.
func TestADeadLetteredFactWasNeverClaimedAndAppliesWhenRedelivered(t *testing.T) {
	h := newHarness(t)
	const id = "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c45" // the id of the NodeActivated example
	unreadable := producerEvent(t, exampleNodeTopologyNodeActivated, id, map[string]any{"nodeId": ""})
	if err := h.topo.HandleMessage(context.Background(), unreadable); err == nil {
		t.Fatal("a status fact without a node id can never be applied: it must fail (dead-letter path)")
	}
	if eventType, claimed := h.store.ClaimedType(id); claimed {
		t.Fatalf("the refused fact was claimed in the inbox as %q: a replay would be swallowed", eventType)
	}
	if _, err := h.store.NetworkSaver().Node(context.Background(), "node-0f3a91"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("the refused fact left a row: %v", err)
	}
	// The same id, readable now (the producer's example as published).
	feedExample(t, h.topo, exampleNodeTopologyNodeActivated)
	if got := h.node(t, "node-0f3a91"); got.Lifecycle != network.NodeActive {
		t.Fatalf("the redelivered fact was not applied: %+v", got)
	}
	if eventType, claimed := h.store.ClaimedType(id); !claimed || eventType != "com.retailnetwork.logistics.node-topology.node.NodeActivated" {
		t.Fatalf("claim = %q %v, want the full type", eventType, claimed)
	}
	// And once claimed, a third delivery of the id is swallowed.
	feedExample(t, h.topo, exampleNodeTopologyNodeActivated)
}

// The dead letter found in the cluster's retailnetwork.node-topology.events.dlq
// (failed 2026-11-15T04:40:31Z, x-dlq-error "poison message: malformed event
// payload"): a NodeActivated exactly as node-topology publishes it. It is a
// well-formed fact and is applied.
func TestTheObservedDeadLetterIsAppliedNow(t *testing.T) {
	h := newHarness(t)
	observed := producerEvent(t, exampleNodeTopologyNodeActivated, "e-observed-dead-letter", map[string]any{
		"nodeId": "node-7a93ef1d-9997-4aa5-955f-b57f6a042b32", "activatedAt": "2026-11-14T00:02:44.945943Z",
	})
	if err := h.topo.HandleMessage(context.Background(), observed); err != nil {
		t.Fatalf("the observed dead letter is still refused: %v", err)
	}
	if got := h.node(t, "node-7a93ef1d-9997-4aa5-955f-b57f6a042b32"); got.Lifecycle != network.NodeActive {
		t.Fatalf("node = %+v", got)
	}
}
