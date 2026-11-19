package network

import (
	"testing"
	"time"
)

var (
	statusT0 = time.Date(2026, 11, 14, 0, 2, 44, 0, time.UTC)
	statusT1 = statusT0.Add(time.Hour)
)

// TestNodeWithRegistration pins what a NodeRegistered fact does to a row: it
// writes the descriptive attributes and starts a new node PENDING, and it
// never demotes a lifecycle a status fact already set.
func TestNodeWithRegistration(t *testing.T) {
	cases := []struct {
		name string
		held Node
		want Node
	}{
		{
			name: "a node never seen starts PENDING",
			held: Node{NodeID: "node-1"},
			want: Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodePending},
		},
		{
			name: "an ACTIVE node keeps its lifecycle and its status instant",
			held: Node{NodeID: "node-1", Type: TypeFulfillmentCenter, RegionID: "region-old", Lifecycle: NodeActive, LifecycleAt: statusT0},
			want: Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodeActive, LifecycleAt: statusT0},
		},
		{
			name: "a SUSPENDED node is not demoted",
			held: Node{NodeID: "node-1", Lifecycle: NodeSuspended, LifecycleAt: statusT0},
			want: Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodeSuspended, LifecycleAt: statusT0},
		},
		{
			name: "a RETIRED node is not demoted",
			held: Node{NodeID: "node-1", Lifecycle: NodeRetired, LifecycleAt: statusT0},
			want: Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodeRetired, LifecycleAt: statusT0},
		},
		{
			name: "a PENDING node stays PENDING",
			held: Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodePending},
			want: Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodePending},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.held.WithRegistration(TypeReturnsCenter, "region-se"); got != c.want {
				t.Fatalf("WithRegistration = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestNodeWithStatus pins the status facts: only the lifecycle (and its
// instant) moves, and a fact older than the one already applied is stale.
func TestNodeWithStatus(t *testing.T) {
	registered := Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodePending}
	active := Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodeActive, LifecycleAt: statusT1}
	cases := []struct {
		name      string
		held      Node
		lifecycle NodeLifecycle
		at        time.Time
		want      Node
		applied   bool
	}{
		{
			name: "the first status fact applies and keeps type and region",
			held: registered, lifecycle: NodeActive, at: statusT0,
			want:    Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodeActive, LifecycleAt: statusT0},
			applied: true,
		},
		{
			name: "a newer fact applies",
			held: active, lifecycle: NodeSuspended, at: statusT1.Add(time.Nanosecond),
			want:    Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodeSuspended, LifecycleAt: statusT1.Add(time.Nanosecond)},
			applied: true,
		},
		{
			name: "a fact at the very same instant applies",
			held: active, lifecycle: NodeSuspended, at: statusT1,
			want:    Node{NodeID: "node-1", Type: TypeReturnsCenter, RegionID: "region-se", Lifecycle: NodeSuspended, LifecycleAt: statusT1},
			applied: true,
		},
		{
			name: "an older fact is stale and changes nothing",
			held: active, lifecycle: NodeSuspended, at: statusT1.Add(-time.Nanosecond),
			want: active, applied: false,
		},
		{
			name: "a node never seen gets a lifecycle-only row",
			held: Node{NodeID: "node-9"}, lifecycle: NodeRetired, at: statusT0,
			want:    Node{NodeID: "node-9", Lifecycle: NodeRetired, LifecycleAt: statusT0},
			applied: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, applied := c.held.WithStatus(c.lifecycle, c.at)
			if got != c.want || applied != c.applied {
				t.Fatalf("WithStatus = %+v applied=%v, want %+v applied=%v", got, applied, c.want, c.applied)
			}
		})
	}
}
