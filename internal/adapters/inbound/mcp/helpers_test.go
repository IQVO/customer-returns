package mcp_test

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/customer-returns/internal/adapters/inbound/mcp"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/memory"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// now is the fixture's fixed "now".
var now = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// harness is a connected in-memory MCP client over the real server wired to
// the application service over in-memory repositories, exactly as cmd/mcp
// wires them (minus Postgres). The write use cases exist ONLY here, to seed
// data the way the REST adapter would: the MCP surface itself has none.
type harness struct {
	session *sdk.ClientSession
	store   *memory.Store
	svc     *usecases.Service
	clock   *memory.Clock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := memory.NewStore()
	clock := &memory.Clock{}
	clock.Set(now)
	deps := usecases.Deps{
		UoW:            memory.NewUnitOfWork(store),
		Authorizations: memory.NewAuthorizationRepository(store),
		Groups:         memory.NewGroupViewRepository(store),
		Network:        memory.NewNetworkViewRepository(store),
		Outcomes:       memory.NewOutcomeFacts(store),
		Labels:         memory.NewLabelIssuer(store),
		Outbox:         memory.NewOutbox(store),
		Inbox:          memory.NewInbox(store),
		Clock:          clock, IDs: memory.NewSequentialIDs(),
	}
	svc := usecases.New(deps, usecases.Config{})
	return &harness{
		session: connectSession(t, inboundmcp.Deps{Reads: svc}),
		store:   store, svc: svc, clock: clock,
	}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// seedReturn authorizes one return the way the REST adapter would and
// returns its id.
func (h *harness) seedReturn(t *testing.T, orderID string, disposition returnauthorization.Disposition) returnauthorization.ID {
	t.Helper()
	ctx := context.Background()
	h.clock.Set(now)
	if err := h.store.GroupSaver().SaveGroup(ctx, network.OrderGroup{
		OrderID: orderID, GroupNo: 1, Status: network.GroupDelivered,
		DestinationPostalPrefix: "013", Lines: []network.GroupLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if err := h.store.NetworkSaver().RegisterNode(ctx, network.Node{
		NodeID: "node-rc-1", Type: network.TypeReturnsCenter, RegionID: "region-se",
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := h.store.NetworkSaver().SetNodeLifecycle(ctx, "node-rc-1", network.NodeActive, now); err != nil {
		t.Fatalf("activate node: %v", err)
	}
	if err := h.store.NetworkSaver().SavePrefix(ctx, network.PrefixAssignment{
		PostalPrefix: "013", RegionID: "region-se", AssignmentVersion: 1,
	}); err != nil {
		t.Fatalf("seed prefix: %v", err)
	}
	p := int64(9900)
	view, err := h.svc.AuthorizeReturn(ctx, requests.AuthorizeReturn{
		OrderID: orderID, GroupNo: 1,
		Lines:  []requests.AuthorizeLine{{LineNo: 1, UnitPriceCents: &p}},
		Reason: returnauthorization.ReasonDefective,
		Method: returnauthorization.MethodDropOff,
	})
	if err != nil {
		t.Fatalf("AuthorizeReturn: %v", err)
	}
	return view.AuthorizationID
}
