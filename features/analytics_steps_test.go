package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/cucumber/godog"
	kafkago "github.com/segmentio/kafka-go"

	inboundkafka "github.com/IQVO/customer-returns/internal/adapters/inbound/kafka"
	"github.com/IQVO/customer-returns/internal/adapters/inbound/reportshttp"
	"github.com/IQVO/customer-returns/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/IQVO/customer-returns/internal/adapters/outbound/kafka"
)

// analyticsWorld is the analytics read side of one scenario (ADR 0012): the
// in-memory twin of the analytical store, the projector's consumer over it and
// the reports router. It never touches the OLTP store except to read the
// events the service published, and it feeds them through the REAL outbox
// encoder, so the projector provably reads what the service writes.
type analyticsWorld struct {
	store    *analyticsstore.MemoryStore
	consumer *inboundkafka.AnalyticsConsumer
	encoder  *outboundkafka.Encoder
	reports  *httptest.Server
	folded   int // how many of the service's published events were already fed
	status   int
	body     map[string]any
}

// captureDLQ keeps dead letters in memory; no scenario here may dead-letter.
type captureDLQ struct{ msgs []kafkago.Message }

func (d *captureDLQ) WriteMessages(_ context.Context, m ...kafkago.Message) error {
	d.msgs = append(d.msgs, m...)
	return nil
}

func (d *captureDLQ) Close() error { return nil }

// reportsNow is the reports' clock: after every instant the scenarios use.
var reportsNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func newAnalyticsWorld() *analyticsWorld {
	store := analyticsstore.NewMemoryStore()
	logger := newQuietLogger()
	return &analyticsWorld{
		store: store,
		consumer: &inboundkafka.AnalyticsConsumer{
			Projection: store, DLQ: &captureDLQ{}, Topic: inboundkafka.OwnEventsTopic,
			DLQTopic: inboundkafka.AnalyticsDLQTopic, Logger: logger,
		},
		encoder: outboundkafka.NewEncoder(),
		reports: httptest.NewServer(reportshttp.NewRouter(&reportshttp.Server{Reader: store, Ping: store, Now: func() time.Time { return reportsNow }}, logger, "")),
	}
}

func registerAnalyticsSteps(sc *godog.ScenarioContext, w *world) {
	sc.When(`^the projector folds the events the service published$`, w.fold)
	sc.When(`^a ReturnCompleted for "([^"]*)" received at "([^"]*)" arrives on the events topic$`, w.legacyReceipt)
	sc.When(`^a legacy flat message that is not a CloudEvent is on the events topic$`, w.legacyFlat)
	sc.When(`^the returns performance report is requested from "([^"]*)" to "([^"]*)"$`, w.requestReport)
	sc.Then(`^the report has (\d+) days$`, w.reportDays)
	sc.Then(`^day "([^"]*)" shows (\d+) authorized and (\d+) received$`, w.dayCounts)
	sc.Then(`^day "([^"]*)" took (\d+) seconds to receive over (\d+) samples$`, w.dayTiming)
	sc.Then(`^day "([^"]*)" has no time to receive$`, w.dayNoTiming)
	sc.Then(`^the reason Pareto is "([^"]*)"$`, w.pareto)
	sc.Then(`^the reason Pareto is empty$`, w.paretoEmpty)
	sc.Then(`^the freshness is set$`, w.freshnessSet)
	sc.Then(`^the freshness is null$`, w.freshnessNull)
	sc.Then(`^the report problem is "([^"]*)" with status (\d+)$`, w.reportProblem)
}

func (w *world) feedTopic(value []byte) error {
	return w.analytics.consumer.HandleMessage(context.Background(), kafkago.Message{Topic: inboundkafka.OwnEventsTopic, Value: value})
}

// fold encodes the events the service published since the last fold through the
// real encoder and hands each message to the projector's consumer.
func (w *world) fold() error {
	events := w.store.Events()
	for _, ev := range events[w.analytics.folded:] {
		msgs, err := w.analytics.encoder.Encode(ev)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if err := w.feedTopic(m.Value); err != nil {
				return err
			}
		}
	}
	w.analytics.folded = len(events)
	return nil
}

func (w *world) legacyReceipt(authorizationID, receivedAt string) error {
	at, err := time.Parse(time.RFC3339, receivedAt)
	if err != nil {
		return err
	}
	w.eventID++
	raw, err := cloudevents.New(cloudevents.Spec{
		ID: fmt.Sprintf("legacy-%d", w.eventID), Entity: "returnauthorization", EventName: "ReturnCompleted", Subject: authorizationID,
		Time: at.Add(time.Minute), Stream: cloudevents.StreamEvents, Version: 3,
		Data: map[string]any{"authorizationId": authorizationID, "outcome": "RECEIVED", "nodeId": "node-rc-1", "receivedAt": receivedAt, "lines": []any{}},
	})
	if err != nil {
		return err
	}
	return w.feedTopic(raw)
}

func (w *world) legacyFlat() error {
	return w.feedTopic([]byte(`{"event_id":"legacy-1","event_type":"ReturnCompleted","authorization_id":"ret-legacy"}`))
}

func (w *world) requestReport(from, to string) error {
	resp, err := w.analytics.reports.Client().Get(w.analytics.reports.URL + "/reports/returns-performance?from=" + from + "&to=" + to)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	w.analytics.status = resp.StatusCode
	w.analytics.body = map[string]any{}
	return json.Unmarshal(raw, &w.analytics.body)
}

func (w *world) days() []map[string]any {
	var out []map[string]any
	list, _ := w.analytics.body["days"].([]any)
	for _, d := range list {
		m, _ := d.(map[string]any)
		out = append(out, m)
	}
	return out
}

func (w *world) day(day string) (map[string]any, error) {
	for _, d := range w.days() {
		if d["day"] == day {
			return d, nil
		}
	}
	return nil, fmt.Errorf("no row for day %s in %v", day, w.analytics.body["days"])
}

func (w *world) reportDays(want int) error {
	if w.analytics.status != http.StatusOK {
		return fmt.Errorf("status = %d, body %v", w.analytics.status, w.analytics.body)
	}
	if got := len(w.days()); got != want {
		return fmt.Errorf("days = %d, want %d: %v", got, want, w.analytics.body["days"])
	}
	return nil
}

func (w *world) dayCounts(day string, authorized, received int) error {
	d, err := w.day(day)
	if err != nil {
		return err
	}
	if d["returnsAuthorized"] != float64(authorized) || d["returnsReceived"] != float64(received) {
		return fmt.Errorf("day %s = %v, want %d authorized and %d received", day, d, authorized, received)
	}
	return nil
}

func (w *world) dayTiming(day string, seconds, samples int) error {
	d, err := w.day(day)
	if err != nil {
		return err
	}
	if d["timeToReceiveP50Seconds"] != float64(seconds) || d["timeToReceiveP95Seconds"] != float64(seconds) || d["timeToReceiveSamples"] != float64(samples) {
		return fmt.Errorf("day %s = %v, want %d seconds over %d samples", day, d, seconds, samples)
	}
	return nil
}

func (w *world) dayNoTiming(day string) error {
	d, err := w.day(day)
	if err != nil {
		return err
	}
	if d["timeToReceiveP50Seconds"] != nil || d["timeToReceiveP95Seconds"] != nil || d["timeToReceiveSamples"] != float64(0) {
		return fmt.Errorf("day %s = %v, want no time to receive", day, d)
	}
	return nil
}

func (w *world) pareto(want string) error {
	list, _ := w.analytics.body["reasonPareto"].([]any)
	var got string
	for i, e := range list {
		m, _ := e.(map[string]any)
		if i > 0 {
			got += ","
		}
		got += fmt.Sprintf("%v=%v", m["reason"], m["returns"])
	}
	if got != want {
		return fmt.Errorf("reasonPareto = %q, want %q", got, want)
	}
	return nil
}

func (w *world) paretoEmpty() error {
	if list, ok := w.analytics.body["reasonPareto"].([]any); !ok || len(list) != 0 {
		return fmt.Errorf("reasonPareto = %v, want []", w.analytics.body["reasonPareto"])
	}
	return nil
}

func (w *world) freshness() (map[string]any, error) {
	f, ok := w.analytics.body["freshness"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no freshness in %v", w.analytics.body)
	}
	return f, nil
}

func (w *world) freshnessSet() error {
	f, err := w.freshness()
	if err != nil {
		return err
	}
	if f["asOf"] == nil || f["lagSeconds"] == nil {
		return fmt.Errorf("freshness = %v, want asOf and lagSeconds", f)
	}
	return nil
}

func (w *world) freshnessNull() error {
	f, err := w.freshness()
	if err != nil {
		return err
	}
	if v, present := f["asOf"]; !present || v != nil || f["lagSeconds"] != nil {
		return fmt.Errorf("freshness = %v, want explicit nulls", f)
	}
	return nil
}

func (w *world) reportProblem(slug string, status int) error {
	if w.analytics.status != status || w.analytics.body["type"] != slug {
		return fmt.Errorf("got %d %v, want %d problem %s", w.analytics.status, w.analytics.body, status, slug)
	}
	return nil
}
