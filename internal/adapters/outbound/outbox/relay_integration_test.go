//go:build integration

package outbox_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/IQVO/customer-returns/internal/adapters/outbound/kafka"
	relay "github.com/IQVO/customer-returns/internal/adapters/outbound/outbox"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/postgres"
	"github.com/IQVO/customer-returns/internal/application/outbox"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/testing/kafkatest"
	"github.com/IQVO/customer-returns/internal/testing/pgtest"
)

// The relay is proven end to end against real infrastructure, both started
// with testcontainers (never skip-gated, never a hardcoded broker address):
// the outbox writes rows in real Postgres, the relay drains them to a real
// broker on a unique topic, and the CloudEvents are read back.

func TestRelay_RealPostgresAndKafka_PublishesCloudEventsKeyedByAggregateID(t *testing.T) {
	ctx := context.Background()
	brokers := kafkatest.Brokers(t)
	pool := pgtest.NewPool(t)
	topic := kafkatest.Topic(t, outboundkafka.TopicEvents)

	repo := postgres.NewOutboxRepo(pool)
	var msgs []outbox.Message
	enc := outboundkafka.NewEncoder()
	for _, id := range []string{"ret-0001", "ret-0002"} {
		a, err := returnauthorization.Authorize(returnauthorization.NewAuthorization{
			ID: returnauthorization.ID(id), OrderID: "ord-" + id, GroupNo: 1,
			Lines:       []returnauthorization.Line{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 1}},
			Reason:      returnauthorization.ReasonOther,
			Disposition: returnauthorization.NearestReturnsCenter, Method: returnauthorization.MethodDropOff,
			DestinationNodeID: "node-rc-1", AuthorizedAt: time.Now(),
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		m, err := enc.Encode(a.Events()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := range m {
			m[i].Topic = topic
		}
		msgs = append(msgs, m...)
	}
	if err := repo.Insert(ctx, msgs...); err != nil {
		t.Fatal(err)
	}

	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	r := relay.NewRelay(repo, sink, slog.New(slog.NewTextHandler(io.Discard, nil)), relay.WithInterval(100*time.Millisecond))
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(runCtx) }()
	t.Cleanup(func() { stop(); <-done })

	typ := "com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnAuthorized"
	got := kafkatest.ReadN(t, topic, 2, 60*time.Second)
	for i, want := range []string{"ret-0001", "ret-0002"} {
		assertCloudEvent(t, got[i], typ, want)
	}
	pgtest.WaitAllPublished(t, pool)
}

// assertCloudEvent checks the key, the content-type header and the decoded
// CloudEvents attributes of one consumed message.
func assertCloudEvent(t *testing.T, msg kafkatest.Message, wantType, wantSubject string) {
	t.Helper()
	if msg.Key != wantSubject {
		t.Errorf("key = %q, want %s", msg.Key, wantSubject)
	}
	if len(msg.Headers) != 1 || msg.Headers["content-type"] != cloudevents.MediaType {
		t.Errorf("headers = %+v", msg.Headers)
	}
	e, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Type() != wantType || e.Subject() != wantSubject || e.Source() != cloudevents.Source {
		t.Fatalf("event = type %q subject %q source %q, want type %q subject %q", e.Type(), e.Subject(), e.Source(), wantType, wantSubject)
	}
}
