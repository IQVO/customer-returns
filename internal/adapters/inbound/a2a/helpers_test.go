package a2a

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/memory"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// seededService builds the application service over the memory doubles with
// one DELIVERED group, one ACTIVE returns centre, its prefix and one
// authorized return.
func seededService(t *testing.T) (*usecases.Service, returnauthorization.ID) {
	t.Helper()
	store := memory.NewStore()
	clock := memory.NewClockAtNow()
	svc := usecases.New(usecases.Deps{
		UoW:            memory.NewUnitOfWork(store),
		Authorizations: memory.NewAuthorizationRepository(store),
		Groups:         memory.NewGroupViewRepository(store),
		Network:        memory.NewNetworkViewRepository(store),
		Outcomes:       memory.NewOutcomeFacts(store),
		Labels:         memory.NewLabelIssuer(store),
		Outbox:         memory.NewOutbox(store),
		Inbox:          memory.NewInbox(store),
		Clock:          clock, IDs: memory.NewSequentialIDs(),
	}, usecases.Config{})
	ctx := context.Background()
	must(t, store.GroupSaver().SaveGroup(ctx, network.OrderGroup{
		OrderID: "ord-1", GroupNo: 1, Status: network.GroupDelivered,
		DestinationPostalPrefix: "013", Lines: []network.GroupLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}},
		UpdatedAt: clock.Now(),
	}))
	must(t, store.NetworkSaver().SaveNode(ctx, network.Node{
		NodeID: "node-rc-1", Type: network.TypeReturnsCenter, RegionID: "region-se", Lifecycle: network.NodeActive,
	}))
	must(t, store.NetworkSaver().SavePrefix(ctx, network.PrefixAssignment{
		PostalPrefix: "013", RegionID: "region-se", AssignmentVersion: 1,
	}))
	price := int64(9900)
	view, err := svc.AuthorizeReturn(ctx, requests.AuthorizeReturn{
		OrderID: "ord-1", GroupNo: 1,
		Lines:  []requests.AuthorizeLine{{LineNo: 1, UnitPriceCents: &price}},
		Reason: returnauthorization.ReasonDefective, Method: returnauthorization.MethodDropOff,
	})
	must(t, err)
	return svc, view.AuthorizationID
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type wireProblem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		Authorization *struct {
			AuthorizationID string `json:"authorizationId"`
			Disposition     string `json:"disposition"`
			State           string `json:"state"`
			OrderID         string `json:"orderId"`
		} `json:"authorization"`
	} `json:"data"`
	Problem *wireProblem `json:"problem"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	must(t, err)
	return &wire{client: client}
}

// call sends one message part and returns the decoded envelope and summary.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string, string) {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) != 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(text), string(raw)
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	env, text, _ := w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
	return env, text
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d %s problem, got data: %+v", status, slug, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
}
