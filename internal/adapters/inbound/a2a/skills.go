package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// tracerName is the OTel instrumentation scope for A2A skill spans.
const tracerName = "github.com/IQVO/customer-returns/internal/adapters/inbound/a2a"

// skillGetReturnAuthorization is the ONE skill id advertised on the Agent Card
// (see Card). It is goal-shaped, not a verb mirror of a REST route.
const skillGetReturnAuthorization = "get_return_authorization"

// contentTypeJSON is the only content type the skill consumes; it is the
// input mode of every skill.
const contentTypeJSON = "application/json"

// skillRequest is the wire contract of every skill call: the first message
// part carries the skill id and its arguments. Strict on purpose: an agent
// client is a program, not a browser.
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// getReturnAuthorizationArgs is the skill's argument shape.
type getReturnAuthorizationArgs struct {
	AuthorizationID string `json:"authorizationId"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (ADR 0009).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: &dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillGetReturnAuthorization:
		return deps.getReturnAuthorization(ctx, req.Arguments)
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// getReturnAuthorization is the read skill, through the same
// GetReturnAuthorization use case the REST and MCP adapters use.
func (d Deps) getReturnAuthorization(ctx context.Context, rawArgs json.RawMessage) (skillDTO, *problem) {
	var args getReturnAuthorizationArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return skillDTO{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	id := strings.TrimSpace(args.AuthorizationID)
	if id == "" {
		return skillDTO{}, problemInvalidArgument("authorizationId is required")
	}
	v, err := d.Reads.GetReturnAuthorization(ctx, returnauthorization.ID(id))
	if err != nil {
		return skillDTO{}, problemFromUseCase(err, "return authorization", "authorizationId", id)
	}
	dto := toAuthorizationDTO(v)
	summary := summarizeAuthorization(dto)
	return skillDTO{Authorization: &dto, summary: summary}, nil
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first part.
// A Data part (or a Text part holding the same JSON object) is accepted;
// anything else is a request-shape problem, answered, not raised, because a
// malformed delegation is a business answer for the calling agent, not a
// transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so the
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// problemFromUseCase maps a use-case error to a problem answer, using the
// SAME classification as the REST adapter for the errors a read can raise: a
// missing aggregate is 404 not-found, a value validation failure is 422
// validation-failed and anything unrecognised is 500 internal-error (the
// 409 classes belong to writes this read-only agent does not offer). The
// detail is adapter-owned text; the 500 never leaks the internal error.
func problemFromUseCase(err error, what, idName, id string) *problem {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return &problem{Type: "not-found", Title: "Not found", Status: 404,
			Detail: fmt.Sprintf("no %s exists with %s %q", what, idName, id)}
	case errors.Is(err, shared.ErrValidation):
		return &problem{Type: "validation-failed", Title: "Validation failed", Status: 422, Detail: err.Error()}
	default:
		slog.Error("a2a skill failed", "error", logReplacer.Replace(err.Error()))
		return &problem{Type: "internal-error", Title: "Internal server error", Status: 500,
			Detail: "the skill failed; see server logs (trace id in the response metadata)"}
	}
}

// toAuthorizationDTO projects the application view (kept here so the SDK file
// stays SDK-only).
func toAuthorizationDTO(v views.ReturnAuthorization) authorizationDTO {
	lines := make([]authorizationLineDTO, 0, len(v.Lines))
	for _, l := range v.Lines {
		lines = append(lines, authorizationLineDTO{
			LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity,
			UnitPriceCents: l.UnitPriceCents, OwnerRef: l.OwnerRef,
		})
	}
	return authorizationDTO{
		AuthorizationID: string(v.AuthorizationID), OrderID: v.OrderID, GroupNo: v.GroupNo,
		Reason: string(v.Reason), Disposition: string(v.Disposition), Method: string(v.Method),
		State: string(v.State), Lines: lines, DestinationNodeID: v.DestinationNodeID,
		OriginNodeID: v.OriginNodeID, PickupWindowStart: instant(v.PickupWindowStart),
		PickupWindowEnd: instant(v.PickupWindowEnd), TrackingNumbers: v.TrackingNumbers,
		AuthorizedAt: v.AuthorizedAt.UTC(), ExpiresAt: v.ExpiresAt.UTC(),
		ClosedAt: instant(v.ClosedAt), Version: v.Version, PricesAbsent: v.PricesAbsent,
	}
}

// summarizeAuthorization is the one-line text form of an authorization.
func summarizeAuthorization(a authorizationDTO) string {
	return fmt.Sprintf("%s: %s return of order %s group %d, %s, %d line(s), expires %s",
		a.AuthorizationID, a.State, a.OrderID, a.GroupNo, a.Disposition, len(a.Lines),
		a.ExpiresAt.Format(time.RFC3339))
}
