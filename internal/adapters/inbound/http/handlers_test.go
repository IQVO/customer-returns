package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	inboundhttp "github.com/IQVO/customer-returns/internal/adapters/inbound/http"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/memory"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

// api is a handler-level harness: the real router over the real use cases on
// the in-memory doubles (no Idempotency middleware, always-ready readiness --
// those have their own tests).
type api struct {
	t     *testing.T
	store *memory.Store
	clock *memory.Clock
	srv   *httptest.Server
	mu    sync.Mutex
	last  *http.Response
	body  string
}

var start = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func newAPI(t *testing.T) *api {
	t.Helper()
	return newAPIWithLogger(t, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
}

func newAPIWithLogger(t *testing.T, logger *slog.Logger) *api {
	t.Helper()
	store := memory.NewStore()
	clock := &memory.Clock{}
	clock.Set(start)
	deps := usecases.Deps{
		UoW:            memory.NewUnitOfWork(store),
		Authorizations: memory.NewAuthorizationRepository(store),
		Groups:         memory.NewGroupViewRepository(store),
		Network:        memory.NewNetworkViewRepository(store),
		Outcomes:       memory.NewOutcomeFacts(store),
		Labels:         memory.NewLabelIssuer(store),
		Outbox:         memory.NewOutbox(store),
		Inbox:          memory.NewInbox(store),
		Clock:          clock, IDs: memory.NewSequentialIDs(),
	}
	svc := usecases.New(deps, usecases.Config{Logger: logger})
	router := inboundhttp.NewRouter(&inboundhttp.Server{Service: svc, Metrics: memory.NewMetrics()}, logger, "")
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &api{t: t, store: store, clock: clock, srv: srv}
}

func (e *api) do(method, path, body string) {
	e.t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, e.srv.URL+path, reader)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := &bytes.Buffer{}
	_, _ = buf.ReadFrom(resp.Body)
	e.mu.Lock()
	e.last, e.body = resp, buf.String()
	e.mu.Unlock()
}

func (e *api) status() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.last == nil {
		e.t.Fatal("no request made yet")
	}
	return e.last.StatusCode
}

func (e *api) slug() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var p struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(e.body), &p)
	return p.Type
}

func (e *api) field(path string) (any, bool) {
	e.mu.Lock()
	body := e.body
	e.mu.Unlock()
	var doc any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, false
	}
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// seedShippedGroup projects the OrderGroupView facts a SHIPPED group needs.
func (e *api) seedShippedGroup(orderID string, groupNo int, status network.GroupStatus) {
	e.t.Helper()
	ctx := context.Background()
	g := network.OrderGroup{
		OrderID: orderID, GroupNo: groupNo, Status: status,
		DestinationPostalPrefix: "013", WitnessNodeID: "node-fc-1",
		Lines:     []network.GroupLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}},
		UpdatedAt: start,
	}
	if err := e.store.GroupSaver().SaveGroup(ctx, g); err != nil {
		e.t.Fatalf("seed group: %v", err)
	}
}

func (e *api) seedNetwork() {
	e.t.Helper()
	ctx := context.Background()
	s := e.store.NetworkSaver()
	_ = s.RegisterNode(ctx, network.Node{NodeID: "node-rc-1", Type: network.TypeReturnsCenter, RegionID: "region-se", LabelPostalPrefix: "07750"})
	_ = s.SetNodeLifecycle(ctx, "node-rc-1", network.NodeActive, time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC))
	_ = s.SavePrefix(ctx, network.PrefixAssignment{PostalPrefix: "013", RegionID: "region-se", AssignmentVersion: 2})
}

const authorizeBody = `{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":400}],"reason":"DEFECTIVE"}`

func TestAuthorizeReturnHappyPath(t *testing.T) {
	e := newAPI(t)
	e.seedShippedGroup("ord-1", 1, network.GroupShipped)
	e.seedNetwork()
	e.do(http.MethodPost, "/return-authorizations", `{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":9900}],"reason":"DEFECTIVE"}`)
	if e.status() != http.StatusCreated {
		t.Fatalf("status = %d (%s)", e.status(), e.body)
	}
	if got, _ := e.field("disposition"); got != "NEAREST_RETURNS_CENTER" {
		t.Fatalf("disposition = %v, want NEAREST_RETURNS_CENTER (9900 is over the 1500 threshold)", got)
	}
	if _, ok := e.field("authorizationId"); !ok {
		t.Fatalf("no authorizationId: %s", e.body)
	}
	if _, ok := e.field("pricesAbsent"); !ok || e.fieldMustBool("pricesAbsent") {
		t.Fatalf("pricesAbsent must be present and false: %s", e.body)
	}
}

func (e *api) fieldMustBool(path string) bool {
	v, ok := e.field(path)
	if !ok {
		e.t.Fatalf("field %s missing", path)
	}
	b, _ := v.(bool)
	return b
}

func TestAuthorizeKeepUnderThreshold(t *testing.T) {
	e := newAPI(t)
	e.seedShippedGroup("ord-1", 1, network.GroupShipped)
	e.seedNetwork()
	e.do(http.MethodPost, "/return-authorizations", authorizeBody)
	if e.status() != http.StatusCreated {
		t.Fatalf("status = %d (%s)", e.status(), e.body)
	}
	if got, _ := e.field("disposition"); got != "KEEP" {
		t.Fatalf("disposition = %v, want KEEP (2 x 400 = 800 <= 1500)", got)
	}
	if got, _ := e.field("state"); got != "CLOSED_KEEP" {
		t.Fatalf("state = %v, want CLOSED_KEEP", got)
	}
	if _, ok := e.field("destinationNodeId"); ok {
		t.Fatalf("KEEP carries no destination: %s", e.body)
	}
}

func TestAuthorizeGroupNotShipped(t *testing.T) {
	e := newAPI(t)
	e.seedShippedGroup("ord-1", 1, network.GroupPromised)
	e.do(http.MethodPost, "/return-authorizations", authorizeBody)
	if e.status() != http.StatusUnprocessableEntity || e.slug() != "group-not-shipped" {
		t.Fatalf("status/slug = %d/%s", e.status(), e.slug())
	}
}

func TestAuthorizeGroupUnknown(t *testing.T) {
	e := newAPI(t)
	e.do(http.MethodPost, "/return-authorizations", authorizeBody)
	if e.status() != http.StatusUnprocessableEntity || e.slug() != "group-not-shipped" {
		t.Fatalf("status/slug = %d/%s (the view may lag; honest retry answer)", e.status(), e.slug())
	}
}

func TestAuthorizeNoDestination(t *testing.T) {
	e := newAPI(t)
	e.seedShippedGroup("ord-1", 1, network.GroupShipped)
	body := `{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":9900}],"reason":"OTHER"}`
	e.do(http.MethodPost, "/return-authorizations", body)
	if e.status() != http.StatusUnprocessableEntity || e.slug() != "no-return-destination" {
		t.Fatalf("status/slug = %d/%s", e.status(), e.slug())
	}
}

func TestAuthorizeUnknownLine(t *testing.T) {
	e := newAPI(t)
	e.seedShippedGroup("ord-1", 1, network.GroupShipped)
	e.seedNetwork()
	e.do(http.MethodPost, "/return-authorizations", `{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":7}],"reason":"OTHER"}`)
	if e.status() != http.StatusUnprocessableEntity || e.slug() != "validation-failed" {
		t.Fatalf("status/slug = %d/%s", e.status(), e.slug())
	}
}

func TestAuthorizeMalformedBody(t *testing.T) {
	e := newAPI(t)
	e.do(http.MethodPost, "/return-authorizations", `{"orderId":"ord-1","groupNo":1,"lines":[],"reason":"DEFECTIVE"}`)
	if e.status() != http.StatusUnprocessableEntity || e.slug() != "validation-failed" {
		t.Fatalf("status/slug = %d/%s", e.status(), e.slug())
	}
	e.do(http.MethodPost, "/return-authorizations", `{`)
	if e.status() != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", e.status())
	}
}

func TestGetAndListAuthorizations(t *testing.T) {
	e := newAPI(t)
	e.seedShippedGroup("ord-1", 1, network.GroupShipped)
	e.seedNetwork()
	e.do(http.MethodPost, "/return-authorizations", authorizeBody)
	id, _ := e.field("authorizationId")
	e.do(http.MethodGet, "/return-authorizations/"+id.(string), "")
	if e.status() != http.StatusOK {
		t.Fatalf("get status = %d", e.status())
	}
	e.do(http.MethodGet, "/return-authorizations?orderId=ord-1", "")
	if e.status() != http.StatusOK {
		t.Fatalf("list status = %d", e.status())
	}
	items, ok := e.field("items")
	if !ok {
		t.Fatalf("no items: %s", e.body)
	}
	if len(items.([]any)) != 1 {
		t.Fatalf("items = %v", items)
	}
	e.do(http.MethodGet, "/return-authorizations", "")
	if e.status() != http.StatusUnprocessableEntity {
		t.Fatalf("list without orderId = %d", e.status())
	}
	e.do(http.MethodGet, "/return-authorizations?orderId=ord-1&cursor=broken", "")
	if e.status() != http.StatusUnprocessableEntity {
		t.Fatalf("list with bad cursor = %d", e.status())
	}
}

func TestGetUnknownAuthorization(t *testing.T) {
	e := newAPI(t)
	e.do(http.MethodGet, "/return-authorizations/ret-none", "")
	if e.status() != http.StatusNotFound || e.slug() != "not-found" {
		t.Fatalf("status/slug = %d/%s", e.status(), e.slug())
	}
}

func TestCancelReturn(t *testing.T) {
	e := newAPI(t)
	e.seedShippedGroup("ord-1", 1, network.GroupDelivered)
	e.seedNetwork()
	e.do(http.MethodPost, "/return-authorizations", `{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":9900}],"reason":"WRONG_ITEM"}`)
	id, _ := e.field("authorizationId")
	e.do(http.MethodPost, "/return-authorizations/"+id.(string)+"/cancellations", "")
	if e.status() != http.StatusOK {
		t.Fatalf("cancel status = %d (%s)", e.status(), e.body)
	}
	if got, _ := e.field("state"); got != "CANCELLED" {
		t.Fatalf("state = %v", got)
	}
	e.do(http.MethodPost, "/return-authorizations/"+id.(string)+"/cancellations", "")
	if e.status() != http.StatusConflict {
		t.Fatalf("second cancel = %d", e.status())
	}
}

func TestIssueLabelUnavailableUntilConfigured(t *testing.T) {
	e := newAPI(t)
	e.seedShippedGroup("ord-1", 1, network.GroupDelivered)
	e.seedNetwork()
	e.do(http.MethodPost, "/return-authorizations", `{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":9900}],"reason":"WRONG_ITEM"}`)
	id, _ := e.field("authorizationId")
	// The label port answers label-unavailable by default (no route live yet).
	e.do(http.MethodPost, "/return-authorizations/"+id.(string)+"/labels", `{"packageNo":1}`)
	if e.status() != http.StatusUnprocessableEntity || e.slug() != "label-unavailable" {
		t.Fatalf("status/slug = %d/%s", e.status(), e.slug())
	}
}

func TestIssueLabelLinksTrackingNumber(t *testing.T) {
	e := newAPI(t)
	e.seedShippedGroup("ord-1", 1, network.GroupDelivered)
	e.seedNetwork()
	// The route is asked for a label to the returns centre (07750), not to
	// the order group's own prefix (013, the shopper's).
	e.store.LabelResponse(func(in requests.IssueReturnLabel) (requests.ReturnLabel, error) {
		if in.DestinationPostalPrefix != "07750" {
			return requests.ReturnLabel{}, fmt.Errorf("destination prefix = %q, want the returns centre's 07750", in.DestinationPostalPrefix)
		}
		return requests.ReturnLabel{TrackingNumber: "trk-rev-1", Carrier: "RN-GROUND"}, nil
	})
	e.do(http.MethodPost, "/return-authorizations", `{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":9900}],"reason":"WRONG_ITEM"}`)
	id, _ := e.field("authorizationId")
	e.do(http.MethodPost, "/return-authorizations/"+id.(string)+"/labels", `{"packageNo":1}`)
	if e.status() != http.StatusOK {
		t.Fatalf("label status = %d (%s)", e.status(), e.body)
	}
	nums, _ := e.field("trackingNumbers")
	if len(nums.([]any)) != 1 || nums.([]any)[0] != "trk-rev-1" {
		t.Fatalf("trackingNumbers = %v", nums)
	}
	e.do(http.MethodPost, "/return-authorizations/"+id.(string)+"/labels", `{"packageNo":9}`)
	if e.status() != http.StatusUnprocessableEntity {
		t.Fatalf("package 9 = %d", e.status())
	}
}
