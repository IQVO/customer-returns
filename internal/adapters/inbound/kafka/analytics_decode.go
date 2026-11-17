package kafka

import (
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/IQVO/customer-returns/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

// entityAuthorization is the CloudEvents entity segment of this context's one
// aggregate (apis/asyncapi.yaml: ...customer-returns.returnauthorization.<Event>).
const entityAuthorization = "returnauthorization"

// outcomeReceived is the only `outcome` a ReturnCompleted carries
// (apis/asyncapi.yaml). Anything else is not a receipt this projection knows.
const outcomeReceived = "RECEIVED"

// projectedKinds maps the FULL CloudEvents type of each projected event
// (ADR 0012) to its returnsperformance.Kind.
func projectedKinds() map[string]returnsperformance.Kind {
	return map[string]returnsperformance.Kind{
		cloudevents.Type(entityAuthorization, string(returnsperformance.KindAuthorized)): returnsperformance.KindAuthorized,
		cloudevents.Type(entityAuthorization, string(returnsperformance.KindCompleted)):  returnsperformance.KindCompleted,
	}
}

// acknowledgedTypes are this context's other published types: committed past
// without a warning, because no report needs them (ADR 0012).
func acknowledgedTypes() map[string]bool {
	return map[string]bool{
		cloudevents.Type(entityAuthorization, "ReturnLabelIssued"): true,
		cloudevents.Type(entityAuthorization, "ReturnCancelled"):   true,
	}
}

// analyticsData is the union of the payload fields the projection reads,
// hand-mirrored from apis/asyncapi.yaml (never imported from the encoder).
// Unknown fields are ignored; an absent timestamp stays zero.
type analyticsData struct {
	AuthorizationID string    `json:"authorizationId"`
	Reason          string    `json:"reason"`
	Outcome         string    `json:"outcome"`
	AuthorizedAt    time.Time `json:"authorizedAt"`
	ReceivedAt      time.Time `json:"receivedAt"`
}

// toEvent decodes and validates a CloudEvent of a projected kind. Every error
// is deterministic: the same bytes can never pass.
func toEvent(kind returnsperformance.Kind, evt ce.Event) (returnsperformance.Event, error) {
	var d analyticsData
	if err := evt.DataAs(&d); err != nil {
		return returnsperformance.Event{}, fmt.Errorf("decode %s %s data: %w", kind, evt.ID(), err)
	}
	if d.AuthorizationID == "" || d.AuthorizationID != evt.Subject() {
		return returnsperformance.Event{}, fmt.Errorf("%s %s: the aggregate id %q does not match the subject %q", kind, evt.ID(), d.AuthorizationID, evt.Subject())
	}
	if kind == returnsperformance.KindCompleted && d.Outcome != outcomeReceived {
		return returnsperformance.Event{}, fmt.Errorf("%s %s: outcome %q is not %s", kind, evt.ID(), d.Outcome, outcomeReceived)
	}
	e := returnsperformance.Event{
		Kind: kind, EventID: evt.ID(), At: evt.Time().UTC(), AuthorizationID: d.AuthorizationID,
		OccurredAt: d.instant(kind).UTC(), Reason: d.Reason,
	}
	if err := e.Validate(); err != nil {
		return returnsperformance.Event{}, err
	}
	return e, nil
}

// instant is the business instant of a fact: `authorizedAt` of an authorization,
// `receivedAt` of a receipt.
func (d analyticsData) instant(kind returnsperformance.Kind) time.Time {
	if kind == returnsperformance.KindAuthorized {
		return d.AuthorizedAt
	}
	return d.ReceivedAt
}
