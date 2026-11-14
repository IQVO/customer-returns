//go:build probe

package kafka_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	inboundkafka "github.com/IQVO/customer-returns/internal/adapters/inbound/kafka"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/clock"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/ids"
	outboundkafka "github.com/IQVO/customer-returns/internal/adapters/outbound/kafka"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/labels"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/postgres"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/testing/kafkatest"
	"github.com/IQVO/customer-returns/internal/testing/pgtest"
)

// TestProbeConsumer mirrors the failing e2e phase: full deps, the fixed topic,
// the e2e's exact CustomerOrderPlaced payload, then dumps what landed.
func TestProbeConsumer(t *testing.T) {
	brokers := kafkatest.Brokers(t)
	url := pgtest.URL(t)
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := usecases.New(usecases.Deps{
		UoW:            postgres.NewUnitOfWork(pool),
		Groups:         postgres.NewGroupViewRepo(pool),
		Network:        postgres.NewNetworkViewRepo(pool),
		Authorizations: postgres.NewAuthorizationRepo(pool),
		Outcomes:       postgres.NewOutcomeFactsRepo(pool),
		Labels:         labels.Disabled,
		Inbox:          postgres.NewProcessedEventRepo(pool),
		Outbox:         postgres.NewEventOutbox(postgres.NewOutboxRepo(pool), outboundkafka.NewEncoder()),
		Clock:          clock.System{}, IDs: ids.UUID{},
	}, usecases.Config{})
	topic := "retailnetwork.customer-ordering.events"
	kafkatest.CreateTopic(t, topic)
	c := inboundkafka.NewOrderConsumer(brokers, "probe-group-2", svc, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	defer func() { cancel(); select { case <-done: case <-time.After(20 * time.Second): } }()

	time.Sleep(2 * time.Second)
	placed := `{"orderId":"ord-1","placedAt":"2026-11-01T10:00:00Z","groups":[{"groupNo":1,"destinationPostalPrefix":"22","witnessNodeId":"node-fc-1","lines":[{"LineNo":1,"CatalogItemID":"sku-100","Quantity":2}]}]}`
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID("probe-2")
	e.SetSource("/retailnetwork/customer-ordering")
	e.SetType("com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced")
	e.SetSubject("ord-1")
	e.SetTime(time.Now().UTC())
	e.SetDataSchema("urn:retailnetwork:e2e:events:X:v1")
	if err := e.SetData("application/json", json.RawMessage(placed)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	w := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic, Balancer: &kafkago.Hash{}, RequiredAcks: kafkago.RequireAll}
	if err := w.WriteMessages(context.Background(), kafkago.Message{Key: []byte("ord-1"), Value: raw}); err != nil {
		t.Fatalf("produce: %v", err)
	}
	w.Close()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var groups, processed int
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM order_group_views`).Scan(&groups)
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM processed_events`).Scan(&processed)
		if groups > 0 {
			t.Logf("FED: groups=%d processed=%d", groups, processed)
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	var processed int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM processed_events`).Scan(&processed)
	t.Fatalf("view never fed; processed_events=%d", processed)
}
