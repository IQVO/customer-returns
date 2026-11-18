// Package repocontract holds the contract every implementation of an out port
// must keep, written ONCE and run against both the in-memory double (unit
// tests) and the Postgres adapter (testcontainers): a behaviour the double has
// and the real adapter lacks, or the reverse, fails here instead of in
// production.
package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

var (
	t0 = time.Date(2026, 11, 14, 0, 2, 44, 945943000, time.UTC)
	t1 = t0.Add(time.Hour)
)

// nodeFixture drives one repository through the node contract.
type nodeFixture struct {
	t    *testing.T
	repo ports.NetworkViewRepository
}

func (f nodeFixture) register(n network.Node) {
	f.t.Helper()
	if err := f.repo.RegisterNode(context.Background(), n); err != nil {
		f.t.Fatalf("RegisterNode(%s): %v", n.NodeID, err)
	}
}

func (f nodeFixture) status(nodeID string, lifecycle network.NodeLifecycle, at time.Time) {
	f.t.Helper()
	if err := f.repo.SetNodeLifecycle(context.Background(), nodeID, lifecycle, at); err != nil {
		f.t.Fatalf("SetNodeLifecycle(%s, %s): %v", nodeID, lifecycle, err)
	}
}

// want asserts the stored row, field by field.
func (f nodeFixture) want(want network.Node) {
	f.t.Helper()
	got, err := f.repo.Node(context.Background(), want.NodeID)
	if err != nil {
		f.t.Fatalf("Node(%s): %v", want.NodeID, err)
	}
	if !got.LifecycleAt.Equal(want.LifecycleAt) {
		f.t.Fatalf("node %s lifecycleAt = %s, want %s", want.NodeID, got.LifecycleAt, want.LifecycleAt)
	}
	got.LifecycleAt = want.LifecycleAt
	if got != want {
		f.t.Fatalf("node = %+v, want %+v", got, want)
	}
}

// labelPrefix is the label prefix every returns centre of the suite is
// registered with, unless a case says otherwise.
const labelPrefix = "07750"

// returnsCentre is the row a NodeRegistered of a returns centre asks for.
func returnsCentre(nodeID, regionID string) network.Node {
	return network.Node{NodeID: nodeID, Type: network.TypeReturnsCenter, RegionID: regionID, LabelPostalPrefix: labelPrefix}
}

// held is the row the view must hold for a returns centre in the given
// lifecycle (zero `at` while no status fact was applied).
func held(nodeID, regionID string, lifecycle network.NodeLifecycle, at time.Time) network.Node {
	n := returnsCentre(nodeID, regionID)
	n.Lifecycle, n.LifecycleAt = lifecycle, at
	return n
}

// NetworkNodes is the contract of the node half of the ReturnsNetworkView
// (ADR 0003): NodeRegistered owns the descriptive attributes (type, region,
// label prefix), the status facts own the lifecycle, and neither write touches
// what the other wrote.
// Every case uses its own node id and its own region, so the suite runs on
// one store.
func NetworkNodes(t *testing.T, repo ports.NetworkViewRepository) {
	t.Helper()
	fixture := func(t *testing.T) nodeFixture { return nodeFixture{t: t, repo: repo} }

	t.Run("an unknown node is not found", func(t *testing.T) {
		if _, err := repo.Node(context.Background(), "node-nobody"); !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("Node(unknown) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("a registered node starts PENDING with its attributes", func(t *testing.T) {
		f := fixture(t)
		f.register(returnsCentre("node-a", "region-a"))
		f.want(held("node-a", "region-a", network.NodePending, time.Time{}))
	})

	t.Run("the lifecycle a registration is called with is not read", func(t *testing.T) {
		f := fixture(t)
		f.register(held("node-a2", "region-a2", network.NodeActive, t1))
		f.want(held("node-a2", "region-a2", network.NodePending, time.Time{}))
	})

	t.Run("a status fact moves the lifecycle and nothing else", func(t *testing.T) {
		const id, region = "node-b", "region-b"
		f := fixture(t)
		f.register(returnsCentre(id, region))
		f.status(id, network.NodeActive, t0)
		f.want(held(id, region, network.NodeActive, t0))
		rc, err := repo.ReturnsCenterForRegion(context.Background(), region)
		if err != nil || rc.NodeID != id {
			t.Fatalf("ReturnsCenterForRegion = %+v %v, want the ACTIVE %s", rc, err, id)
		}
		f.status(id, network.NodeSuspended, t1)
		f.want(held(id, region, network.NodeSuspended, t1))
		if _, err := repo.ReturnsCenterForRegion(context.Background(), region); !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("a SUSPENDED centre is no destination: err = %v", err)
		}
	})

	t.Run("a registration applied again never demotes the lifecycle", func(t *testing.T) {
		const id = "node-c"
		f := fixture(t)
		f.register(returnsCentre(id, "region-c"))
		f.status(id, network.NodeActive, t0)
		f.register(returnsCentre(id, "region-c2"))
		f.want(held(id, "region-c2", network.NodeActive, t0))
	})

	t.Run("a registration applied again fills the label prefix of a row that had none", func(t *testing.T) {
		const id, region = "node-c3", "region-c3"
		f := fixture(t)
		// The row as a release before the prefix was kept left it, then ACTIVE.
		before := returnsCentre(id, region)
		before.LabelPostalPrefix = ""
		f.register(before)
		f.status(id, network.NodeActive, t0)
		noPrefix := held(id, region, network.NodeActive, t0)
		noPrefix.LabelPostalPrefix = ""
		f.want(noPrefix)
		// The same registration, applied again by the rebuild.
		f.register(returnsCentre(id, region))
		f.want(held(id, region, network.NodeActive, t0))
	})

	t.Run("a status fact never erases the label prefix", func(t *testing.T) {
		const id, region = "node-c4", "region-c4"
		f := fixture(t)
		f.register(returnsCentre(id, region))
		for i, lifecycle := range []network.NodeLifecycle{network.NodeActive, network.NodeSuspended, network.NodeRetired} {
			at := t0.Add(time.Duration(i) * time.Minute)
			f.status(id, lifecycle, at)
			f.want(held(id, region, lifecycle, at))
		}
	})

	t.Run("a status fact older than the one applied is a no-op", func(t *testing.T) {
		const id, region = "node-d", "region-d"
		f := fixture(t)
		f.register(returnsCentre(id, region))
		f.status(id, network.NodeSuspended, t1)
		f.status(id, network.NodeActive, t0) // a dead letter replayed late
		f.want(held(id, region, network.NodeSuspended, t1))
		f.status(id, network.NodeRetired, t1) // the same instant still applies
		f.want(held(id, region, network.NodeRetired, t1))
	})

	t.Run("a status fact for a node never seen keeps a lifecycle-only row the registration completes", func(t *testing.T) {
		const id = "node-e"
		f := fixture(t)
		f.status(id, network.NodeActive, t0)
		f.want(network.Node{NodeID: id, Lifecycle: network.NodeActive, LifecycleAt: t0})
		if _, err := repo.ReturnsCenterForRegion(context.Background(), ""); !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("a lifecycle-only row is never a returns centre: err = %v", err)
		}
		f.register(returnsCentre(id, "region-e"))
		f.want(held(id, "region-e", network.NodeActive, t0))
	})
}
