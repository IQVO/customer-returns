//go:build integration

package main

import (
	"context"
	"os"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/customer-returns/internal/adapters/inbound/mcp"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/clock"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/ids"
	outboundkafka "github.com/IQVO/customer-returns/internal/adapters/outbound/kafka"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/labels"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/postgres"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// The binary migrates a database itself (idempotently: pgtest's databases are
// already migrated, a second run must be a no-op), then reads through every
// MCP tool exactly what the write use cases persisted, and its reads add no
// outbox row.
func TestMCPAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := pgtest.URL(t) // a fresh, already migrated database
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	countOutbox := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
			t.Fatalf("count outbox_events: %v", err)
		}
		return n
	}

	w := seedThroughWriteUseCases(t, url)
	before := countOutbox()
	if before < 1 {
		t.Fatalf("outbox rows after seeding = %d, want the authorized event", before)
	}

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	assertSeededReads(t, session, w)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the MCP binary wrote an event", before, after)
	}
}

// seeded is the shape of the world the write use cases persist.
type seeded struct {
	authorizationID string
}

// seedThroughWriteUseCases folds the consumer facts of both read models and
// authorizes one return, the way cmd/api does (same use cases, Postgres
// adapters, transactional outbox and inbox).
func seedThroughWriteUseCases(t *testing.T, url string) seeded {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ob := postgres.NewOutboxRepo(pool)
	svc := usecases.New(usecases.Deps{
		UoW:            postgres.NewUnitOfWork(pool),
		Authorizations: postgres.NewAuthorizationRepo(pool),
		Groups:         postgres.NewGroupViewRepo(pool),
		Network:        postgres.NewNetworkViewRepo(pool),
		Outcomes:       postgres.NewOutcomeFactsRepo(pool),
		Labels:         labels.Disabled,
		Outbox:         postgres.NewEventOutbox(ob, outboundkafka.NewEncoder()),
		Inbox:          postgres.NewProcessedEventRepo(pool),
		Clock:          clock.System{}, IDs: ids.UUID{},
	}, usecases.Config{Logger: quietLogger()})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	must(svc.OnOrderPlaced(ctx, "ev-seed-1", requests.OrderPlaced{
		OrderID: "ord-1", PlacedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
		Groups: []requests.PlacedGroup{{
			GroupNo: 1, DestinationPostalPrefix: "22", WitnessNodeID: "node-fc-1",
			Lines: []network.GroupLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}},
		}},
	}))
	must(svc.OnNoticeDecided(ctx, "ev-seed-2", requests.NoticeDecided{
		OrderID: "ord-1", GroupNo: 1, Family: "GROUP_SHIPPED", Status: network.GroupShipped,
	}))
	must(svc.OnNodeFact(ctx, "ev-seed-3", "NodeRegistered", requests.NodeFact{
		NodeID: "node-rc-1", NodeType: network.TypeReturnsCenter, RegionID: "region-east", Lifecycle: network.NodePending,
	}))
	must(svc.OnNodeFact(ctx, "ev-seed-4", "NodeActivated", requests.NodeFact{
		NodeID: "node-rc-1", NodeType: network.TypeReturnsCenter, RegionID: "region-east", Lifecycle: network.NodeActive,
	}))
	must(svc.OnPrefixFact(ctx, "ev-seed-5", requests.PrefixFact{PostalPrefix: "22", RegionID: "region-east", AssignmentVersion: 1}))
	must(svc.OnOriginFact(ctx, "ev-seed-6", "FulfillmentLocked", requests.OriginFact{
		OrderID: "ord-1", GroupNo: 1, NodeID: "node-fc-1", Locked: true,
	}))

	price := int64(20000)
	view, err := svc.AuthorizeReturn(ctx, requests.AuthorizeReturn{
		OrderID: "ord-1", GroupNo: 1,
		Lines:  []requests.AuthorizeLine{{LineNo: 1, UnitPriceCents: &price}},
		Reason: returnauthorization.ReasonDefective, Method: returnauthorization.MethodDropOff,
	})
	must(err)
	return seeded{authorizationID: string(view.AuthorizationID)}
}

// assertSeededReads drives every tool and checks the answer against the
// seeded world.
func assertSeededReads(t *testing.T, session *sdk.ClientSession, w seeded) {
	t.Helper()
	ctx := context.Background()
	call := func(name string, args map[string]any) *sdk.CallToolResult {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: transport error: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s returned a tool error", name)
		}
		return res
	}
	sc := func(res *sdk.CallToolResult) map[string]any {
		t.Helper()
		m, ok := res.StructuredContent.(map[string]any)
		if !ok {
			t.Fatalf("structured content = %#v, want an object", res.StructuredContent)
		}
		return m
	}

	one := sc(call("get_return_authorization", map[string]any{"authorizationId": w.authorizationID}))
	if one["authorizationId"] != w.authorizationID || one["state"] != "AUTHORIZED" ||
		one["disposition"] != "NEAREST_RETURNS_CENTER" || one["destinationNodeId"] != "node-rc-1" || one["pricesAbsent"] != false {
		t.Fatalf("get_return_authorization = %v", one)
	}
	lines, _ := one["lines"].([]any)
	if len(lines) != 1 || lines[0].(map[string]any)["catalogItemId"] != "sku-100" {
		t.Fatalf("lines = %v", one["lines"])
	}
	page := sc(call("list_return_authorizations", map[string]any{"orderId": "ord-1"}))
	items, _ := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["authorizationId"] != w.authorizationID {
		t.Fatalf("list_return_authorizations = %v", page)
	}
}
