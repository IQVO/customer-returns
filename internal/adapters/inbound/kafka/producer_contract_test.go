package kafka_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

// Scenarios over the producers' own examples, read from the pinned specs
// (pinned_examples_test.go): sequences of facts on one aggregate, facts out
// of order, and the dead-letter paths. What ONE example leaves behind when it
// is applied alone is the golden of its type (TestGoldenEventsDecode); these
// tests are about what several of them, or an adapted one, do together. The
// examples are fed as published; an adapted one keeps the producer's shape and
// changes only the fields the scenario names (producerEvent).

func TestProducerExamplesNodeLifecycle(t *testing.T) {
	h := newHarness(t)
	registered := exampleNodeTopologyNodeRegistered
	nodeID := exampleField(t, registered, "nodeId")
	feedExample(t, h.topo, registered)
	want := network.Node{
		NodeID: nodeID, Type: network.NodeType(exampleField(t, registered, "nodeType")),
		RegionID: exampleField(t, registered, "regionId"), Lifecycle: network.NodePending,
	}
	if got := h.node(t, nodeID); got != want {
		t.Fatalf("after NodeRegistered the node = %+v, want %+v", got, want)
	}
	// The three status examples carry the node id and an instant, nothing
	// else: each moves the lifecycle and leaves type and region alone. The
	// producer's examples tell the story of ONE node, in this order.
	steps := []struct {
		example   string
		lifecycle network.NodeLifecycle
	}{
		{exampleNodeTopologyNodeActivated, network.NodeActive},
		{exampleNodeTopologyNodeSuspended, network.NodeSuspended},
		{exampleNodeTopologyNodeRetired, network.NodeRetired},
	}
	for _, s := range steps {
		if other := exampleField(t, s.example, "nodeId"); other != nodeID {
			t.Fatalf("the pinned %s example names node %s, the registration %s: this sequence needs one node", s.lifecycle, other, nodeID)
		}
		feedExample(t, h.topo, s.example)
		// the instant of the change is the CloudEvents `time` of the example
		want.Lifecycle, want.LifecycleAt = s.lifecycle, exampleTime(t, s.example)
		if got := h.node(t, nodeID); got != want {
			t.Fatalf("after the %s fact the node = %+v, want %+v", s.lifecycle, got, want)
		}
	}
}

// Order tolerance (ADR 0003): a status fact whose registration this view
// never saw is a well-formed fact, never a dead letter. It keeps a
// lifecycle-only row; the registration completes it without demoting it.
func TestProducerExampleNodeStatusBeforeItsRegistration(t *testing.T) {
	h := newHarness(t)
	activated, registered := exampleNodeTopologyNodeActivated, exampleNodeTopologyNodeRegistered
	nodeID := exampleField(t, activated, "nodeId")
	if other := exampleField(t, registered, "nodeId"); other != nodeID {
		t.Fatalf("the pinned NodeActivated example names node %s, the registration %s: this scenario needs one node", nodeID, other)
	}
	feedExample(t, h.topo, activated)
	at := exampleTime(t, activated)
	if got, want := h.node(t, nodeID), (network.Node{NodeID: nodeID, Lifecycle: network.NodeActive, LifecycleAt: at}); got != want {
		t.Fatalf("lifecycle-only row = %+v, want %+v", got, want)
	}
	feedExample(t, h.topo, registered)
	want := network.Node{
		NodeID: nodeID, Type: network.NodeType(exampleField(t, registered, "nodeType")),
		RegionID: exampleField(t, registered, "regionId"), Lifecycle: network.NodeActive, LifecycleAt: at,
	}
	if got := h.node(t, nodeID); got != want {
		t.Fatalf("after the late registration the node = %+v, want %+v", got, want)
	}
}

// A status fact older than the one already applied changes nothing: the
// dead-lettered NodeActivated replayed after a newer NodeSuspended.
func TestStaleNodeStatusFactIsANoOp(t *testing.T) {
	h := newHarness(t)
	registered, suspended, activated := exampleNodeTopologyNodeRegistered, exampleNodeTopologyNodeSuspended, exampleNodeTopologyNodeActivated
	if !exampleTime(t, suspended).After(exampleTime(t, activated)) {
		t.Fatalf("the pinned NodeSuspended example (%s) is not later than NodeActivated (%s): this scenario needs it to be",
			exampleAttr(t, suspended, "time"), exampleAttr(t, activated, "time"))
	}
	feedExample(t, h.topo, registered)
	feedExample(t, h.topo, suspended)
	feedExample(t, h.topo, activated) // the older fact, replayed late
	if got := h.node(t, exampleField(t, registered, "nodeId")); got.Lifecycle != network.NodeSuspended {
		t.Fatalf("the older NodeActivated undid the newer status: %+v", got)
	}
}

// The producer's two ServiceAreaAssigned examples are two versions of the
// assignment of ONE prefix: applied in order, the prefix resolves to the
// region both name.
func TestProducerExamplesServiceAreaAssigned(t *testing.T) {
	h := newHarness(t)
	first, handoff := exampleNodeTopologyServiceAreaAssigned, exampleNodeTopologyServiceAreaAssignedCarrierHandoff
	prefix, want := exampleField(t, first, "postalPrefix"), exampleField(t, first, "regionId")
	if exampleField(t, handoff, "postalPrefix") != prefix || exampleField(t, handoff, "regionId") != want {
		t.Fatalf("the two pinned ServiceAreaAssigned examples no longer name one prefix and one region: this scenario needs them to")
	}
	feedExample(t, h.topo, first)
	feedExample(t, h.topo, handoff)
	region, err := h.store.NetworkSaver().RegionOfPrefix(context.Background(), prefix)
	if err != nil || region != want {
		t.Fatalf("region of %s = %q %v, want %s", prefix, region, err, want)
	}
}

// In a merged release a line names its member (`assignmentId`): the owner of a
// line decorates the group that line belongs to and no other. The producer's
// merged example has operator-owned lines only, so this is that example with
// one line per member, given distinct items and owners (the line shape is the
// producer's releasedLine: catalogItemId, listingId, ownerRef, quantity,
// assignmentId).
func TestMergedReleaseOwnersFollowTheMemberOfEachLine(t *testing.T) {
	h := newHarness(t)
	merged := exampleOrderSourcingFulfillmentReleasedToNodeMerged
	members := objects(t, exampleData(t, merged), "members")
	if len(members) != 2 {
		t.Fatalf("the pinned merged example has %d members: this scenario needs a carrier and one follower", len(members))
	}
	msg := producerEvent(t, merged, "e-merged-owners", map[string]any{
		"lines": []map[string]any{
			{"catalogItemId": "cat-000123", "listingId": "lst-000123-fc-ws-sor-01-sel-0042", "ownerRef": "sel-0042", "quantity": 2, "assignmentId": text(t, members[0], "assignmentId")},
			{"catalogItemId": "cat-000456", "listingId": "lst-000456-fc-ws-sor-01-sel-0077", "ownerRef": "sel-0077", "quantity": 3, "assignmentId": text(t, members[1], "assignmentId")},
		},
	})
	if err := h.src.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("merged release: %v", err)
	}
	carrier, err := h.store.NetworkSaver().Origin(context.Background(), text(t, members[0], "orderId"), number(t, members[0]["groupNo"]))
	if err != nil {
		t.Fatalf("carrier origin: %v", err)
	}
	follower, err := h.store.NetworkSaver().Origin(context.Background(), text(t, members[1], "orderId"), number(t, members[1]["groupNo"]))
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

// A fact this context cannot read is dead-lettered BEFORE the inbox is
// touched: the claim of an event id is made inside the unit of work that
// applies the fact (usecases.consume), and a payload the handler refuses never
// reaches it. So a dead letter was never claimed in processed_events, and the
// same event id delivered again once it can be read IS applied.
func TestADeadLetteredFactWasNeverClaimedAndAppliesWhenRedelivered(t *testing.T) {
	h := newHarness(t)
	activated := exampleNodeTopologyNodeActivated
	id, nodeID := exampleAttr(t, activated, "id"), exampleField(t, activated, "nodeId")
	unreadable := producerEvent(t, activated, id, map[string]any{"nodeId": ""})
	if err := h.topo.HandleMessage(context.Background(), unreadable); err == nil {
		t.Fatal("a status fact without a node id can never be applied: it must fail (dead-letter path)")
	}
	if eventType, claimed := h.store.ClaimedType(id); claimed {
		t.Fatalf("the refused fact was claimed in the inbox as %q: a replay would be swallowed", eventType)
	}
	if _, err := h.store.NetworkSaver().Node(context.Background(), nodeID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("the refused fact left a row: %v", err)
	}
	// The same id, readable now (the producer's example as published).
	feedExample(t, h.topo, activated)
	if got := h.node(t, nodeID); got.Lifecycle != network.NodeActive {
		t.Fatalf("the redelivered fact was not applied: %+v", got)
	}
	if eventType, claimed := h.store.ClaimedType(id); !claimed || eventType != exampleAttr(t, activated, "type") {
		t.Fatalf("claim = %q %v, want the full type", eventType, claimed)
	}
	// And once claimed, a third delivery of the id is swallowed.
	feedExample(t, h.topo, activated)
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
