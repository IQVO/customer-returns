package kafka_test

// Consumer contract tests of the four event edges (the fleet's archetypes
// E1-E4, docs/docs/development/contract-testing.md). Each runs per producer,
// against the producer's pinned spec and through this context's REAL
// consumers: the constructors cmd/api wires, their kafka-go reader
// configuration, their handler tables and the use cases behind them.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/customer-returns/internal/adapters/inbound/kafka"
)

// consumedEdge is one producer this context consumes: the name of its pin and
// the consumer that reads its topic.
type consumedEdge struct {
	producer string
	consumer func(*harness) *kafka.Consumer
}

var consumedEdges = []consumedEdge{
	{pinCustomerOrdering, func(h *harness) *kafka.Consumer { return h.order }},
	{pinNodeTopology, func(h *harness) *kafka.Consumer { return h.topo }},
	{pinOrderSourcing, func(h *harness) *kafka.Consumer { return h.src }},
	{pinVendorConnectivity, func(h *harness) *kafka.Consumer { return h.vendor }},
}

// subscribedTopic is the topic the consumer's kafka-go reader was configured
// with: the reader's own configuration, not a constant restated in a test.
func subscribedTopic(t *testing.T, c *kafka.Consumer) string {
	t.Helper()
	reader, ok := c.Reader.(*kafkago.Reader)
	if !ok {
		t.Fatalf("the %s consumer does not read through a kafka-go reader: %T", c.Name, c.Reader)
	}
	topic := reader.Config().Topic
	if topic == "" || topic != c.Topic {
		t.Fatalf("the %s consumer's reader subscribes to %q, its dead-letter topic is derived from %q", c.Name, topic, c.Topic)
	}
	return topic
}

// ownContext is this context's segment of a topic name: its own topics are
// its published contract (apis/asyncapi.yaml), not a pinned one.
const ownContext = "retailnetwork.customer-returns."

var topicLiteral = regexp.MustCompile(`"(retailnetwork\.[a-z-]+\.[a-z.]+)"`)

// foreignTopicLiterals lists every topic of another context that the
// production code of this package names in a string literal.
func foreignTopicLiterals(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range topicLiteral.FindAllStringSubmatch(string(raw), -1) {
			if !strings.HasPrefix(m[1], ownContext) {
				found[m[1]] = true
			}
		}
	}
	return sortedKeys(found)
}

// E1: every topic this context's readers subscribe to is documented by its
// producer's pinned spec, with every type the handlers route on.
func TestPinnedAsyncAPICoversSubscriptions(t *testing.T) {
	h := newHarness(t)
	subscribed := map[string]bool{}
	for _, edge := range consumedEdges {
		c := edge.consumer(h)
		topic := subscribedTopic(t, c)
		subscribed[topic] = true
		t.Run(edge.producer, func(t *testing.T) {
			assertSubscriptionCovered(t, loadPinnedAsyncAPI(t, edge.producer), topic, c)
		})
	}
	// The edge list above is checked against the code, not trusted: a reader
	// added for a fifth topic fails here until its producer is pinned.
	t.Run("every foreign topic in the production code has a pin", func(t *testing.T) {
		literals := foreignTopicLiterals(t)
		for _, topic := range literals {
			if !subscribed[topic] {
				t.Errorf("the production code names %s, which no pinned edge covers: pin its producer (contracts/pinned.json)", topic)
			}
		}
		if len(literals) != len(subscribed) {
			t.Errorf("the pinned edges subscribe to %v, the production code names %v", sortedKeys(subscribed), literals)
		}
	})
}

func assertSubscriptionCovered(t *testing.T, spec pinnedSpec, topic string, c *kafka.Consumer) {
	t.Helper()
	messages := spec.messagesOn(topic)
	if len(messages) == 0 {
		t.Fatalf("the %s consumer subscribes to %s, which the pinned %s spec has no channel for (by key or by address)", c.Name, topic, spec.producer)
	}
	documented := map[string]bool{}
	for _, m := range messages {
		for _, typ := range spec.types(m) {
			documented[typ] = true
		}
	}
	if len(c.Handlers) == 0 {
		t.Fatalf("the %s consumer routes on no type", c.Name)
	}
	for _, typ := range sortedKeys(c.Handlers) {
		if !documented[typ] {
			t.Errorf("the %s consumer routes on %q, which the pinned %s spec does not document on %s", c.Name, typ, spec.producer, topic)
		}
	}
}

// goldenCase is one example of one consumed type, as the pinned spec has it.
type goldenCase struct {
	topic   string
	typ     string
	message pinnedMessage
	ex      pinnedExample
}

func (c goldenCase) name() string { return c.message.name + "/" + c.ex.name }

// goldenCases lists, for one edge, every pinned example of every type the
// real consumer routes on. A routed type the producer gives no example of
// fails here: derive the payload from the pinned schema's required fields and
// say so in the pull request ("derived; producer example backfill pending").
func goldenCases(t *testing.T, edge consumedEdge) []goldenCase {
	t.Helper()
	c := edge.consumer(newHarness(t))
	spec := loadPinnedAsyncAPI(t, edge.producer)
	topic := subscribedTopic(t, c)
	byType := map[string][]goldenCase{}
	for _, m := range spec.messagesOn(topic) {
		for _, ex := range m.examples() {
			typ := ex.attr("type")
			byType[typ] = append(byType[typ], goldenCase{topic: topic, typ: typ, message: m, ex: ex})
		}
	}
	var cases []goldenCase
	for _, typ := range sortedKeys(c.Handlers) {
		if len(byType[typ]) == 0 {
			t.Errorf("the pinned %s spec has no example of %s: derive one from the schema's required fields", edge.producer, typ)
		}
		cases = append(cases, byType[typ]...)
	}
	return cases
}

// E2: every example of every consumed type, read from the pinned spec, is
// applied by the real handler and leaves the state its golden describes; the
// CloudEvents id is the inbox key, so the same id delivered again changes
// nothing even when the redelivery carries other values.
func TestGoldenEventsDecode(t *testing.T) {
	for _, edge := range consumedEdges {
		t.Run(edge.producer, func(t *testing.T) {
			for _, c := range goldenCases(t, edge) {
				t.Run(c.name(), func(t *testing.T) { runGolden(t, edge, c) })
			}
		})
	}
}

func runGolden(t *testing.T, edge consumedEdge, c goldenCase) {
	t.Helper()
	// A fresh state per example: customer-ordering stamps ONE CloudEvents id
	// on all of its examples, and a shared inbox would swallow the second
	// (TestExamplesThatShareAnIDAreOneDeliveryToTheInbox).
	h := newHarness(t)
	consumer := edge.consumer(h)
	g := goldenOf(t, c.typ)
	wire := string(c.ex.wire(t))
	feedExample(t, consumer, wire)
	g.verify(t, h, c.ex)

	id := c.ex.attr("id")
	if claimed, ok := h.store.ClaimedType(id); !ok || claimed != g.claim {
		t.Fatalf("the inbox claim of id %s = %q %v, want %q", id, claimed, ok, g.claim)
	}
	h.clock.Set(now.Add(time.Hour))
	before := h.fingerprint()
	if err := consumer.HandleMessage(context.Background(), producerEvent(t, wire, id, g.replay)); err != nil {
		t.Fatalf("the redelivery of id %s failed: %v", id, err)
	}
	if after := h.fingerprint(); after != before {
		t.Fatalf("the redelivery of id %s was applied:\n before %s\n after  %s", id, before, after)
	}
	g.verify(t, h, c.ex)
}

// fingerprint is the state a redelivery must leave untouched: the order
// groups (their update instant included), the outcome facts and the number of
// domain events.
func (h *harness) fingerprint() string {
	return fmt.Sprintf("groups=%v outcomes=%v events=%d", h.store.Groups(), h.store.OutcomeFactRows(), len(h.store.Events()))
}

// golden is how one consumed type is proven.
type golden struct {
	// claim is the event type the use case claims the id under in the inbox.
	claim string
	// verify asserts the state the real handler leaves after the example,
	// with every expectation read from the example itself.
	verify func(t *testing.T, h *harness, ex pinnedExample)
	// replay overrides `data` fields of a redelivery of the SAME id, chosen so
	// that applying it would change what verify asserts.
	replay map[string]any
}

func goldenOf(t *testing.T, typ string) golden {
	t.Helper()
	for _, set := range []map[string]golden{
		customerOrderingGoldens(), nodeTopologyGoldens(), orderSourcingGoldens(), vendorConnectivityGoldens(),
	} {
		if g, ok := set[typ]; ok {
			return g
		}
	}
	t.Fatalf("no golden describes what %s must leave behind: add one beside its producer's", typ)
	return golden{}
}
