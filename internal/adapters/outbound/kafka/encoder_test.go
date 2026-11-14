package kafka

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
)

var at = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func price(c int64) *int64 { return &c }

func authorizedEvent() returnauthorization.Authorized {
	return returnauthorization.Authorized{
		Header:          shared.Header{Version: 1, At: at},
		AuthorizationID: "ret-0001", OrderID: "ord-51a0c9", GroupNo: 2,
		Reason: ReasonDefective, Disposition: NearestReturnsCenter, Method: MethodDropOff,
		Lines: []returnauthorization.EventLine{
			{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2, OwnerRef: "sel-42"},
			{LineNo: 2, CatalogItemID: "sku-200", Quantity: 1},
		},
		DestinationNodeID: "node-rc-1", OriginNodeID: "node-fc-1",
		ExpiresAt: at.Add(returnauthorization.ReturnAuthorizationTTL), AuthorizedAt: at,
	}
}

// re-exports of the domain enums keep the golden bodies below readable.
const (
	ReasonDefective      = returnauthorization.ReasonDefective
	NearestReturnsCenter = returnauthorization.NearestReturnsCenter
	MethodDropOff        = returnauthorization.MethodDropOff
	MethodPickup         = returnauthorization.MethodPickup
)

func authorizedKeepEvent() returnauthorization.Authorized {
	e := authorizedEvent()
	e.Disposition = returnauthorization.KeepDisposition
	e.DestinationNodeID = ""
	e.OriginNodeID = ""
	return e
}

func authorizedPickupEvent() returnauthorization.Authorized {
	e := authorizedEvent()
	e.Method = returnauthorization.MethodPickup
	e.PickupStart = at.Add(24 * time.Hour)
	e.PickupEnd = at.Add(48 * time.Hour)
	return e
}

func labelEvent() returnauthorization.LabelIssued {
	return returnauthorization.LabelIssued{
		Header: shared.Header{Version: 2, At: at}, AuthorizationID: "ret-0001",
		TrackingNumber: "trk-rev-9", DestinationNodeID: "node-rc-1", IssuedAt: at,
	}
}

func completedEvent() returnauthorization.Completed {
	return returnauthorization.Completed{
		Header: shared.Header{Version: 3, At: at}, AuthorizationID: "ret-0001",
		Outcome: "RECEIVED", NodeID: "node-rc-1",
		Lines: []returnauthorization.CompletedLine{
			{CatalogItemID: "sku-100", OwnerRef: "sel-42", QtyRestockable: 1, QtyUnsellable: 1},
		},
		ReceivedAt: at,
	}
}

func cancelledEvent() returnauthorization.Cancelled {
	return returnauthorization.Cancelled{
		Header: shared.Header{Version: 2, At: at}, AuthorizationID: "ret-0001",
		Reason: returnauthorization.CancelShopperRequested, CancelledAt: at,
	}
}

func TestEncodeAllFourEvents(t *testing.T) {
	events := []shared.Event{
		authorizedEvent(), authorizedKeepEvent(), authorizedPickupEvent(),
		labelEvent(), completedEvent(), cancelledEvent(),
	}
	enc := NewEncoder()
	enc.NewID = func() string { return "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40" }
	msgs, err := enc.Encode(events...)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(msgs) != len(events) {
		t.Fatalf("messages = %d, want %d", len(msgs), len(events))
	}
	for i, m := range msgs {
		if m.Topic != TopicEvents {
			t.Fatalf("msg %d topic = %s", i, m.Topic)
		}
		if string(m.Key) != "ret-0001" {
			t.Fatalf("msg %d key = %s", i, m.Key)
		}
		if m.EventType != cloudevents.Type("returnauthorization", events[i].EventName()) {
			t.Fatalf("msg %d type = %s", i, m.EventType)
		}
		var env struct {
			Type       string          `json:"type"`
			Source     string          `json:"source"`
			Subject    string          `json:"subject"`
			DataSchema string          `json:"dataschema"`
			Data       json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(m.Value, &env); err != nil {
			t.Fatalf("msg %d is not a CloudEvent: %v", i, err)
		}
		if env.Source != "/retailnetwork/customer-returns" || env.Subject != "ret-0001" {
			t.Fatalf("msg %d envelope = %+v", i, env)
		}
		wantType := "com.retailnetwork.commerce.customer-returns.returnauthorization." + events[i].EventName()
		if env.Type != wantType {
			t.Fatalf("msg %d type = %s, want %s", i, env.Type, wantType)
		}
	}
}

// goldenFile renders the wire payload of one event occurrence.
func goldenFile(t *testing.T, ev shared.Event) []byte {
	t.Helper()
	data, err := payloadFor(ev)
	if err != nil {
		t.Fatalf("payloadFor: %v", err)
	}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func TestEventGoldens(t *testing.T) {
	cases := []struct {
		name string
		ev   shared.Event
	}{
		{"return_authorized", authorizedEvent()},
		{"return_authorized_keep", authorizedKeepEvent()},
		{"return_authorized_pickup", authorizedPickupEvent()},
		{"return_label_issued", labelEvent()},
		{"return_completed", completedEvent()},
		{"return_cancelled", cancelledEvent()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join("testdata", tc.name+".json")
			got := goldenFile(t, tc.ev)
			if os.Getenv("UPDATE_GOLDENS") == "1" {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run UPDATE_GOLDENS=1 go test ./... once): %v", err)
			}
			if string(got) != string(want) {
				t.Fatalf("golden %s drifted:\ngot:  %s\nwant: %s", path, got, want)
			}
		})
	}
}

func TestEncodeUnknownEventFails(t *testing.T) {
	enc := NewEncoder()
	if _, err := enc.Encode(unknownEvent{}); err == nil {
		t.Fatal("expected an error for an unregistered event")
	}
}

type unknownEvent struct{ shared.Header }

func (unknownEvent) EventName() string { return "Unknown" }
func (unknownEvent) Entity() string    { return "returnauthorization" }
func (unknownEvent) SubjectID() string { return "ret-0001" }

// Keep payload lines honest: prices never appear on the wire.
func TestAuthorizedCarriesNoPrices(t *testing.T) {
	data, err := payloadFor(authorizedEvent())
	if err != nil {
		t.Fatalf("payloadFor: %v", err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if containsPrice(raw) {
		t.Fatalf("ReturnAuthorized must not carry prices: %s", raw)
	}
}

func containsPrice(b []byte) bool {
	return stringContains(string(b), "Price")
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
