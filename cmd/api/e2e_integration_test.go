//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The end-to-end proof that the shipped binary is really wired: the test boots
// the REAL entry point (run, in-process) against testcontainers Postgres and
// Kafka with nothing but the environment a deployment sets, and drives it only
// through REST and Kafka. The order, node-topology, sourcing and vendor facts
// go in through the input topics, a shopper authorizes, labels and cancels
// returns through the REST API, and the effects are read from the produced
// topic and Postgres. The vendor's graded outcome goes in through Kafka and
// completes the return. /readyz then follows Kafka going away. Every Kafka
// read is bounded by a deadline; run it with -timeout 300s.

const (
	topicOrdering = "retailnetwork.customer-ordering.events"
	topicTopology = "retailnetwork.node-topology.events"
	topicSourcing = "retailnetwork.order-sourcing.events"
	topicVendor   = "retailnetwork.vendor-connectivity.events"
	topicEvents   = "retailnetwork.customer-returns.events"

	source     = "/retailnetwork/customer-returns"
	typePrefix = "com.retailnetwork.commerce.customer-returns."
	entityName = "returnauthorization"
)

// containerStartTimeout bounds a testcontainers start.
const containerStartTimeout = 120 * time.Second

func startPostgres(t *testing.T) (*tcpostgres.PostgresContainer, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("customer_returns_e2e"), tcpostgres.WithUsername("e2e"), tcpostgres.WithPassword("e2e"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}
	return c, url
}

func startKafka(t *testing.T) (*tckafka.KafkaContainer, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("customer-returns-e2e"))
	if err != nil {
		t.Fatalf("start kafka: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	brokers, err := c.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	return c, brokers
}

// createTopic creates a one-partition topic and waits for its leader.
func createTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if parts, err := conn.ReadPartitions(topic); err == nil && len(parts) == 1 && parts[0].Leader.ID != 0 {
			return
		}
	}
	t.Fatalf("topic %s never got a leader", topic)
}

// produce writes already-encoded messages to topic with RequireAll acks.
func produce(t *testing.T, brokers []string, topic string, msgs ...kafkago.Message) {
	t.Helper()
	transport := &kafkago.Transport{}
	defer transport.CloseIdleConnections()
	w := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic, Balancer: &kafkago.Hash{}, RequiredAcks: kafkago.RequireAll, BatchTimeout: 10 * time.Millisecond, Transport: transport}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.WriteMessages(ctx, msgs...); err != nil {
		t.Fatalf("produce to %s: %v", topic, err)
	}
}

// inputEvent builds a CloudEvent as a sibling context publishes it.
func inputEvent(t *testing.T, id, src, typ, subject, data string) []byte {
	t.Helper()
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(id)
	e.SetSource(src)
	e.SetType(typ)
	e.SetSubject(subject)
	e.SetTime(time.Now().UTC())
	e.SetDataSchema("urn:retailnetwork:e2e:events:X:v1")
	if err := e.SetData("application/json", json.RawMessage(data)); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// consume reads exactly n messages of the one-partition topic from the start,
// bounded by a deadline.
func consume(t *testing.T, brokers []string, topic string, n int) []kafkago.Message {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond})
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.SetOffset(kafkago.FirstOffset); err != nil {
		t.Fatal(err)
	}
	var out []kafkago.Message
	for len(out) < n {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("read %s: got %d of %d messages: %v", topic, len(out), n, err)
		}
		out = append(out, m)
	}
	return out
}

// noMoreMessages fails if the topic holds more than want messages after a
// settling period (nothing was duplicated).
func noMoreMessages(t *testing.T, brokers []string, topic string, want int) {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond})
	defer r.Close()
	if err := r.SetOffset(kafkago.FirstOffset); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := 0
	for {
		if _, err := r.ReadMessage(ctx); err != nil {
			break
		}
		got++
	}
	if got != want {
		t.Fatalf("%s holds %d messages, want exactly %d", topic, got, want)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// service is one running instance of the real entry point.
type service struct {
	t      *testing.T
	base   string
	cancel context.CancelFunc
	done   chan error
}

// env is the environment a deployment sets. logLevel defaults to warn; set it
// to info while debugging a phase.
type env struct {
	dbURL     string
	brokers   []string
	addr      string
	labelsURL string
	logLevel  string
}

func (e env) apply(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", e.dbURL)
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", strings.Join(e.brokers, ","))
	t.Setenv("EVENT_PUBLISHER", "kafka")
	t.Setenv("ORDER_CONSUMER_GROUP", "customer-returns-order-e2e")
	t.Setenv("TOPOLOGY_CONSUMER_GROUP", "customer-returns-topology-e2e")
	t.Setenv("SOURCING_CONSUMER_GROUP", "customer-returns-sourcing-e2e")
	t.Setenv("VENDOR_CONSUMER_GROUP", "customer-returns-vendor-e2e")
	t.Setenv("SHIPPING_LABELS_URL", e.labelsURL)
	t.Setenv("HTTP_ADDR", e.addr)
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0")
	t.Setenv("OUTBOX_RELAY_INTERVAL", "200ms")
	t.Setenv("LOG_LEVEL", firstNonEmpty(e.logLevel, "warn"))
}

// start boots run in-process and waits until the listener answers /healthz.
func (e env) start(t *testing.T) *service {
	t.Helper()
	e.apply(t)
	ctx, cancel := context.WithCancel(context.Background())
	s := &service{t: t, base: "http://" + e.addr, cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- run(ctx) }()
	t.Cleanup(s.stopQuietly)
	waitFor(t, 90*time.Second, "the service to listen on "+e.addr, func() bool {
		select {
		case err := <-s.done:
			t.Fatalf("run returned while booting: %v", err)
		default:
		}
		status, _ := s.try(http.MethodGet, "/healthz", "", "")
		return status == http.StatusOK
	})
	return s
}

func (s *service) stopQuietly() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(60 * time.Second):
	}
}

// expectExit waits for run to return on its own (nobody cancelled it) and
// returns its error.
func (s *service) expectExit() error {
	s.t.Helper()
	select {
	case err := <-s.done:
		s.done <- nil // let the cleanup find it stopped
		return err
	case <-time.After(90 * time.Second):
		s.t.Fatal("run did not return although Kafka is gone")
		return nil
	}
}

// try performs a request and returns -1 when the connection fails. A non-empty
// key is sent as the Idempotency-Key.
func (s *service) try(method, path, body, key string) (int, string) {
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, strings.NewReader(body))
	if err != nil {
		return -1, err.Error()
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return -1, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (s *service) call(method, path, body, key string, want int) string {
	s.t.Helper()
	status, raw := s.try(method, path, body, key)
	if status != want {
		s.t.Fatalf("%s %s = %d %s, want %d", method, path, status, raw, want)
	}
	return raw
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s; last poll: %s", timeout, what, wdiagnostics)
}

// wdiagnostics is set by a phase before a waitFor so its timeout names what
// the last poll actually saw.
var wdiagnostics string

// pgView reads the service's own database.
type pgView struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func newPGView(t *testing.T, url string) pgView {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pgView{t: t, pool: pool}
}

func (v pgView) ids(query string) []string {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := v.pool.Query(ctx, query)
	if err != nil {
		v.t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			v.t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func (v pgView) count(query string, args ...any) int {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int
	if err := v.pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		v.t.Fatalf("%s: %v", query, err)
	}
	return n
}

// The CloudEvents context attributes of a published message, plus its data.
type envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject"`
	Time            string          `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	DataSchema      string          `json:"dataschema"`
	Data            json.RawMessage `json:"data"`
}

func decodeEnvelope(t *testing.T, m kafkago.Message) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(m.Value, &e); err != nil {
		t.Fatalf("not a CloudEvent: %v: %s", err, m.Value)
	}
	return e
}

func mustDecode(t *testing.T, raw string, dest any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

func jsonField(t *testing.T, raw, key string) string {
	t.Helper()
	var m map[string]any
	mustDecode(t, raw, &m)
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("%s has no string field %q", raw, key)
	}
	return v
}

func assertProblem(t *testing.T, body, slug string) {
	t.Helper()
	if !strings.Contains(body, `"type":"`+slug+`"`) {
		t.Fatalf("body = %s, want problem type %s", body, slug)
	}
}

func readyz(s *service) (int, string) { return s.try(http.MethodGet, "/readyz", "", "") }

// assertEnvelope checks every context attribute and the Kafka key/header of a
// message of this service. The aggregate version may differ per occurrence, so
// the dataschema check pins the family, not the version digit.
func assertEnvelope(t *testing.T, m kafkago.Message, name, subject string) envelope {
	t.Helper()
	e := decodeEnvelope(t, m)
	wantType := typePrefix + entityName + "." + name
	schemaPrefix := "urn:retailnetwork:customer-returns:events:" + name + ":v"
	if e.SpecVersion != "1.0" || e.Type != wantType || e.Source != source || !strings.HasPrefix(e.DataSchema, schemaPrefix) ||
		e.DataContentType != "application/json" || e.Subject != subject || e.ID == "" {
		t.Fatalf("envelope = %+v, want 1.0 %s from %s (%s*) about %s", e, wantType, source, schemaPrefix, subject)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.Time); err != nil {
		t.Fatalf("time %q is not RFC 3339: %v", e.Time, err)
	}
	if string(m.Key) != subject {
		t.Fatalf("key = %q, want %q", m.Key, subject)
	}
	ct := ""
	for _, h := range m.Headers {
		if h.Key == "content-type" {
			ct = string(h.Value)
		}
	}
	if ct != "application/cloudevents+json; charset=UTF-8" {
		t.Fatalf("content-type header = %q", ct)
	}
	return e
}

func dataOf(t *testing.T, e envelope) map[string]any {
	t.Helper()
	var d map[string]any
	mustDecode(t, string(e.Data), &d)
	return d
}

// The input-event builders, one per consumed contract.

func orderingEvent(t *testing.T, id, typ, entity, subject, data string) kafkago.Message {
	t.Helper()
	return kafkago.Message{Key: []byte(subject), Value: inputEvent(t, id, "/retailnetwork/customer-ordering",
		"com.retailnetwork.commerce.customer-ordering."+entity+"."+typ, subject, data)}
}

func topologyEvent(t *testing.T, id, entity, typ, subject, data string) kafkago.Message {
	t.Helper()
	return kafkago.Message{Key: []byte(subject), Value: inputEvent(t, id, "/retailnetwork/node-topology",
		"com.retailnetwork.logistics.node-topology."+entity+"."+typ, subject, data)}
}

func sourcingEvent(t *testing.T, id, typ, subject, data string) kafkago.Message {
	t.Helper()
	return kafkago.Message{Key: []byte(subject), Value: inputEvent(t, id, "/retailnetwork/order-sourcing",
		"com.retailnetwork.commerce.order-sourcing.assignment."+typ, subject, data)}
}

func vendorEvent(t *testing.T, id, subject, data string) kafkago.Message {
	t.Helper()
	return kafkago.Message{Key: []byte(subject), Value: inputEvent(t, id, "/retailnetwork/vendor-connectivity",
		"com.retailnetwork.partner.vendor-connectivity.returnoutcome.VendorReturnOutcomeSubmitted", subject, data)}
}

// nodeRegisteredData is node-topology's NodeRegistered payload
// (registeredPayload in its outbound encoder): every field of the contract,
// the three nullable ones null as for a node without a vendor code. subtype
// is raw JSON (`null` or a quoted FULFILLMENT_CENTER subtype).
func nodeRegisteredData(nodeID, nodeType, subtype, postal string) string {
	return fmt.Sprintf(`{"nodeId":%q,"nodeType":%q,"subtype":%s,"operator":"VENDOR","vendorId":"vendor-77","vendorNodeCode":%q,`+
		`"servingStationNodeId":null,"regionId":"region-east","timezone":"America/Sao_Paulo","postalCode":%q,"countryCode":"BR"}`,
		nodeID, nodeType, subtype, "code-"+nodeID, postal)
}

// placedGroupData is one group of customer-ordering's CustomerOrderPlaced
// (placedGroupData in its outbound encoder): no postal prefix on a group.
func placedGroupData(no int, lines string) string {
	return fmt.Sprintf(`{"groupNo":%d,"promiseId":"p-%d","tier":"STANDARD","witnessNodeId":"node-fc-1","shipBy":"2026-11-01T22:00:00Z",`+
		`"deliverByEarliest":"2026-11-02T13:00:00Z","deliverByLatest":"2026-11-03T22:00:00Z","lines":[%s]}`, no, no, lines)
}

// orderPlacedData is customer-ordering's CustomerOrderPlaced payload
// (placedData): the destination postal prefix is the order's.
func orderPlacedData(orderID, prefix, placedAt string, groups ...string) string {
	return fmt.Sprintf(`{"orderId":%q,"shopperId":"shopper-1","destinationPostalPrefix":%q,"placedAt":%q,"groups":[%s]}`,
		orderID, prefix, placedAt, strings.Join(groups, ","))
}

// noticeData is customer-ordering's ShopperNoticeDecided payload
// (shopperNoticeDecidedData) of a terminal family: the fact carries the
// group status.
func noticeData(orderID string, groupNo int, family, status string) string {
	return fmt.Sprintf(`{"orderId":%q,"groupNo":%d,"family":%q,"notifier":"NOBODY","policyVersion":0,`+
		`"noticeKey":"e17a743b013e6726bd9b0dcb238ce8a8909bc9e18237878d103b54b7edba7e76","decidedAt":"2026-11-02T12:00:00Z","fact":{"status":%q}}`,
		orderID, groupNo, family, status)
}

// stubShippingLabels plays the shipping context's POST /return-labels route:
// package 3 is the outage case (503 ship-to-unavailable), everything else
// answers what the route answers on success, 201 Created with a ReturnLabel
// (every required property of that schema). The route is asked for a label to
// the RETURN destination: the stub refuses (400, which the client maps to
// label-unavailable and fails the phase) any destination prefix but the
// returns centre's, 07750 of node-rc-1's 07750-000. The order's own prefix,
// the shopper's, is 220.
func stubShippingLabels(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/return-labels" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			ReturnAuthorizationID string `json:"returnAuthorizationId"`
			OrderID               string `json:"orderId"`
			PackageNo             int    `json:"packageNo"`
			DestinationPrefix     string `json:"destinationPostalPrefix"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ReturnAuthorizationID == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.DestinationPrefix != "07750" {
			t.Errorf("label requested for destination prefix %q, want the returns centre's 07750", body.DestinationPrefix)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Idempotency-Key") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.PackageNo == 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		tracking := fmt.Sprintf("RN-RET-%s-%d", strings.TrimPrefix(body.ReturnAuthorizationID, "ret-"), body.PackageNo)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"labelRef":              "lbl-" + tracking,
			"trackingNumber":        tracking,
			"carrier":               "RN-GROUND",
			"packageNo":             body.PackageNo,
			"orderId":               body.OrderID,
			"tier":                  "STANDARD",
			"status":                "ISSUED",
			"issuedAt":              "2026-11-16T12:00:00Z",
			"purpose":               "RETURN",
			"returnAuthorizationId": body.ReturnAuthorizationID,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// world is what the phases share.
type world struct {
	kc      *tckafka.KafkaContainer
	db      pgView
	brokers []string

	mainID, keepID, pricesID, cancelID string

	events []string // the event names in published order

}

// phaseSetup feeds the two read models through the input topics: an ACTIVE
// returns centre and a fulfilment centre in region-east, the prefix 220 in
// region-east, prefix 310 in region-west (no centre), five order groups of
// ord-1 (four SHIPPED, one still PROMISED) and ord-2 in region-west, plus the
// sourcing facts naming node-fc-1 the origin of ord-1 group 1 with sel-42
// owning sku-100. Every payload has the shape its producer publishes (the
// builders below name the producer's wire struct): the status facts of a node
// carry the node id and an instant only, the destination postal prefix is on
// the order and never on a group.
func phaseSetup(t *testing.T, w *world) {
	t.Helper()
	produce(t, w.brokers, topicTopology,
		topologyEvent(t, "evt-nt1", "node", "NodeRegistered", "node-rc-1", nodeRegisteredData("node-rc-1", "RETURNS_CENTER", "null", "07750-000")),
		topologyEvent(t, "evt-nt2", "node", "NodeActivated", "node-rc-1", `{"nodeId":"node-rc-1","activatedAt":"2026-11-01T09:00:00Z"}`),
		topologyEvent(t, "evt-nt3", "node", "NodeRegistered", "node-fc-1", nodeRegisteredData("node-fc-1", "FULFILLMENT_CENTER", `"SORTABLE"`, "13210-000")),
		topologyEvent(t, "evt-nt4", "node", "NodeActivated", "node-fc-1", `{"nodeId":"node-fc-1","activatedAt":"2026-11-01T09:00:00Z"}`),
		topologyEvent(t, "evt-nt5", "servicearea", "ServiceAreaAssigned", "220", `{"postalPrefix":"220","deliveryStationNodeId":"node-ds-1","regionId":"region-east","assignmentVersion":1}`),
		topologyEvent(t, "evt-nt6", "servicearea", "ServiceAreaAssigned", "310", `{"postalPrefix":"310","deliveryStationNodeId":"node-ds-2","regionId":"region-west","assignmentVersion":1}`))
	const twoLines = `{"lineNo":1,"catalogItemId":"sku-100","quantity":2},{"lineNo":2,"catalogItemId":"sku-200","quantity":1}`
	produce(t, w.brokers, topicOrdering,
		orderingEvent(t, "evt-co1", "CustomerOrderPlaced", "customerorder", "ord-1", orderPlacedData("ord-1", "220", "2026-11-01T10:00:00Z",
			placedGroupData(1, twoLines), placedGroupData(2, twoLines), placedGroupData(3, twoLines), placedGroupData(4, twoLines), placedGroupData(5, twoLines))),
		orderingEvent(t, "evt-co2", "ShopperNoticeDecided", "order", "ord-1", noticeData("ord-1", 1, "GROUP_SHIPPED", "SHIPPED")),
		orderingEvent(t, "evt-co3", "ShopperNoticeDecided", "order", "ord-1", noticeData("ord-1", 2, "GROUP_SHIPPED", "SHIPPED")),
		orderingEvent(t, "evt-co4", "ShopperNoticeDecided", "order", "ord-1", noticeData("ord-1", 3, "GROUP_SHIPPED", "SHIPPED")),
		orderingEvent(t, "evt-co5", "ShopperNoticeDecided", "order", "ord-1", noticeData("ord-1", 5, "GROUP_SHIPPED", "SHIPPED")),
		orderingEvent(t, "evt-co6", "CustomerOrderPlaced", "customerorder", "ord-2", orderPlacedData("ord-2", "310", "2026-11-01T11:00:00Z",
			placedGroupData(1, `{"lineNo":1,"catalogItemId":"sku-300","quantity":1}`))),
		orderingEvent(t, "evt-co7", "ShopperNoticeDecided", "order", "ord-2", noticeData("ord-2", 1, "GROUP_DELIVERED", "DELIVERED")))
	// ord-idem's placed event and its notice: the notice FIRST on purpose --
	// the view must stay SHIPPED when the placed event lands after the fact.
	produce(t, w.brokers, topicOrdering,
		orderingEvent(t, "evt-co8", "ShopperNoticeDecided", "order", "ord-idem", noticeData("ord-idem", 1, "GROUP_SHIPPED", "SHIPPED")),
		orderingEvent(t, "evt-co9", "CustomerOrderPlaced", "customerorder", "ord-idem", orderPlacedData("ord-idem", "220", "2026-11-01T12:00:00Z",
			placedGroupData(1, `{"lineNo":1,"catalogItemId":"sku-100","quantity":1}`))))
	produce(t, w.brokers, topicSourcing,
		sourcingEvent(t, "evt-os1", "FulfillmentReleasedToNode", "asg-1",
			`{"assignmentId":"asg-1","releaseNo":1,"orderId":"ord-1","groupNo":1,"nodeId":"node-fc-1","pickupDepartureId":"dep-pk-1",`+
				`"requiredShipBy":"2026-11-01T21:00:00Z","promisedDeliverByLatest":"2026-11-03T23:00:00Z","tier":"STANDARD","lines":[`+
				`{"catalogItemId":"sku-100","listingId":"lst-sku-100-node-fc-1-sel-42","ownerRef":"sel-42","quantity":2},`+
				`{"catalogItemId":"sku-200","listingId":"lst-sku-200-node-fc-1-sel-42","ownerRef":"sel-42","quantity":1}]}`),
		sourcingEvent(t, "evt-os2", "FulfillmentLocked", "asg-1",
			`{"assignmentId":"asg-1","orderId":"ord-1","groupNo":1,"promiseId":"p-ord-1-1","nodeId":"node-fc-1","pickupDepartureId":"dep-pk-1",`+
				`"lockedAt":"2026-11-01T10:05:00Z","lockReason":"PURCHASE_ORDER_ACCEPTED"}`))
	waitFor(t, 60*time.Second, "the order group and returns network views to be fed", func() bool {
		g1 := w.db.count(`SELECT count(*) FROM order_group_views WHERE order_id='ord-1' AND group_no=1 AND status='SHIPPED'`) == 1
		g2 := w.db.count(`SELECT count(*) FROM order_group_views WHERE order_id='ord-2' AND group_no=1 AND status='DELIVERED'`) == 1
		g3 := w.db.count(`SELECT count(*) FROM order_group_views WHERE order_id='ord-idem' AND group_no=1 AND status='SHIPPED'`) == 1
		n1 := w.db.count(`SELECT count(*) FROM returns_network_nodes WHERE node_id='node-rc-1' AND lifecycle='ACTIVE'`) == 1
		p1 := w.db.count(`SELECT count(*) FROM service_area_prefixes WHERE postal_prefix='220' AND region_id='region-east'`) == 1
		o1 := w.db.count(`SELECT count(*) FROM group_origins WHERE order_id='ord-1' AND group_no=1 AND node_id='node-fc-1'`) == 1
		wdiagnostics = fmt.Sprintf("g1=%v g2=%v g3=%v node=%v prefix=%v origin=%v processed=%d ids=%s",
			g1, g2, g3, n1, p1, o1, w.db.count(`SELECT count(*) FROM processed_events`),
			strings.Join(w.db.ids(`SELECT event_id FROM processed_events ORDER BY event_id`), ","))
		return g1 && g2 && g3 && n1 && p1 && o1
	})
}

// phaseAuthorize drives POST /return-authorizations: the happy path, the
// failure modes of ADR 0002's decide table, the KEEP ceiling and the
// prices-absent honesty flag, then cancels one authorization.
func phaseAuthorize(t *testing.T, s *service, w *world) {
	t.Helper()
	const auth = `{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":20000},{"lineNo":2,"unitPriceCents":900}],"reason":"DEFECTIVE"}`
	body := s.call(http.MethodPost, "/return-authorizations", auth, "auth-1", http.StatusCreated)
	if !strings.Contains(body, `"disposition":"NEAREST_RETURNS_CENTER"`) || !strings.Contains(body, `"destinationNodeId":"node-rc-1"`) ||
		!strings.Contains(body, `"originNodeId":"node-fc-1"`) || !strings.Contains(body, `"state":"AUTHORIZED"`) || !strings.Contains(body, `"pricesAbsent":false`) {
		t.Fatalf("authorized = %s", body)
	}
	w.mainID = jsonField(t, body, "authorizationId")
	if !strings.HasPrefix(w.mainID, "ret-") {
		t.Fatalf("authorization id %q has no ret- prefix", w.mainID)
	}
	if !strings.Contains(body, `"ownerRef":"sel-42"`) {
		t.Fatalf("the release's ownerRef must decorate the lines: %s", body)
	}
	// A replayed key answers the first response byte for byte.
	if again := s.call(http.MethodPost, "/return-authorizations", auth, "auth-1", http.StatusCreated); again != body {
		t.Fatalf("a replayed key must answer the first response: %s vs %s", again, body)
	}
	// GET one.
	if got := s.call(http.MethodGet, "/return-authorizations/"+w.mainID, "", "", http.StatusOK); !strings.Contains(got, w.mainID) {
		t.Fatalf("get = %s", got)
	}
	assertProblem(t, s.call(http.MethodGet, "/return-authorizations/ret-404", "", "", http.StatusNotFound), "not-found")
	// Failure modes: a shipped group in a region with no returns centre and no
	// origin node has no destination at all.
	assertProblem(t, s.call(http.MethodPost, "/return-authorizations",
		`{"orderId":"ord-2","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":5000}],"reason":"NOT_AS_DESCRIBED"}`, "auth-west", http.StatusUnprocessableEntity), "no-return-destination")
	assertProblem(t, s.call(http.MethodPost, "/return-authorizations",
		`{"orderId":"ord-1","groupNo":99,"lines":[{"lineNo":1}],"reason":"DEFECTIVE"}`, "auth-nogroup", http.StatusUnprocessableEntity), "group-not-shipped")
	assertProblem(t, s.call(http.MethodPost, "/return-authorizations",
		`{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":9}],"reason":"DEFECTIVE"}`, "auth-noline", http.StatusUnprocessableEntity), "validation-failed")
	assertProblem(t, s.call(http.MethodPost, "/return-authorizations", `{"orderId":"ord-1","groupNo":1,"reason":"DEFECTIVE"}`, "auth-nolines", http.StatusUnprocessableEntity), "validation-failed")
	// KEEP: the total is under the returnless threshold and every price is known.
	keep := s.call(http.MethodPost, "/return-authorizations",
		`{"orderId":"ord-1","groupNo":2,"lines":[{"lineNo":1,"unitPriceCents":600}],"reason":"NOT_NEEDED"}`, "auth-keep", http.StatusCreated)
	if !strings.Contains(keep, `"disposition":"KEEP"`) || !strings.Contains(keep, `"state":"CLOSED_KEEP"`) || strings.Contains(keep, `"destinationNodeId"`) {
		t.Fatalf("keep = %s", keep)
	}
	w.keepID = jsonField(t, keep, "authorizationId")
	// Prices absent: KEEP can never be chosen and the view says so.
	prices := s.call(http.MethodPost, "/return-authorizations",
		`{"orderId":"ord-1","groupNo":3,"lines":[{"lineNo":1},{"lineNo":2}],"reason":"WRONG_ITEM"}`, "auth-prices", http.StatusCreated)
	if !strings.Contains(prices, `"disposition":"NEAREST_RETURNS_CENTER"`) || !strings.Contains(prices, `"pricesAbsent":true`) {
		t.Fatalf("prices absent = %s", prices)
	}
	w.pricesID = jsonField(t, prices, "authorizationId")
	// One more authorization to cancel, and the keyset list.
	open := s.call(http.MethodPost, "/return-authorizations",
		`{"orderId":"ord-1","groupNo":5,"lines":[{"lineNo":1,"unitPriceCents":4200}],"reason":"OTHER"}`, "auth-cancel", http.StatusCreated)
	w.cancelID = jsonField(t, open, "authorizationId")
	cancelled := s.call(http.MethodPost, "/return-authorizations/"+w.cancelID+"/cancellations", "", "cancel-1", http.StatusOK)
	if !strings.Contains(cancelled, `"state":"CANCELLED"`) || !strings.Contains(cancelled, `"closedAt"`) {
		t.Fatalf("cancelled = %s", cancelled)
	}
	assertProblem(t, s.call(http.MethodPost, "/return-authorizations/"+w.cancelID+"/cancellations", "", "cancel-2", http.StatusConflict), "illegal-transition")
	// The keyset list of ord-1 holds the three open-or-closed authorizations.
	list := s.call(http.MethodGet, "/return-authorizations?orderId=ord-1", "", "", http.StatusOK)
	for _, id := range []string{w.mainID, w.keepID, w.pricesID, w.cancelID} {
		if !strings.Contains(list, `"authorizationId":"`+id+`"`) {
			t.Fatalf("list %s misses %s", list, id)
		}
	}
	assertProblem(t, s.call(http.MethodGet, "/return-authorizations", "", "", http.StatusUnprocessableEntity), "validation-failed")
	w.events = append(w.events, "ReturnAuthorized", "ReturnAuthorized", "ReturnAuthorized", "ReturnAuthorized", "ReturnCancelled")
}

// phaseLabel drives the reverse-label route through the shipping stub: two
// packages linked, the outage answered with a tolerant 422.
func phaseLabel(t *testing.T, s *service, w *world) {
	t.Helper()
	first := s.call(http.MethodPost, "/return-authorizations/"+w.mainID+"/labels", `{"packageNo":1}`, "label-1", http.StatusOK)
	if !strings.Contains(first, `"trackingNumbers":["RN-RET-`) {
		t.Fatalf("label 1 = %s", first)
	}
	second := s.call(http.MethodPost, "/return-authorizations/"+w.mainID+"/labels", `{"packageNo":2}`, "label-2", http.StatusOK)
	if !strings.Contains(second, `"trackingNumbers":["RN-RET-`) || strings.Count(second, "RN-RET-") != 2 {
		t.Fatalf("label 2 = %s", second)
	}
	assertProblem(t, s.call(http.MethodPost, "/return-authorizations/"+w.mainID+"/labels", `{"packageNo":3}`, "label-3", http.StatusUnprocessableEntity), "label-unavailable")
	// A cancelled authorization is never labelable.
	assertProblem(t, s.call(http.MethodPost, "/return-authorizations/"+w.cancelID+"/labels", `{"packageNo":1}`, "label-4", http.StatusConflict), "illegal-transition")
	w.events = append(w.events, "ReturnLabelIssued", "ReturnLabelIssued")
}

// phasePublished asserts the exact CloudEvents of everything driven so far.
func phasePublished(t *testing.T, w *world) {
	t.Helper()
	msgs := consume(t, w.brokers, topicEvents, len(w.events))
	// The subjects in published order: the four authorizations, the
	// cancellation, then the two labels of the main authorization.
	order := []string{w.mainID, w.keepID, w.pricesID, w.cancelID, w.cancelID, w.mainID, w.mainID}
	if len(order) != len(w.events) {
		t.Fatalf("internal: %d subjects for %d events", len(order), len(w.events))
	}
	for i, name := range w.events {
		e := assertEnvelope(t, msgs[i], name, order[i])
		d := dataOf(t, e)
		if d["authorizationId"] != order[i] {
			t.Fatalf("%s data = %v, want authorizationId %s", name, d, order[i])
		}
		switch name {
		case "ReturnAuthorized":
			if i == 0 { // the main authorization: nearest centre, origin, owner refs
				if d["orderId"] != "ord-1" || d["groupNo"] != float64(1) || d["disposition"] != "NEAREST_RETURNS_CENTER" ||
					d["destinationNodeId"] != "node-rc-1" || d["originNodeId"] != "node-fc-1" {
					t.Fatalf("ReturnAuthorized data = %v", d)
				}
				lines, _ := d["lines"].([]any)
				if len(lines) != 2 {
					t.Fatalf("lines = %v", d["lines"])
				}
				l0 := lines[0].(map[string]any)
				if l0["catalogItemId"] != "sku-100" || l0["quantity"] != float64(2) || l0["ownerRef"] != "sel-42" {
					t.Fatalf("line 0 = %v", l0)
				}
				if _, has := l0["unitPriceCents"]; has {
					t.Fatalf("the price is never published: %v", l0)
				}
			}
			if i == 1 { // KEEP: no destination at all
				if d["disposition"] != "KEEP" {
					t.Fatalf("keep event data = %v", d)
				}
				if _, has := d["destinationNodeId"]; has {
					t.Fatalf("KEEP must omit destinationNodeId: %v", d)
				}
			}
		case "ReturnCancelled":
			if d["orderId"] != nil {
				t.Fatalf("ReturnCancelled carries only the ids and the reason: %v", d)
			}
		}
	}
}

// phaseOutcome closes the main authorization with the vendor's graded outcome
// (one line beyond the authorization: recorded, never completed on), proves
// the inbox swallows the duplicate, and reads the final state.
func phaseOutcome(t *testing.T, s *service, w *world) {
	t.Helper()
	// vendor-connectivity's VendorReturnOutcomeSubmitted payload
	// (outcomeSubmittedData in its outbound encoder).
	data := fmt.Sprintf(`{"transactionId":"tx-e2e-1","vendorId":"vendor-77","authorizationId":%q,"nodeId":"node-rc-1","receivedAt":"2026-11-20T09:30:00Z","lines":[`+
		`{"catalogItemId":"sku-100","qtyRestockable":2,"qtyUnsellable":0},`+
		`{"catalogItemId":"sku-200","qtyRestockable":0,"qtyUnsellable":1},`+
		`{"catalogItemId":"sku-999","qtyRestockable":1,"qtyUnsellable":0}]}`, w.mainID)
	produce(t, w.brokers, topicVendor, vendorEvent(t, "evt-out1", w.mainID, data))
	produce(t, w.brokers, topicVendor, vendorEvent(t, "evt-out1", w.mainID, data)) // duplicate id: the inbox swallows it
	waitFor(t, 60*time.Second, "the authorization to complete", func() bool {
		_, body := s.try(http.MethodGet, "/return-authorizations/"+w.mainID, "", "")
		return strings.Contains(body, `"state":"RECEIVED"`)
	})
	w.events = append(w.events, "ReturnCompleted")
	msgs := consume(t, w.brokers, topicEvents, len(w.events))
	e := assertEnvelope(t, msgs[len(msgs)-1], "ReturnCompleted", w.mainID)
	d := dataOf(t, e)
	if d["outcome"] != "RECEIVED" || d["nodeId"] != "node-rc-1" || d["receivedAt"] != "2026-11-20T09:30:00Z" {
		t.Fatalf("ReturnCompleted data = %v", d)
	}
	lines, _ := d["lines"].([]any)
	if len(lines) != 2 { // sku-999 was never authorized: not in the completed fact
		t.Fatalf("completed lines = %v", d["lines"])
	}
	if l1 := lines[1].(map[string]any); l1["catalogItemId"] != "sku-200" || l1["qtyUnsellable"] != float64(1) {
		t.Fatalf("completed line 1 = %v", l1)
	}
	if n := w.db.count(`SELECT count(*) FROM processed_events WHERE event_id='evt-out1'`); n != 1 {
		t.Fatalf("processed_events holds %d rows for the outcome, want 1", n)
	}
	if n := w.db.count(`SELECT count(*) FROM return_outcomes WHERE authorization_id=$1 AND lines::text LIKE '%sku-999%'`, w.mainID); n != 1 {
		t.Fatalf("the unexpected line must be recorded on the outcome facts")
	}
	noMoreMessages(t, w.brokers, topicEvents, len(w.events))
	if n := w.db.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`); n != 0 {
		t.Fatalf("%d outbox rows are still unpublished", n)
	}
}

// phaseKafkaGoesAway proves /readyz follows Kafka going away and the service
// exits so the orchestrator restarts it.
func phaseKafkaGoesAway(t *testing.T, s *service, w *world) {
	t.Helper()
	if status, body := readyz(s); status != http.StatusOK {
		t.Fatalf("readyz = %d %s", status, body)
	}
	stopTimeout := 10 * time.Second
	if err := w.kc.Stop(context.Background(), &stopTimeout); err != nil {
		t.Fatalf("stop kafka: %v", err)
	}
	var body string
	waitFor(t, 60*time.Second, "/readyz to turn 503 once Kafka is gone", func() bool {
		var status int
		status, body = readyz(s)
		return status == http.StatusServiceUnavailable
	})
	if !strings.Contains(body, "dependency-unavailable") || !strings.Contains(body, "kafka") || strings.Contains(body, "postgres") {
		t.Fatalf("readyz with Kafka down = %s, want dependency-unavailable naming only kafka", body)
	}
	if status, body := s.try(http.MethodGet, "/healthz", "", ""); status != http.StatusOK {
		t.Fatalf("healthz with Kafka down = %d %s, want 200 (liveness only)", status, body)
	}
	err := s.expectExit()
	if err == nil || !strings.Contains(err.Error(), "stopped unexpectedly") {
		t.Fatalf("run returned %v after Kafka went away, want a 'stopped unexpectedly' error naming the dead worker", err)
	}
}

func TestRunOnRealPostgresAndKafka(t *testing.T) {
	_, dbURL := startPostgres(t)
	kc, brokers := startKafka(t)
	for _, topic := range []string{topicOrdering, topicTopology, topicSourcing, topicVendor, topicEvents} {
		createTopic(t, brokers, topic)
	}
	w := &world{kc: kc, db: newPGView(t, dbURL), brokers: brokers}
	stub := stubShippingLabels(t)
	e := env{dbURL: dbURL, brokers: brokers, addr: freeAddr(t), labelsURL: stub.URL, logLevel: os.Getenv("E2E_LOG_LEVEL")}
	s := e.start(t)
	phaseSetup(t, w)
	phaseAuthorize(t, s, w)
	phaseLabel(t, s, w)
	phasePublished(t, w)
	phaseOutcome(t, s, w)
	phaseIdempotency(t, s, w)
	phaseKafkaGoesAway(t, s, w)
}
