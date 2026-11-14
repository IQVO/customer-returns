//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/IQVO/customer-returns/internal/adapters/inbound/a2a"
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
// already migrated, a second run must be a no-op), then answers the skill over
// the real JSON-RPC wire from exactly what the write use cases persisted, and
// its reads add no outbox row: the agent is read-only, so nothing it does can
// leave an event behind.
func TestA2AAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
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

	srv := httptest.NewServer(newAgent(deps, testAgentURL, "test", quietLogger(), "customer-returns-a2a"))
	defer srv.Close()
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/a2a", Version: "test"})
	client, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}

	assertSeededReads(t, client, w)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the A2A binary wrote an event", before, after)
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

// ask sends one skill request and returns the decoded data part of the reply.
func ask(t *testing.T, client *a2aclient.Client, skill string, args map[string]any) map[string]any {
	t.Helper()
	part := a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args})
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("%s: transport error: %v", skill, err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) != 2 {
		t.Fatalf("%s: reply = %T, want a message with a data and a text part", skill, resp)
	}
	raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	if env["problem"] != nil {
		t.Fatalf("%s answered a problem: %s", skill, raw)
	}
	data, _ := env["data"].(map[string]any)
	return data
}

// assertSeededReads drives the skill and checks the answer against the
// seeded world.
func assertSeededReads(t *testing.T, client *a2aclient.Client, w seeded) {
	t.Helper()
	data := ask(t, client, "get_return_authorization", map[string]any{"authorizationId": w.authorizationID})
	authorization, ok := data["authorization"].(map[string]any)
	if !ok {
		t.Fatalf("data has no authorization object: %v", data)
	}
	if authorization["authorizationId"] != w.authorizationID || authorization["state"] != "AUTHORIZED" ||
		authorization["disposition"] != "NEAREST_RETURNS_CENTER" || authorization["destinationNodeId"] != "node-rc-1" {
		t.Fatalf("get_return_authorization = %v", authorization)
	}
}
