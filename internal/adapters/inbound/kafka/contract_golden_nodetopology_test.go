package kafka_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

// replayProbeNode is a node no example names: a redelivered status fact
// pointed at it must leave no row.
const replayProbeNode = "node-replay-probe"

// nodeTopologyGoldens: what the five node-topology facts leave in the
// ReturnsNetworkView. The node facts claim an id under the full type, the
// prefix fact under its name.
func nodeTopologyGoldens() map[string]golden {
	const prefix = "com.retailnetwork.logistics.node-topology."
	status := func(typ string, lifecycle network.NodeLifecycle) golden {
		return golden{claim: typ, verify: verifyNodeStatus(lifecycle), replay: map[string]any{"nodeId": replayProbeNode}}
	}
	return map[string]golden{
		prefix + "node.NodeRegistered": {
			claim:  prefix + "node.NodeRegistered",
			verify: verifyNodeRegistered,
			replay: map[string]any{"regionId": "region-replayed"},
		},
		prefix + "node.NodeActivated": status(prefix+"node.NodeActivated", network.NodeActive),
		prefix + "node.NodeSuspended": status(prefix+"node.NodeSuspended", network.NodeSuspended),
		prefix + "node.NodeRetired":   status(prefix+"node.NodeRetired", network.NodeRetired),
		prefix + "servicearea.ServiceAreaAssigned": {
			claim:  "ServiceAreaAssigned",
			verify: verifyServiceArea,
			// a newer assignment of the same prefix to another region:
			// applied, it would win
			replay: map[string]any{"regionId": "region-replayed", "assignmentVersion": 2147483647},
		},
	}
}

// verifyNodeRegistered: the registration is the ONE node fact that carries
// the type and the region; a node never seen starts PENDING.
func verifyNodeRegistered(t *testing.T, h *harness, ex pinnedExample) {
	t.Helper()
	data := ex.data()
	want := network.Node{
		NodeID: text(t, data, "nodeId"), Type: network.NodeType(text(t, data, "nodeType")),
		RegionID: text(t, data, "regionId"), Lifecycle: network.NodePending,
	}
	if got := h.node(t, want.NodeID); got != want {
		t.Fatalf("after NodeRegistered the node = %+v, want %+v", got, want)
	}
}

// verifyNodeStatus: a status fact carries the node id and an instant, nothing
// else. Applied to a view that never saw the node it keeps a lifecycle-only
// row, dated by the CloudEvents `time` (the instant has a different name in
// `data` on each of the three types).
func verifyNodeStatus(lifecycle network.NodeLifecycle) func(*testing.T, *harness, pinnedExample) {
	return func(t *testing.T, h *harness, ex pinnedExample) {
		t.Helper()
		nodeID := text(t, ex.data(), "nodeId")
		got := h.node(t, nodeID)
		if got.Lifecycle != lifecycle || !got.LifecycleAt.Equal(instant(t, ex.attr("time"))) || got.Type != "" || got.RegionID != "" {
			t.Fatalf("node = %+v, want a %s lifecycle-only row dated %s", got, lifecycle, ex.attr("time"))
		}
		if _, err := h.store.NetworkSaver().Node(context.Background(), replayProbeNode); !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("a redelivered status fact left a row for %s: %v", replayProbeNode, err)
		}
	}
}

// verifyServiceArea: the prefix belongs to the region of the fact.
func verifyServiceArea(t *testing.T, h *harness, ex pinnedExample) {
	t.Helper()
	data := ex.data()
	prefix, want := text(t, data, "postalPrefix"), text(t, data, "regionId")
	region, err := h.store.NetworkSaver().RegionOfPrefix(context.Background(), prefix)
	if err != nil || region != want {
		t.Fatalf("region of %s = %q %v, want %s", prefix, region, err, want)
	}
}
