package a2a

import (
	"fmt"
	"time"
)

// Problem `type` values are the bare kebab-case slugs the REST adapter uses
// (internal/adapters/inbound/http/errors.go, apis/openapi.yaml: Problem), so a
// delegating agent sees the identifiers a REST caller would for the outcomes
// both surfaces share. The slugs below belong to the A2A request shape only.

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func problemMalformedRequest(detail string) *problem {
	return &problem{Type: "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: 400}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: "unknown-skill", Title: "Unknown skill", Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill), Status: 400}
}

func problemInvalidArgument(detail string) *problem {
	return &problem{Type: "invalid-argument", Title: "Invalid skill argument", Detail: detail, Status: 400}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string    `json:"skill"`
	Data    *skillDTO `json:"data,omitempty"`
	Problem *problem  `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field, keeping the envelope
// flat and versioned as one contract.
type skillDTO struct {
	Authorization *authorizationDTO `json:"authorization,omitempty"`
	summary       string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// authorizationLineDTO is one authorized line on the wire.
type authorizationLineDTO struct {
	LineNo         int    `json:"lineNo"`
	CatalogItemID  string `json:"catalogItemId"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents *int64 `json:"unitPriceCents,omitempty"`
	OwnerRef       string `json:"ownerRef,omitempty"`
}

// authorizationDTO is the message-boundary representation of one return
// authorization. Optional instants are omitted when they do not apply;
// trackingNumbers is always present.
type authorizationDTO struct {
	AuthorizationID   string                 `json:"authorizationId"`
	OrderID           string                 `json:"orderId"`
	GroupNo           int                    `json:"groupNo"`
	Reason            string                 `json:"reason"`
	Disposition       string                 `json:"disposition"`
	Method            string                 `json:"method"`
	State             string                 `json:"state"`
	Lines             []authorizationLineDTO `json:"lines"`
	DestinationNodeID string                 `json:"destinationNodeId,omitempty"`
	OriginNodeID      string                 `json:"originNodeId,omitempty"`
	PickupWindowStart *time.Time             `json:"pickupWindowStart,omitempty"`
	PickupWindowEnd   *time.Time             `json:"pickupWindowEnd,omitempty"`
	TrackingNumbers   []string               `json:"trackingNumbers"`
	AuthorizedAt      time.Time              `json:"authorizedAt"`
	ExpiresAt         time.Time              `json:"expiresAt"`
	ClosedAt          *time.Time             `json:"closedAt,omitempty"`
	Version           int64                  `json:"version"`
	PricesAbsent      bool                   `json:"pricesAbsent"`
}

// instant formats an optional instant; the zero time is nil (omitted), like
// the REST adapter.
func instant(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}
