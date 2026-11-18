package kafka_test

import (
	"context"
	"testing"

	"github.com/IQVO/customer-returns/internal/domain/network"
)

// A read model row written before a fix is rebuilt by delivering its fact
// again: the operator deletes the inbox claims of the event type and re-reads
// the topic (the runbook's "Rebuilding a read model from its topic"). These
// tests prove the two things that procedure rests on: a redelivered id that is
// still claimed changes nothing, and the fact applied again repairs the row
// without undoing anything a later fact wrote.

// What the consumer claims an event under, i.e. the value of
// processed_events.event_type the runbook's DELETE names. The node and
// sourcing consumers claim under the full CloudEvents type; the ordering
// consumer under the event name.
const (
	claimNodeRegistered = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"
	claimOrderPlaced    = "CustomerOrderPlaced"
	claimReleased       = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode"
)

func TestRebuildOrderGroupPrefix(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const id = "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40" // the id of the producer's example
	const orderID = "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10"
	feedExample(t, h.order, exampleCustomerOrderingCustomerOrderPlaced)
	if eventType, claimed := h.store.ClaimedType(id); !claimed || eventType != claimOrderPlaced {
		t.Fatalf("claim = %q %v, want %q", eventType, claimed, claimOrderPlaced)
	}
	// The row as the code before the fix left it: no prefix (it read the
	// prefix from the group, where the producer never puts it), and since
	// then a notice made the group DELIVERED.
	row := h.group(orderID, 1)
	row.DestinationPostalPrefix, row.Status = "", network.GroupDelivered
	if err := h.store.GroupSaver().SaveGroup(ctx, row); err != nil {
		t.Fatal(err)
	}

	// Redelivered while still claimed: swallowed, the row is not repaired.
	feedExample(t, h.order, exampleCustomerOrderingCustomerOrderPlaced)
	if got := h.group(orderID, 1); got.DestinationPostalPrefix != "" {
		t.Fatalf("a claimed id was applied again: %+v", got)
	}

	// The operator's DELETE, then the same fact from the topic.
	if n := h.store.ForgetClaims(claimOrderPlaced); n != 1 {
		t.Fatalf("forgot %d claims, want 1", n)
	}
	feedExample(t, h.order, exampleCustomerOrderingCustomerOrderPlaced)
	got := h.group(orderID, 1)
	if got.DestinationPostalPrefix != "787" {
		t.Fatalf("the rebuild did not write the order's prefix: %+v", got)
	}
	if got.Status != network.GroupDelivered {
		t.Fatalf("the rebuild demoted the group's status: %+v", got)
	}
}

func TestRebuildMergedReleaseOrigins(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const id = "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c67" // the id of the producer's merged example
	// What the code before the fix made of the merged release: it did not
	// read `members`, so only the carrier group got an origin.
	carrierOnly := producerEvent(t, exampleOrderSourcingFulfillmentReleasedToNodeMerged, id, map[string]any{"members": []any{}})
	if err := h.src.HandleMessage(ctx, carrierOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.NetworkSaver().Origin(ctx, "ord-6b2e07", 1); err == nil {
		t.Fatal("setup: the follower must have no origin yet")
	}
	if eventType, claimed := h.store.ClaimedType(id); !claimed || eventType != claimReleased {
		t.Fatalf("claim = %q %v, want %q", eventType, claimed, claimReleased)
	}

	feedExample(t, h.src, exampleOrderSourcingFulfillmentReleasedToNodeMerged)
	if _, err := h.store.NetworkSaver().Origin(ctx, "ord-6b2e07", 1); err == nil {
		t.Fatal("a claimed id was applied again")
	}

	if n := h.store.ForgetClaims(claimReleased); n != 1 {
		t.Fatalf("forgot %d claims, want 1", n)
	}
	feedExample(t, h.src, exampleOrderSourcingFulfillmentReleasedToNodeMerged)
	for _, orderID := range []string{"ord-51a0c9", "ord-6b2e07"} {
		origin, err := h.store.NetworkSaver().Origin(ctx, orderID, 1)
		if err != nil || origin.NodeID != "node-fc-ws-sor-01" {
			t.Fatalf("origin of %s after the rebuild = %+v %v", orderID, origin, err)
		}
	}
}

// A node registered before the view kept the label prefix (migration 0005)
// has none, and NodeRegistered is published once per node: the row is
// repaired only by applying that registration again. The rebuild must not
// undo the lifecycle the status facts moved since.
func TestRebuildNodeLabelPrefix(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const id = "node-0f3a91" // the node of the producer's examples
	feedExample(t, h.topo, exampleNodeTopologyNodeRegistered)
	feedExample(t, h.topo, exampleNodeTopologyNodeActivated)
	at := exampleTime(t, "2026-10-22T14:00:00Z")
	// The row as a release before migration 0005 left it: no label prefix.
	before := h.node(t, id)
	before.LabelPostalPrefix = ""
	if err := h.store.NetworkSaver().RegisterNode(ctx, before); err != nil {
		t.Fatal(err)
	}
	noPrefix := network.Node{NodeID: id, Type: network.TypeFulfillmentCenter, RegionID: "region-se", Lifecycle: network.NodeActive, LifecycleAt: at}
	if got := h.node(t, id); got != noPrefix {
		t.Fatalf("setup: the node = %+v, want %+v", got, noPrefix)
	}

	// Redelivered while still claimed (a new consumer group alone): swallowed.
	feedExample(t, h.topo, exampleNodeTopologyNodeRegistered)
	if got := h.node(t, id); got != noPrefix {
		t.Fatalf("a claimed registration was applied again: %+v", got)
	}

	// The operator's DELETE of the NodeRegistered claims, then the topic.
	if n := h.store.ForgetClaims(claimNodeRegistered); n != 1 {
		t.Fatalf("forgot %d claims, want the 1 registration (the NodeActivated claim stays)", n)
	}
	feedExample(t, h.topo, exampleNodeTopologyNodeRegistered)
	feedExample(t, h.topo, exampleNodeTopologyNodeActivated) // still claimed: skipped
	want := noPrefix
	want.LabelPostalPrefix = "07750"
	if got := h.node(t, id); got != want {
		t.Fatalf("after the rebuild the node = %+v, want %+v (prefix filled, still ACTIVE)", got, want)
	}
}
