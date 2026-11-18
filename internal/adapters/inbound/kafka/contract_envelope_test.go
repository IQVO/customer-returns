package kafka_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/kafka/cloudevents"
)

// envelopeAttributes are the CloudEvents attributes every network event
// carries beside specversion (the fleet's CloudEvents rule).
var envelopeAttributes = []string{"id", "source", "type", "subject", "time", "datacontenttype", "dataschema"}

// networkTiers are the business-area labels of a network event type.
var networkTiers = map[string]bool{"commerce": true, "logistics": true, "partner": true}

// E3: the envelope of every consumed example. What the producer's schema
// fixes with a `const` is compared with that const; what it leaves open is
// held to the network's naming, with the context, the stream and the event
// name read from the pin (never restated here).
func TestCloudEventsEnvelopeInvariants(t *testing.T) {
	for _, edge := range consumedEdges {
		t.Run(edge.producer, func(t *testing.T) {
			spec := loadPinnedAsyncAPI(t, edge.producer)
			for _, c := range goldenCases(t, edge) {
				t.Run(c.name(), func(t *testing.T) {
					assertEnvelope(t, spec, c)
					assertNetworkNames(t, spec.producer, c)
					assertDecodes(t, c.ex)
				})
			}
		})
	}
}

func assertEnvelope(t *testing.T, spec pinnedSpec, c goldenCase) {
	t.Helper()
	if got := c.ex.attr("specversion"); got != "1.0" {
		t.Errorf("specversion = %q, want 1.0", got)
	}
	for _, attribute := range envelopeAttributes {
		if c.ex.attr(attribute) == "" {
			t.Errorf("the mandatory attribute %q is empty", attribute)
		}
	}
	if _, err := time.Parse(time.RFC3339, c.ex.attr("time")); err != nil {
		t.Errorf("time %q is not RFC 3339: %v", c.ex.attr("time"), err)
	}
	consts := spec.envelopeConsts(c.message)
	for _, attribute := range sortedKeys(consts) {
		if got := c.ex.attr(attribute); got != consts[attribute] {
			t.Errorf("%s = %q, the pinned schema fixes it to %q", attribute, got, consts[attribute])
		}
	}
	if got := c.ex.attr("datacontenttype"); got != "application/json" {
		t.Errorf("datacontenttype = %q, want application/json", got)
	}
	if got := spec.contentType(c.message); got != cloudeventsContentType {
		t.Errorf("the message's content type = %q, want %s", got, cloudeventsContentType)
	}
}

// cloudeventsContentType is the media type of a structured-mode CloudEvent,
// the Kafka `content-type` of every network message.
const cloudeventsContentType = "application/cloudevents+json"

// assertNetworkNames holds the example to the network's naming:
// type com.retailnetwork.<tier>.<context>.<entity>.<Event>, source
// /retailnetwork/<context>, dataschema
// urn:retailnetwork:<context>:<stream>:<Event>:v<n>. The context is the
// producer of the pin, the stream is the last segment of the subscribed topic
// and the event is the pinned message's name.
func assertNetworkNames(t *testing.T, producer string, c goldenCase) {
	t.Helper()
	parts := strings.Split(c.ex.attr("type"), ".")
	if len(parts) != 6 || parts[0] != "com" || parts[1] != "retailnetwork" || !networkTiers[parts[2]] ||
		parts[3] != producer || parts[4] == "" || parts[5] != c.message.name {
		t.Errorf("type %q is not com.retailnetwork.<tier>.%s.<entity>.%s", c.ex.attr("type"), producer, c.message.name)
	}
	if got := c.ex.attr("source"); got != "/retailnetwork/"+producer {
		t.Errorf("source = %q, want /retailnetwork/%s", got, producer)
	}
	stream := c.topic[strings.LastIndex(c.topic, ".")+1:]
	family := "urn:retailnetwork:" + producer + ":" + stream + ":" + c.message.name + ":v"
	version, found := strings.CutPrefix(c.ex.attr("dataschema"), family)
	if n, err := strconv.Atoi(version); !found || err != nil || n < 1 {
		t.Errorf("dataschema %q is not %s<n>", c.ex.attr("dataschema"), family)
	}
}

// assertDecodes: the example is a valid CloudEvents 1.0 event for the helper
// every consumer decodes with.
func assertDecodes(t *testing.T, ex pinnedExample) {
	t.Helper()
	e, err := cloudevents.Decode(ex.wire(t))
	if err != nil {
		t.Fatalf("the producer's example is not a valid CloudEvent for this context's decoder: %v", err)
	}
	if e.ID() != ex.attr("id") || e.Type() != ex.attr("type") || e.Subject() != ex.attr("subject") || e.Source() != ex.attr("source") {
		t.Errorf("decoded id/type/subject/source = %q %q %q %q, the example says %q %q %q %q",
			e.ID(), e.Type(), e.Subject(), e.Source(), ex.attr("id"), ex.attr("type"), ex.attr("subject"), ex.attr("source"))
	}
}

// probeExtension is an extension attribute no consumer knows. CloudEvents
// attribute names are lowercase letters and digits only: an underscore would
// make the event invalid and the test would prove the wrong thing.
const probeExtension = "xconsumercontractprobe"

// withUnknownKeys adds a key this context does not know to every object of a
// decoded JSON value.
func withUnknownKeys(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{"aFieldThisVersionDoesNotKnow": map[string]any{"forward": []any{"compatibility", 1}}}
		for k, item := range x {
			out[k] = withUnknownKeys(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, withUnknownKeys(item))
		}
		return out
	}
	return v
}

// perturbed is the example with its envelope changed by change.
func perturbed(t *testing.T, ex pinnedExample, change func(envelope map[string]any)) []byte {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(ex.wire(t), &envelope); err != nil {
		t.Fatal(err)
	}
	change(envelope)
	out, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// E4: a producer may add to its payload. Keys this context does not know, at
// any depth of `data`, and an unknown extension attribute do not break the
// decode: the example is applied exactly as without them.
func TestUnknownAttributesTolerated(t *testing.T) {
	for _, edge := range consumedEdges {
		t.Run(edge.producer, func(t *testing.T) {
			for _, c := range goldenCases(t, edge) {
				t.Run(c.name(), func(t *testing.T) {
					h := newHarness(t)
					msg := perturbed(t, c.ex, func(envelope map[string]any) {
						envelope["data"] = withUnknownKeys(envelope["data"])
						envelope[probeExtension] = "1"
					})
					if err := edge.consumer(h).HandleMessage(context.Background(), msg); err != nil {
						t.Fatalf("unknown keys broke the consumer: %v", err)
					}
					goldenOf(t, c.typ).verify(t, h, c.ex)
				})
			}
		})
	}
}

// A dataschema version this context has never seen. The consumers dispatch on
// the full `type` and do not read `dataschema`, so the event is handled like
// any other of its type: it never fails, never blocks the partition and is
// never dead-lettered for its version. Whether such an event should instead be
// skipped or dead-lettered is NOT decided anywhere in this context (ADR 0003
// and ADR 0004 are silent on it), so this test asserts only what holds under
// either decision and leaves the rest to the owner (see the contract-testing
// page, "Open points").
func TestAnUnknownDataschemaVersionNeverFailsTheConsumer(t *testing.T) {
	for _, edge := range consumedEdges {
		t.Run(edge.producer, func(t *testing.T) {
			for _, c := range goldenCases(t, edge) {
				t.Run(c.name(), func(t *testing.T) {
					msg := perturbed(t, c.ex, func(envelope map[string]any) {
						schema := asString(envelope["dataschema"])
						envelope["dataschema"] = schema[:strings.LastIndex(schema, ":")+1] + "v999"
					})
					if err := edge.consumer(newHarness(t)).HandleMessage(context.Background(), msg); err != nil {
						t.Fatalf("a dataschema version this context does not know failed the consumer: %v", err)
					}
				})
			}
		})
	}
}
