package kafka_test

// The pinned producer specs as the consumer contract tests read them. A pin
// is a byte copy of a producer's apis/asyncapi.yaml on its develop branch,
// registered in contracts/pinned.json and compared with the producer on every
// CI run (scripts/harness/pinned_contract_gate.py). The tests read the pins at
// test time: no payload of another context is pasted into this repository.
//
// The estate's AsyncAPI files come in four shapes, and the parser reads all of
// them: 2.6 with the producer's own topics under `subscribe:`, 2.6 written
// from the application's side (own topics under `publish:`), 3.0 with channels
// keyed by a logical name and the topic in `address`, and 2.6 with a
// channel-level `messages:` map and no operation. A channel is therefore found
// by its map key OR its address, and its messages are the union of the
// channel-level map and the operations' message lists. The direction of an
// operation is never trusted: which topics this context reads comes from its
// own reader configuration, the pin is the catalogue of what is on them.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// pinDir is the repo-root directory of the pins, seen from this package. The
// pins live outside internal/ on purpose: see contracts/pinned.json.
const pinDir = "../../../../contracts/"

// pinnedSpec is one producer's pinned AsyncAPI document.
type pinnedSpec struct {
	producer string
	root     map[string]any
}

var (
	pinnedSpecsMu sync.Mutex
	pinnedSpecs   = map[string]pinnedSpec{}
)

// readPinnedAsyncAPI parses contracts/<producer>.asyncapi.yaml (once per test
// binary; the parsed document is read-only).
func readPinnedAsyncAPI(producer string) (pinnedSpec, error) {
	pinnedSpecsMu.Lock()
	defer pinnedSpecsMu.Unlock()
	if spec, ok := pinnedSpecs[producer]; ok {
		return spec, nil
	}
	raw, err := os.ReadFile(pinDir + producer + ".asyncapi.yaml")
	if err != nil {
		return pinnedSpec{}, fmt.Errorf("the pinned %s spec is missing (contracts/pinned.json): %w", producer, err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return pinnedSpec{}, fmt.Errorf("the pinned %s spec does not parse: %w", producer, err)
	}
	spec := pinnedSpec{producer: producer, root: root}
	pinnedSpecs[producer] = spec
	return spec, nil
}

func loadPinnedAsyncAPI(t *testing.T, producer string) pinnedSpec {
	t.Helper()
	spec, err := readPinnedAsyncAPI(producer)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func lastSegment(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }

// pointer resolves a JSON pointer ("#/components/messages/X") in the document.
func (s pinnedSpec) pointer(ref string) any {
	var cur any = s.root
	unescape := strings.NewReplacer("~1", "/", "~0", "~")
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = asMap(cur)[unescape.Replace(seg)]
	}
	return cur
}

// deref follows a chain of $ref (a 3.0 operation names a channel's message,
// which names a component) and returns the object it ends at.
func (s pinnedSpec) deref(v any) map[string]any {
	m := asMap(v)
	for range 8 {
		ref := asString(m["$ref"])
		if ref == "" {
			return m
		}
		m = asMap(s.pointer(ref))
	}
	return m
}

// channel finds the channel of a topic: by its map key (2.6) or by its
// address (3.0, where the key is a logical name).
func (s pinnedSpec) channel(topic string) (string, map[string]any) {
	channels := asMap(s.root["channels"])
	if ch := s.deref(channels[topic]); ch != nil {
		return topic, ch
	}
	for _, key := range sortedKeys(channels) {
		if ch := s.deref(channels[key]); asString(ch["address"]) == topic {
			return key, ch
		}
	}
	return "", nil
}

// pinnedMessage is one message a pinned spec documents on a topic.
type pinnedMessage struct {
	name string         // the message's `name`, else the key it is listed under
	body map[string]any // the message object, references resolved
}

// oneOf flattens the `message` of a 2.6 operation: one message or a list.
func oneOf(message any) []any {
	m := asMap(message)
	if m == nil {
		return nil
	}
	if list := asList(m["oneOf"]); list != nil {
		return list
	}
	return []any{m}
}

// messageRefs lists every place the spec names a message of the topic: the
// channel-level map, the 2.6 operations of the channel (either perspective)
// and the 3.0 operations that point at the channel.
func (s pinnedSpec) messageRefs(topic string) []any {
	key, ch := s.channel(topic)
	if ch == nil {
		return nil
	}
	var refs []any
	listed := asMap(ch["messages"])
	for _, name := range sortedKeys(listed) {
		refs = append(refs, listed[name])
	}
	for _, op := range []string{"subscribe", "publish"} {
		refs = append(refs, oneOf(asMap(ch[op])["message"])...)
	}
	operations := asMap(s.root["operations"])
	for _, name := range sortedKeys(operations) {
		op := asMap(operations[name])
		ref := asString(asMap(op["channel"])["$ref"])
		if lastSegment(ref) == key || asString(s.deref(op["channel"])["address"]) == topic {
			refs = append(refs, asList(op["messages"])...)
		}
	}
	return refs
}

// messagesOn returns the messages the spec documents on a topic, each once.
func (s pinnedSpec) messagesOn(topic string) []pinnedMessage {
	seen := map[string]bool{}
	var out []pinnedMessage
	for _, ref := range s.messageRefs(topic) {
		body := s.deref(ref)
		if body == nil {
			continue
		}
		name := asString(body["name"])
		if name == "" {
			name = lastSegment(asString(asMap(ref)["$ref"]))
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, pinnedMessage{name: name, body: body})
	}
	return out
}

// envelopeConsts are the `const` values the message's payload schema fixes on
// the CloudEvents attributes, allOf branches and references followed.
func (s pinnedSpec) envelopeConsts(m pinnedMessage) map[string]string {
	consts := map[string]string{}
	var walk func(schema map[string]any, depth int)
	walk = func(schema map[string]any, depth int) {
		if schema == nil || depth > 6 {
			return
		}
		properties := asMap(schema["properties"])
		for _, name := range sortedKeys(properties) {
			if c := asString(s.deref(properties[name])["const"]); c != "" {
				consts[name] = c
			}
		}
		for _, branch := range asList(schema["allOf"]) {
			walk(s.deref(branch), depth+1)
		}
	}
	walk(s.deref(m.body["payload"]), 0)
	return consts
}

// types returns every CloudEvents `type` the message is documented with: the
// const of its payload schema and the type of each of its own examples (three
// of the four pinned specs declare no const and name the type in the examples
// only).
func (s pinnedSpec) types(m pinnedMessage) []string {
	set := map[string]bool{}
	if c := s.envelopeConsts(m)["type"]; c != "" {
		set[c] = true
	}
	for _, ex := range m.examples() {
		if typ := ex.attr("type"); typ != "" {
			set[typ] = true
		}
	}
	return sortedKeys(set)
}

// contentType is the message's content type, the document's default when the
// message declares none.
func (s pinnedSpec) contentType(m pinnedMessage) string {
	if ct := asString(m.body["contentType"]); ct != "" {
		return ct
	}
	return asString(s.root["defaultContentType"])
}

// pinnedExample is one example of a pinned message: a whole CloudEvent, as
// the producer wrote it.
type pinnedExample struct {
	message string
	name    string
	event   map[string]any
}

func (m pinnedMessage) examples() []pinnedExample {
	var out []pinnedExample
	for i, e := range asList(m.body["examples"]) {
		event := asMap(asMap(e)["payload"])
		if event == nil {
			continue
		}
		name := asString(asMap(e)["name"])
		if name == "" {
			name = fmt.Sprintf("example-%d", i)
		}
		out = append(out, pinnedExample{message: m.name, name: name, event: event})
	}
	return out
}

// attr is one CloudEvents attribute of the example ("" when absent).
func (e pinnedExample) attr(name string) string { return asString(e.event[name]) }

// data is the example's `data`.
func (e pinnedExample) data() map[string]any { return asMap(e.event["data"]) }

// wire renders the example as the JSON a consumer receives.
func (e pinnedExample) wire(t *testing.T) []byte {
	t.Helper()
	out, err := json.Marshal(e.event)
	if err != nil {
		t.Fatalf("the pinned example %s/%s does not render as JSON: %v", e.message, e.name, err)
	}
	return out
}

// text reads a string the test relies on. It must be there: an expectation
// read from a field the producer renamed would otherwise be empty on both
// sides and the test would pass on nothing.
func text(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	s := asString(object[key])
	if s == "" {
		t.Fatalf("the pinned example has no %q (the producer renamed or dropped it?): %v", key, object)
	}
	return s
}

// number reads a whole number: yaml.v3 decodes it as int, a JSON round trip
// as float64.
func number(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	t.Fatalf("the pinned example carries %v (%T) where a whole number is expected", v, v)
	return 0
}

// objects reads a non-empty array of objects.
func objects(t *testing.T, object map[string]any, key string) []map[string]any {
	t.Helper()
	list := asList(object[key])
	if len(list) == 0 {
		t.Fatalf("the pinned example has no %q: %v", key, object)
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		out = append(out, asMap(item))
	}
	return out
}

// instant reads an RFC 3339 instant.
func instant(t *testing.T, v any) time.Time {
	t.Helper()
	if at, ok := v.(time.Time); ok {
		return at.UTC()
	}
	at, err := time.Parse(time.RFC3339, asString(v))
	if err != nil {
		t.Fatalf("the pinned example carries %v where an RFC 3339 instant is expected: %v", v, err)
	}
	return at.UTC()
}
