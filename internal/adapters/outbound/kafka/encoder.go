// Package kafka holds the outbound Kafka adapters. The outbox relay publishes
// through the Writer built here; the topic is chosen per message, never on
// the Writer, so one Writer serves the events and analytics topics.
package kafka

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/IQVO/customer-returns/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/customer-returns/internal/application/outbox"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// TopicEvents is the integration topic of this context.
const TopicEvents = "retailnetwork.customer-returns.events"

// TopicAnalytics is the analytics-only topic (no analytics event in v1).
const TopicAnalytics = "retailnetwork.customer-returns.analytics"

const schemaVersion = 1

// Encoder implements outbox.Encoder: each domain event becomes one
// structured-mode CloudEvents message on the events topic, keyed by the
// authorizationId.
type Encoder struct {
	// NewID mints the CloudEvents id once per occurrence.
	NewID func() string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

var _ outbox.Encoder = (*Encoder)(nil)

// Encode turns the domain events into outbox messages.
func (e *Encoder) Encode(events ...shared.Event) ([]outbox.Message, error) {
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		data, err := payloadFor(ev)
		if err != nil {
			return nil, err
		}
		msg, err := buildMessage(ev, newID(), data)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func buildMessage(ev shared.Event, id string, data any) (outbox.Message, error) {
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: ev.SubjectID(),
		Time: ev.OccurredAt(), Stream: cloudevents.StreamEvents, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	return outbox.Message{
		EventID: id, Topic: TopicEvents, EventType: cloudevents.Type(ev.Entity(), ev.EventName()),
		Subject: ev.SubjectID(), Key: []byte(ev.SubjectID()),
		DataSchema: cloudevents.DataSchema(cloudevents.StreamEvents, ev.EventName(), schemaVersion),
		Value:      value, Headers: []outbox.Header{{Key: "content-type", Value: cloudevents.MediaType}},
	}, nil
}

func payloadFor(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case returnauthorization.Authorized:
		return authorizedPayload(e), nil
	case returnauthorization.LabelIssued:
		return labelIssuedPayload(e), nil
	case returnauthorization.Completed:
		return completedPayload(e), nil
	case returnauthorization.Cancelled:
		return cancelledPayload(e), nil
	default:
		return nil, fmt.Errorf("encode: unknown event %T", ev)
	}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

type wireEventLine struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
	OwnerRef      string `json:"ownerRef,omitempty"`
}

type wireAuthorized struct {
	AuthorizationID   string          `json:"authorizationId"`
	OrderID           string          `json:"orderId"`
	GroupNo           int             `json:"groupNo"`
	Reason            string          `json:"reason"`
	Disposition       string          `json:"disposition"`
	Method            string          `json:"method"`
	Lines             []wireEventLine `json:"lines"`
	DestinationNodeID string          `json:"destinationNodeId,omitempty"`
	OriginNodeID      string          `json:"originNodeId,omitempty"`
	PickupWindowStart string          `json:"pickupWindowStart,omitempty"`
	PickupWindowEnd   string          `json:"pickupWindowEnd,omitempty"`
	ExpiresAt         string          `json:"expiresAt"`
	AuthorizedAt      string          `json:"authorizedAt"`
}

func authorizedPayload(e returnauthorization.Authorized) wireAuthorized {
	lines := make([]wireEventLine, 0, len(e.Lines))
	for _, l := range e.Lines {
		lines = append(lines, wireEventLine{
			LineNo: l.LineNo, CatalogItemID: l.CatalogItemID,
			Quantity: l.Quantity, OwnerRef: l.OwnerRef,
		})
	}
	return wireAuthorized{
		AuthorizationID: string(e.AuthorizationID), OrderID: e.OrderID, GroupNo: e.GroupNo,
		Reason: string(e.Reason), Disposition: string(e.Disposition), Method: string(e.Method),
		Lines: lines, DestinationNodeID: e.DestinationNodeID, OriginNodeID: e.OriginNodeID,
		PickupWindowStart: windowField(e.PickupStart), PickupWindowEnd: windowField(e.PickupEnd),
		ExpiresAt: rfc3339(e.ExpiresAt), AuthorizedAt: rfc3339(e.AuthorizedAt),
	}
}

func windowField(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return rfc3339(t)
}

type wireLabelIssued struct {
	AuthorizationID   string `json:"authorizationId"`
	TrackingNumber    string `json:"trackingNumber"`
	DestinationNodeID string `json:"destinationNodeId"`
	IssuedAt          string `json:"issuedAt"`
}

func labelIssuedPayload(e returnauthorization.LabelIssued) wireLabelIssued {
	return wireLabelIssued{
		AuthorizationID: string(e.AuthorizationID), TrackingNumber: e.TrackingNumber,
		DestinationNodeID: e.DestinationNodeID, IssuedAt: rfc3339(e.IssuedAt),
	}
}

type wireCompletedLine struct {
	CatalogItemID  string `json:"catalogItemId"`
	OwnerRef       string `json:"ownerRef,omitempty"`
	QtyRestockable int    `json:"qtyRestockable"`
	QtyUnsellable  int    `json:"qtyUnsellable"`
}

type wireCompleted struct {
	AuthorizationID string              `json:"authorizationId"`
	Outcome         string              `json:"outcome"`
	NodeID          string              `json:"nodeId"`
	Lines           []wireCompletedLine `json:"lines"`
	ReceivedAt      string              `json:"receivedAt"`
}

func completedPayload(e returnauthorization.Completed) wireCompleted {
	lines := make([]wireCompletedLine, 0, len(e.Lines))
	for _, l := range e.Lines {
		lines = append(lines, wireCompletedLine{
			CatalogItemID: l.CatalogItemID, OwnerRef: l.OwnerRef,
			QtyRestockable: l.QtyRestockable, QtyUnsellable: l.QtyUnsellable,
		})
	}
	return wireCompleted{
		AuthorizationID: string(e.AuthorizationID), Outcome: e.Outcome, NodeID: e.NodeID,
		Lines: lines, ReceivedAt: rfc3339(e.ReceivedAt),
	}
}

type wireCancelled struct {
	AuthorizationID string `json:"authorizationId"`
	Reason          string `json:"reason"`
	CancelledAt     string `json:"cancelledAt"`
}

func cancelledPayload(e returnauthorization.Cancelled) wireCancelled {
	return wireCancelled{
		AuthorizationID: string(e.AuthorizationID), Reason: string(e.Reason),
		CancelledAt: rfc3339(e.CancelledAt),
	}
}

// wireJSON is the test hook the golden tests use to render a payload.
func wireJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
