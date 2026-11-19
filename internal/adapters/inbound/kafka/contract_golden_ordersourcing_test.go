package kafka_test

import (
	"context"
	"testing"
)

// orderSourcingGoldens: what the two order-sourcing facts leave in the origin
// half of the ReturnsNetworkView. Both claim an id under the full type.
func orderSourcingGoldens() map[string]golden {
	const (
		released = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode"
		locked   = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked"
	)
	return map[string]golden{
		released: {claim: released, verify: verifyReleased, replay: map[string]any{"nodeId": "node-replayed"}},
		locked:   {claim: locked, verify: verifyLocked, replay: map[string]any{"nodeId": "node-replayed"}},
	}
}

// verifyReleased: the release names the node and the per-item owners. A
// merged release is ONE event for several order groups: orderId and groupNo
// name the carrier group only, `members` lists every merged group, and each
// member gets its origin with the owners of ITS lines (a line names its
// member in `assignmentId`).
func verifyReleased(t *testing.T, h *harness, ex pinnedExample) {
	t.Helper()
	data := ex.data()
	nodeID, lines := text(t, data, "nodeId"), objects(t, data, "lines")
	members := asList(data["members"])
	if len(members) == 0 {
		assertOrigin(t, h, originKey{text(t, data, "orderId"), number(t, data["groupNo"])}, nodeID, lineOwners(t, lines, ""))
		return
	}
	for _, m := range members {
		member := asMap(m)
		key := originKey{text(t, member, "orderId"), number(t, member["groupNo"])}
		assertOrigin(t, h, key, nodeID, lineOwners(t, lines, text(t, member, "assignmentId")))
	}
}

// verifyLocked: the lock names the fulfilling node of its own group and
// carries no line.
func verifyLocked(t *testing.T, h *harness, ex pinnedExample) {
	t.Helper()
	data := ex.data()
	assertOrigin(t, h, originKey{text(t, data, "orderId"), number(t, data["groupNo"])}, text(t, data, "nodeId"), map[string]string{})
}

type originKey struct {
	orderID string
	groupNo int
}

// lineOwners maps catalog item to owner for the lines of one member
// (assignmentID), or for every line when assignmentID is empty. A line
// without an ownerRef is operator-owned and has no entry.
func lineOwners(t *testing.T, lines []map[string]any, assignmentID string) map[string]string {
	t.Helper()
	owners := map[string]string{}
	for _, l := range lines {
		owner := asString(l["ownerRef"])
		if owner == "" || (assignmentID != "" && asString(l["assignmentId"]) != assignmentID) {
			continue
		}
		owners[text(t, l, "catalogItemId")] = owner
	}
	return owners
}

func assertOrigin(t *testing.T, h *harness, key originKey, nodeID string, owners map[string]string) {
	t.Helper()
	origin, err := h.store.NetworkSaver().Origin(context.Background(), key.orderID, key.groupNo)
	if err != nil {
		t.Fatalf("origin of %+v: %v", key, err)
	}
	if origin.NodeID != nodeID {
		t.Fatalf("origin of %+v = %+v, want node %s", key, origin, nodeID)
	}
	if len(origin.OwnersByItem) != len(owners) {
		t.Fatalf("owners of %+v = %v, want %v", key, origin.OwnersByItem, owners)
	}
	for item, owner := range owners {
		if origin.OwnerOf(item) != owner {
			t.Fatalf("owner of %s in %+v = %q, want %q", item, key, origin.OwnerOf(item), owner)
		}
	}
}
