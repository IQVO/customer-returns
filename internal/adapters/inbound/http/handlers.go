package http

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// authorizeBody is the body of POST /return-authorizations.
type authorizeBody struct {
	OrderID     string          `json:"orderId"`
	GroupNo     int             `json:"groupNo"`
	Lines       []authorizeLine `json:"lines"`
	Reason      string          `json:"reason"`
	Method      string          `json:"method,omitempty"`
	PickupStart string          `json:"pickupWindowStart,omitempty"`
	PickupEnd   string          `json:"pickupWindowEnd,omitempty"`
}

// authorizeLine picks one order line by lineNo with the caller-declared price.
type authorizeLine struct {
	LineNo         int    `json:"lineNo"`
	UnitPriceCents *int64 `json:"unitPriceCents,omitempty"`
}

// labelBody is the body of POST /return-authorizations/{id}/labels.
type labelBody struct {
	PackageNo int `json:"packageNo"`
}

// handleAuthorizeReturn is POST /return-authorizations.
func (s *Server) handleAuthorizeReturn(w http.ResponseWriter, r *http.Request) {
	var body authorizeBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	req, err := authorizeRequest(body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.Service.AuthorizeReturn(r.Context(), req)
	if err != nil {
		s.rejectMetric(r, err)
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, authorizationDTO(view))
}

func authorizeRequest(body authorizeBody) (requests.AuthorizeReturn, error) {
	if len(body.Lines) == 0 {
		return requests.AuthorizeReturn{}, fmt.Errorf("%w: at least one line is required", errMalformedRequest)
	}
	req := requests.AuthorizeReturn{
		OrderID: body.OrderID, GroupNo: body.GroupNo,
		Reason: returnauthorization.Reason(body.Reason),
		Method: returnauthorization.MethodDropOff,
	}
	if body.Method != "" {
		req.Method = returnauthorization.Method(body.Method)
	}
	var err error
	if req.PickupStart, req.PickupEnd, err = pickupWindow(body); err != nil {
		return requests.AuthorizeReturn{}, err
	}
	req.Lines = make([]requests.AuthorizeLine, 0, len(body.Lines))
	for _, l := range body.Lines {
		req.Lines = append(req.Lines, requests.AuthorizeLine{LineNo: l.LineNo, UnitPriceCents: l.UnitPriceCents})
	}
	return req, nil
}

func pickupWindow(body authorizeBody) (time.Time, time.Time, error) {
	if body.PickupStart == "" && body.PickupEnd == "" {
		return time.Time{}, time.Time{}, nil
	}
	if body.PickupStart == "" || body.PickupEnd == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: pickup window needs both bounds", errMalformedRequest)
	}
	start, err := time.Parse(time.RFC3339, body.PickupStart)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: pickupWindowStart is not RFC 3339", errMalformedRequest)
	}
	end, err := time.Parse(time.RFC3339, body.PickupEnd)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: pickupWindowEnd is not RFC 3339", errMalformedRequest)
	}
	return start, end, nil
}

// handleGetAuthorization is GET /return-authorizations/{authorizationId}.
func (s *Server) handleGetAuthorization(w http.ResponseWriter, r *http.Request) {
	id := returnauthorization.ID(chi.URLParam(r, "authorizationId"))
	view, err := s.Service.GetReturnAuthorization(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, authorizationDTO(view))
}

// handleListAuthorizations is GET /return-authorizations?orderId=&cursor=&limit=.
func (s *Server) handleListAuthorizations(w http.ResponseWriter, r *http.Request) {
	orderID := r.URL.Query().Get("orderId")
	if orderID == "" {
		writeProblem(w, r, problemCatalogue[0].p, "orderId is required")
		return
	}
	afterAt, afterID, err := listCursor(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > usecases.MaxListLimit {
			writeError(w, r, errMalformedRequest)
			return
		}
		limit = n
	}
	page, err := s.Service.ListReturnAuthorizations(r.Context(), requests.ListAuthorizations{
		OrderID: orderID, AfterCreatedAt: afterAt, AfterID: afterID, Limit: limit,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	items := make([]authorizationBody, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, authorizationDTO(item))
	}
	body := listBody{Items: items, NextCursor: page.NextCursor}
	if body.Items == nil {
		body.Items = []authorizationBody{}
	}
	writeJSON(w, http.StatusOK, body)
}

func listCursor(r *http.Request) (time.Time, returnauthorization.ID, error) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return time.Time{}, "", nil
	}
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("%w: cursor is malformed", errMalformedRequest)
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: cursor is malformed", errMalformedRequest)
	}
	return at, returnauthorization.ID(parts[1]), nil
}

type listBody struct {
	Items      []authorizationBody `json:"items"`
	NextCursor string              `json:"nextCursor,omitempty"`
}

// handleCancelReturn is POST /return-authorizations/{authorizationId}/cancellations.
func (s *Server) handleCancelReturn(w http.ResponseWriter, r *http.Request) {
	id := returnauthorization.ID(chi.URLParam(r, "authorizationId"))
	view, err := s.Service.CancelReturn(r.Context(), requests.CancelReturn{AuthorizationID: id})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, authorizationDTO(view))
}

// handleIssueLabel is POST /return-authorizations/{authorizationId}/labels.
func (s *Server) handleIssueLabel(w http.ResponseWriter, r *http.Request) {
	id := returnauthorization.ID(chi.URLParam(r, "authorizationId"))
	var body labelBody
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.Service.IssueLabel(r.Context(), requests.IssueLabel{AuthorizationID: id, PackageNo: body.PackageNo})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, authorizationDTO(view))
}

func (s *Server) rejectMetric(r *http.Request, err error) {
	if s.Metrics == nil {
		return
	}
	p := problemFor(err)
	s.Metrics.ReturnRejected(r.Context(), p.slug)
}

// authorizationDTO renders the view (camelCase; optional fields omitted when
// they do not apply, arrays always present).
type authorizationBody struct {
	AuthorizationID   string              `json:"authorizationId"`
	OrderID           string              `json:"orderId"`
	GroupNo           int                 `json:"groupNo"`
	Reason            string              `json:"reason"`
	Disposition       string              `json:"disposition"`
	Method            string              `json:"method"`
	State             string              `json:"state"`
	Lines             []authorizationLine `json:"lines"`
	DestinationNodeID string              `json:"destinationNodeId,omitempty"`
	OriginNodeID      string              `json:"originNodeId,omitempty"`
	PickupWindowStart string              `json:"pickupWindowStart,omitempty"`
	PickupWindowEnd   string              `json:"pickupWindowEnd,omitempty"`
	TrackingNumbers   []string            `json:"trackingNumbers"`
	AuthorizedAt      string              `json:"authorizedAt"`
	ExpiresAt         string              `json:"expiresAt"`
	ClosedAt          string              `json:"closedAt,omitempty"`
	Version           int64               `json:"version"`
	PricesAbsent      bool                `json:"pricesAbsent"`
}

type authorizationLine struct {
	LineNo         int    `json:"lineNo"`
	CatalogItemID  string `json:"catalogItemId"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents *int64 `json:"unitPriceCents,omitempty"`
	OwnerRef       string `json:"ownerRef,omitempty"`
}

func authorizationDTO(v views.ReturnAuthorization) authorizationBody {
	lines := make([]authorizationLine, 0, len(v.Lines))
	for _, l := range v.Lines {
		lines = append(lines, authorizationLine{
			LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity,
			UnitPriceCents: l.UnitPriceCents, OwnerRef: l.OwnerRef,
		})
	}
	out := authorizationBody{
		AuthorizationID: string(v.AuthorizationID), OrderID: v.OrderID, GroupNo: v.GroupNo,
		Reason: string(v.Reason), Disposition: string(v.Disposition), Method: string(v.Method),
		State: string(v.State), Lines: lines, DestinationNodeID: v.DestinationNodeID,
		OriginNodeID: v.OriginNodeID, TrackingNumbers: v.TrackingNumbers,
		AuthorizedAt: rfc3339(v.AuthorizedAt), ExpiresAt: rfc3339(v.ExpiresAt),
		ClosedAt: rfc3339NonZero(v.ClosedAt), Version: v.Version, PricesAbsent: v.PricesAbsent,
	}
	if !v.PickupWindowStart.IsZero() {
		out.PickupWindowStart = rfc3339(v.PickupWindowStart)
		out.PickupWindowEnd = rfc3339(v.PickupWindowEnd)
	}
	if out.TrackingNumbers == nil {
		out.TrackingNumbers = []string{}
	}
	return out
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func rfc3339NonZero(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return rfc3339(t)
}
