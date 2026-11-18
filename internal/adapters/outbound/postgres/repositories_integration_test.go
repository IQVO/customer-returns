//go:build integration

package postgres_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/postgres"
	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/testing/pgtest"
)

// The four-places rule (ADR 0002): every persisted field round-trips through
// Snapshot -> Save -> Get -> Restore on REAL Postgres.
func TestAuthorizationRepoRoundTrip(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewAuthorizationRepo(pool)
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	price := int64(400)
	a, err := returnauthorization.Authorize(returnauthorization.NewAuthorization{
		ID: "ret-round", OrderID: "ord-1", GroupNo: 2,
		Lines: []returnauthorization.Line{
			{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2, UnitPriceCents: &price, OwnerRef: "sel-42"},
			{LineNo: 2, CatalogItemID: "sku-200", Quantity: 1},
		},
		Reason: returnauthorization.ReasonWrongItem, Disposition: returnauthorization.NearestReturnsCenter,
		Method: returnauthorization.MethodDropOff, DestinationNodeID: "node-rc-1", OriginNodeID: "node-fc-1",
		AuthorizedAt: at,
	}, at)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	a.Events()
	if err := repo.Save(context.Background(), a); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := repo.Get(context.Background(), "ret-round")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := loaded.LinkLabel("trk-rev-2", at.Add(time.Hour)); err != nil {
		t.Fatalf("LinkLabel: %v", err)
	}
	loaded.Events()
	// One command per Save (the optimistic version moves by one).
	if err := repo.Save(context.Background(), loaded); err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	transiting, err := repo.Get(context.Background(), "ret-round")
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if err := transiting.MarkInTransit(); err != nil {
		t.Fatalf("MarkInTransit: %v", err)
	}
	if err := repo.Save(context.Background(), transiting); err != nil {
		t.Fatalf("Save 3: %v", err)
	}
	final, err := repo.Get(context.Background(), "ret-round")
	if err != nil {
		t.Fatalf("Get 3: %v", err)
	}
	snap := final.Snapshot()
	if snap.State != returnauthorization.StateInTransit || len(snap.TrackingNumbers) != 1 ||
		snap.TrackingNumbers[0] != "trk-rev-2" || len(snap.Lines) != 2 || snap.Lines[0].OwnerRef != "sel-42" {
		t.Fatalf("round trip = %+v", snap)
	}
	if snap.Lines[0].UnitPriceCents == nil || *snap.Lines[0].UnitPriceCents != 400 {
		t.Fatalf("price did not round trip: %+v", snap.Lines[0])
	}
}

func TestAuthorizationRepoConcurrency(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewAuthorizationRepo(pool)
	at := time.Now().UTC()
	a, err := returnauthorization.Authorize(returnauthorization.NewAuthorization{
		ID: "ret-race", OrderID: "ord-1", GroupNo: 1,
		Lines:       []returnauthorization.Line{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 1}},
		Reason:      returnauthorization.ReasonOther,
		Disposition: returnauthorization.NearestReturnsCenter, Method: returnauthorization.MethodDropOff,
		DestinationNodeID: "node-rc-1", AuthorizedAt: at,
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	a.Events()
	if err := repo.Save(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	again, _ := repo.Get(context.Background(), "ret-race")
	again.Events()
	if err := again.Cancel(returnauthorization.CancelShopperRequested, at); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), a); !isConcurrent(err) {
		t.Fatalf("stale save err = %v, want ErrConcurrentUpdate", err)
	}
	_ = repo.Get
}

func isConcurrent(err error) bool {
	return err != nil && err.Error() == ports.ErrConcurrentUpdate.Error()
}

func TestViewRepositoriesRoundTrip(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	groups := postgres.NewGroupViewRepo(pool)
	net := postgres.NewNetworkViewRepo(pool)
	outcomes := postgres.NewOutcomeFactsRepo(pool)

	if err := groups.SaveGroup(ctx, network.OrderGroup{
		OrderID: "ord-1", GroupNo: 1, Status: network.GroupShipped,
		DestinationPostalPrefix: "013", WitnessNodeID: "node-fc-1",
		Lines:     []network.GroupLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}
	g, err := groups.Group(ctx, "ord-1", 1)
	if err != nil || g.Status != network.GroupShipped || len(g.Lines) != 1 {
		t.Fatalf("Group = %+v %v", g, err)
	}

	if err := net.RegisterNode(ctx, network.Node{
		NodeID: "node-rc-1", Type: network.TypeReturnsCenter, RegionID: "region-se",
	}); err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if err := net.SetNodeLifecycle(ctx, "node-rc-1", network.NodeActive, time.Now().UTC()); err != nil {
		t.Fatalf("SetNodeLifecycle: %v", err)
	}
	if err := net.SavePrefix(ctx, network.PrefixAssignment{PostalPrefix: "013", RegionID: "region-se", AssignmentVersion: 2}); err != nil {
		t.Fatalf("SavePrefix: %v", err)
	}
	// a stale assignment version is a no-op
	if err := net.SavePrefix(ctx, network.PrefixAssignment{PostalPrefix: "013", RegionID: "region-ne", AssignmentVersion: 1}); err != nil {
		t.Fatalf("stale SavePrefix: %v", err)
	}
	region, err := net.RegionOfPrefix(ctx, "013")
	if err != nil || region != "region-se" {
		t.Fatalf("region = %q %v", region, err)
	}
	rc, err := net.ReturnsCenterForRegion(ctx, "region-se")
	if err != nil || rc.NodeID != "node-rc-1" {
		t.Fatalf("returns centre = %+v %v", rc, err)
	}

	if err := net.SaveOrigin(ctx, network.GroupOrigin{
		OrderID: "ord-1", GroupNo: 1, NodeID: "node-fc-1",
		OwnersByItem: map[string]string{"sku-100": "sel-42"}, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveOrigin: %v", err)
	}
	origin, err := net.Origin(ctx, "ord-1", 1)
	if err != nil || origin.OwnerOf("sku-100") != "sel-42" {
		t.Fatalf("origin = %+v %v", origin, err)
	}

	if err := outcomes.Record(ctx, requests.OutcomeFact{
		AuthorizationID: "ret-round", NodeID: "node-rc-1", ReceivedAt: time.Now().UTC(),
		Lines: []requests.OutcomeFactLine{{CatalogItemID: "sku-100", QtyRestockable: 1, Unexpected: true}},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func TestMigrationCeiling(t *testing.T) {
	pgtest.NewPool(t) // boots the container and runs every migration
	// The step count is the fleet's migration ceiling: 0001 schema, 0002 stem,
	// 0003 the global Idempotency-Key (REST guideline 2.3), 0004 the node
	// status facts (lifecycle_at, the producer's node types).
	const wantSteps = 4
	matches, err := filepath.Glob("migrations/*.up.sql")
	if err != nil || len(matches) != wantSteps {
		t.Fatalf("migrations = %d steps (%v), want %d (bump the ceiling when you add one)", len(matches), err, wantSteps)
	}
}
