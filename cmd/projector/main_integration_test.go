//go:build integration

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	inboundkafka "github.com/IQVO/customer-returns/internal/adapters/inbound/kafka"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/IQVO/customer-returns/internal/adapters/outbound/kafka"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
	"github.com/IQVO/customer-returns/internal/testing/kafkatest"
	"github.com/IQVO/customer-returns/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The real entry point: env -> embedded analytical migrations on an EMPTY
// database -> /readyz -> the consumer projects an event the real encoder wrote
// to the real topic -> a cancelled context drains to a clean exit.
func TestProjectorBootsMigratesProjectsAndStopsCleanly(t *testing.T) {
	url := pgtest.EmptyURL(t)
	brokers := kafkatest.Brokers(t)
	kafkatest.CreateTopic(t, inboundkafka.OwnEventsTopic)
	at := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	authorized := returnauthorization.Authorized{Header: shared.Header{Version: 1, At: at}, AuthorizationID: "ret-projector-1", OrderID: "ord-1", GroupNo: 1,
		Reason: returnauthorization.Reason("DEFECTIVE"), Disposition: returnauthorization.Disposition("KEEP"), Method: returnauthorization.Method("DROP_OFF"),
		Lines: []returnauthorization.EventLine{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 1}}, ExpiresAt: at.Add(30 * 24 * time.Hour), AuthorizedAt: at}
	msgs, err := outboundkafka.NewEncoder().Encode(authorized)
	if err != nil {
		t.Fatal(err)
	}
	kafkatest.Produce(t, inboundkafka.OwnEventsTopic, kafkatest.Message{Key: string(msgs[0].Key), Value: msgs[0].Value})

	addr := freeAddr(t)
	env := map[string]string{"ANALYTICS_DATABASE_URL": url, "KAFKA_BROKERS": brokers[0], "ADMIN_ADDR": addr, "LOG_LEVEL": "error",
		"ANALYTICS_CONSUMER_GROUP": fmt.Sprintf("projector-itest-%d", time.Now().UnixNano())}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }) }()
	t.Cleanup(cancel)

	waitUntil(t, "/readyz 200", func() bool {
		resp, err := http.Get("http://" + addr + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	pool, err := analyticsstore.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	waitUntil(t, "the ReturnAuthorized projected", func() bool {
		var n int
		err := pool.QueryRow(context.Background(), `SELECT count(*) FROM return_authorizations WHERE authorization_id = 'ret-projector-1' AND reason = 'DEFECTIVE'`).Scan(&n)
		return err == nil && n == 1
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled projector must drain cleanly, got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the projector did not stop")
	}
}
