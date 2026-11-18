package kafka_test

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// The four pins of this context are all AsyncAPI 3.0 today. A producer that
// rewrites its spec in another of the estate's shapes must not turn the
// contract tests into "no channel for topic": the parser is held to all four
// here, on synthetic documents (no payload of any context).

const shapeTopic = "retailnetwork.elsewhere.events"

var asyncAPIShapes = []struct {
	name, doc string
}{
	{"2.6, the producer's own topic under subscribe", `
asyncapi: 2.6.0
channels:
  retailnetwork.elsewhere.events:
    subscribe:
      message:
        oneOf:
          - $ref: '#/components/messages/ThingHappened'
          - $ref: '#/components/messages/ThingEnded'
` + shapeComponents},
	{"2.6, written from the application's side (own topic under publish)", `
asyncapi: 2.6.0
channels:
  retailnetwork.elsewhere.events:
    publish:
      message:
        oneOf:
          - $ref: '#/components/messages/ThingHappened'
          - $ref: '#/components/messages/ThingEnded'
` + shapeComponents},
	{"3.0, a logical channel name and the topic in address", `
asyncapi: 3.0.0
channels:
  elsewhereEvents:
    address: retailnetwork.elsewhere.events
    messages:
      ThingHappened:
        $ref: '#/components/messages/ThingHappened'
operations:
  publishElsewhereEvents:
    action: send
    channel:
      $ref: '#/channels/elsewhereEvents'
    messages:
      - $ref: '#/channels/elsewhereEvents/messages/ThingHappened'
      - $ref: '#/components/messages/ThingEnded'
` + shapeComponents},
	{"2.6, a channel-level messages map and no operation", `
asyncapi: 2.6.0
channels:
  retailnetwork.elsewhere.events:
    messages:
      ThingHappened:
        $ref: '#/components/messages/ThingHappened'
      ThingEnded:
        $ref: '#/components/messages/ThingEnded'
` + shapeComponents},
}

// shapeComponents: one message typed by a schema const (through an allOf and
// a reference), one typed by its example only.
const shapeComponents = `
components:
  messages:
    ThingHappened:
      name: ThingHappened
      payload:
        allOf:
          - $ref: '#/components/schemas/Envelope'
          - type: object
            properties:
              type:
                const: com.retailnetwork.commerce.elsewhere.thing.ThingHappened
    ThingEnded:
      payload:
        $ref: '#/components/schemas/Envelope'
      examples:
        - name: ended
          payload:
            type: com.retailnetwork.commerce.elsewhere.thing.ThingEnded
            data:
              count: 3
  schemas:
    Envelope:
      type: object
      properties:
        source:
          const: /retailnetwork/elsewhere
`

func TestThePinParserReadsTheFourAsyncAPIShapes(t *testing.T) {
	for _, shape := range asyncAPIShapes {
		t.Run(shape.name, func(t *testing.T) {
			var root map[string]any
			if err := yaml.Unmarshal([]byte(shape.doc), &root); err != nil {
				t.Fatal(err)
			}
			spec := pinnedSpec{producer: "elsewhere", root: root}
			var names, types []string
			for _, m := range spec.messagesOn(shapeTopic) {
				names = append(names, m.name)
				types = append(types, spec.types(m)...)
				if got := spec.envelopeConsts(m)["source"]; got != "/retailnetwork/elsewhere" {
					t.Errorf("%s: source const = %q", m.name, got)
				}
			}
			slices.Sort(names)
			slices.Sort(types)
			if !slices.Equal(names, []string{"ThingEnded", "ThingHappened"}) {
				t.Fatalf("messages on the topic = %v", names)
			}
			want := []string{"com.retailnetwork.commerce.elsewhere.thing.ThingEnded", "com.retailnetwork.commerce.elsewhere.thing.ThingHappened"}
			if !slices.Equal(types, want) {
				t.Fatalf("documented types = %v, want %v", types, want)
			}
			if got := spec.messagesOn("retailnetwork.elsewhere.analytics"); len(got) != 0 {
				t.Fatalf("a topic the document does not have resolved to %v", got)
			}
		})
	}
}

// yaml.v3 decodes a whole number as int and JSON as float64: the golden
// helpers read both.
func TestGoldenHelpersReadNumbersOfEitherDecoder(t *testing.T) {
	var root map[string]any
	if err := yaml.Unmarshal([]byte(asyncAPIShapes[0].doc), &root); err != nil {
		t.Fatal(err)
	}
	spec := pinnedSpec{producer: "elsewhere", root: root}
	for _, m := range spec.messagesOn(shapeTopic) {
		for _, ex := range m.examples() {
			if got := number(t, ex.data()["count"]); got != 3 {
				t.Errorf("from YAML: %d", got)
			}
			if got := number(t, exampleData(t, string(ex.wire(t)))["count"]); got != 3 {
				t.Errorf("from JSON: %d", got)
			}
		}
	}
}
