package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/cucumber/godog"

	inboundhttp "github.com/IQVO/customer-returns/internal/adapters/inbound/http"
	inboundkafka "github.com/IQVO/customer-returns/internal/adapters/inbound/kafka"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/memory"
	"github.com/IQVO/customer-returns/internal/application/usecases"
)

type world struct {
	store   *memory.Store
	clock   *memory.Clock
	svc     *usecases.Service
	api     *httptest.Server
	order   *inboundkafka.Consumer
	topo    *inboundkafka.Consumer
	src     *inboundkafka.Consumer
	vendor  *inboundkafka.Consumer
	status  int
	body    string
	lastID  string
	eventID int
}

var start = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func newWorld() *world {
	store := memory.NewStore()
	clock := &memory.Clock{}
	clock.Set(start)
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
	api := httptest.NewServer(inboundhttp.NewRouter(&inboundhttp.Server{Service: svc}, nil, ""))
	logger := newQuietLogger()
	return &world{
		store: store, clock: clock, svc: svc, api: api,
		order:  inboundkafka.NewOrderConsumer([]string{"localhost:9092"}, "g", svc, logger),
		topo:   inboundkafka.NewTopologyConsumer([]string{"localhost:9092"}, "g", svc, logger),
		src:    inboundkafka.NewSourcingConsumer([]string{"localhost:9092"}, "g", svc, logger),
		vendor: inboundkafka.NewVendorConsumer([]string{"localhost:9092"}, "g", svc, logger),
	}
}

// InitializeScenario wires the steps over a fresh world per scenario.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := newWorld()
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.api.Close()
		return ctx, nil
	})
	registerSteps(sc, w)
}

func newQuietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// request drives the REST API of the world.
func (w *world) request(method, path, body string) error {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, w.api.URL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.api.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	w.status, w.body = resp.StatusCode, string(raw)
	return nil
}

// field answers one dotted path of the last response body.
func (w *world) field(path string) (any, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(w.body), &doc); err != nil {
		return nil, err
	}
	cur := any(doc)
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s is not an object", seg)
		}
		cur, ok = obj[seg]
		if !ok {
			return nil, fmt.Errorf("%s missing", seg)
		}
	}
	return cur, nil
}

// feed drives one consumer with one CloudEvent.
func (w *world) feed(c *inboundkafka.Consumer, typ, subject string, data any) error {
	w.eventID++
	body, _ := json.Marshal(data)
	env := map[string]any{
		"specversion": "1.0", "id": fmt.Sprintf("bdd-%d", w.eventID), "source": "/retailnetwork/elsewhere",
		"type": typ, "subject": subject, "time": start.Format(time.RFC3339),
		"datacontenttype": "application/json", "data": json.RawMessage(body),
	}
	raw, _ := json.Marshal(env)
	return c.HandleMessage(context.Background(), raw)
}
