package usecases_test

// The reverse-label call names the return's destination by its NODE ID and
// sends no postal prefix (ADR 0005, addendum 2026-11-18): this context holds
// no address data of a node, the label owner derives the prefix from its own
// node view. These tests drive IssueLabel through the REAL label client
// against a stub of the provider's route, so they read what leaves this
// context on the wire.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/labels"
	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// groupPrefix is the destination prefix of the order group every fixture
// places: the shopper's. It must never leave this context on the label call.
const groupPrefix = "03"

// labelCall is one request the stubbed route received.
type labelCall struct {
	key  string // the Idempotency-Key header
	raw  string
	body map[string]any
}

// labelRoute stubs the provider's POST /return-labels. The first `refusals`
// calls answer 409 destination-prefix-unavailable (the provider does not know
// the node yet, and does not keep that refusal under the Idempotency-Key);
// every later call answers 201 with a ReturnLabel.
type labelRoute struct {
	srv      *httptest.Server
	refusals int
	calls    []labelCall
}

func newLabelRoute(t *testing.T, refusals int) *labelRoute {
	t.Helper()
	r := &labelRoute{refusals: refusals}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		call := labelCall{key: req.Header.Get("Idempotency-Key"), raw: string(raw)}
		if err := json.Unmarshal(raw, &call.body); err != nil {
			t.Errorf("the label request is not JSON: %v", err)
		}
		r.calls = append(r.calls, call)
		if len(r.calls) <= r.refusals {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"type":"destination-prefix-unavailable","title":"destination-prefix-unavailable","status":409}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"labelRef":"lbl-1","trackingNumber":"RN-RET-1-1","carrier":"RN-GROUND","packageNo":1,` +
			`"orderId":"ord-1","tier":"STANDARD","status":"ISSUED","issuedAt":"2026-11-18T12:00:00Z",` +
			`"purpose":"RETURN","returnAuthorizationId":"ret-1"}`))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// authorizedToTheReturnsCentre places ord-1 (group prefix groupPrefix) in a
// region with a returns centre and authorizes a return to it, over the real
// label client pointed at route.
func authorizedToTheReturnsCentre(t *testing.T, route *labelRoute) (*fixture, views.ReturnAuthorization) {
	t.Helper()
	f := newFixtureWithLabels(t, labels.NewClient(route.srv.URL, nil))
	f.placeOrder(t, "ord-1")
	v := f.authorize(t, "ord-1", 1, 2000)
	if v.Disposition != returnauthorization.NearestReturnsCenter || v.DestinationNodeID != "node-rc-1" {
		t.Fatalf("setup: disposition/destination = %s/%s, want NEAREST_RETURNS_CENTER/node-rc-1", v.Disposition, v.DestinationNodeID)
	}
	return f, v
}

func TestIssueLabelNamesTheDestinationByItsNodeIDAndSendsNoPrefix(t *testing.T) {
	route := newLabelRoute(t, 0)
	f, v := authorizedToTheReturnsCentre(t, route)

	got, err := f.svc.IssueLabel(context.Background(), requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1})
	if err != nil {
		t.Fatalf("IssueLabel: %v", err)
	}
	if len(got.TrackingNumbers) != 1 || got.TrackingNumbers[0] != "RN-RET-1-1" {
		t.Fatalf("trackingNumbers = %v", got.TrackingNumbers)
	}
	if len(route.calls) != 1 {
		t.Fatalf("label calls = %d, want 1", len(route.calls))
	}
	sent := route.calls[0]
	want := map[string]any{
		"returnAuthorizationId": string(v.AuthorizationID), "orderId": "ord-1", "packageNo": float64(1),
		"destinationNodeId": v.DestinationNodeID,
	}
	if len(sent.body) != len(want) {
		t.Fatalf("the label request = %s, want exactly the properties of %v", sent.raw, want)
	}
	for k, w := range want {
		if sent.body[k] != w {
			t.Errorf("the label request's %s = %v, want %v (%s)", k, sent.body[k], w, sent.raw)
		}
	}
	// No prefix leaves this context: not under the deprecated property, and
	// not the group's own value under any other name.
	if _, has := sent.body["destinationPostalPrefix"]; has {
		t.Errorf("the label request carries a destination prefix: %s", sent.raw)
	}
	if strings.Contains(sent.raw, `"`+groupPrefix+`"`) {
		t.Errorf("the order group's prefix %q left this context on the label call: %s", groupPrefix, sent.raw)
	}
}

// The provider refuses a node it holds no usable prefix for with 409
// destination-prefix-unavailable and does not keep that refusal under the
// Idempotency-Key. Here it is the tolerant label-unavailable: nothing linked,
// nothing published. The same request, with the same key, is served once the
// provider knows the node.
func TestIssueLabelIsUnavailableWhileTheProviderDoesNotKnowTheNodeAndServedAfter(t *testing.T) {
	route := newLabelRoute(t, 1)
	f, v := authorizedToTheReturnsCentre(t, route)
	ctx := context.Background()
	eventsBefore := len(f.store.Events())

	_, err := f.svc.IssueLabel(ctx, requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1})
	if !errors.Is(err, ports.ErrLabelUnavailable) {
		t.Fatalf("IssueLabel while the provider refuses = %v, want ErrLabelUnavailable", err)
	}
	if n := len(f.store.Events()); n != eventsBefore {
		t.Fatalf("events appended on the refusal = %d, want none", n-eventsBefore)
	}
	after, err := f.svc.GetReturnAuthorization(ctx, v.AuthorizationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.State != v.State || len(after.TrackingNumbers) != 0 {
		t.Fatalf("the refusal changed the authorization: state %v, trackingNumbers %v", after.State, after.TrackingNumbers)
	}
	if len(route.calls) != 1 {
		t.Fatalf("label calls = %d, want exactly 1 (no retry storm)", len(route.calls))
	}

	// The shopper retries later: the provider knows the node now.
	got, err := f.svc.IssueLabel(ctx, requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1})
	if err != nil {
		t.Fatalf("IssueLabel once the provider knows the node: %v", err)
	}
	if len(got.TrackingNumbers) != 1 {
		t.Fatalf("trackingNumbers = %v, want the one label", got.TrackingNumbers)
	}
	wantKey := string(v.AuthorizationID) + ":1"
	if len(route.calls) != 2 || route.calls[0].key != wantKey || route.calls[1].key != wantKey {
		t.Fatalf("Idempotency-Keys = %+v, want %q on both calls", route.calls, wantKey)
	}
	if route.calls[0].raw != route.calls[1].raw {
		t.Fatalf("the retry is not the same request:\n%s\n%s", route.calls[0].raw, route.calls[1].raw)
	}
	if n := len(f.store.Events()); n != eventsBefore+1 {
		t.Fatalf("events appended = %d, want the one ReturnLabelIssued", n-eventsBefore)
	}
}

// The port is handed the authorization's destination node and nothing else
// about the destination: the request type has no prefix to carry.
func TestIssueLabelHandsThePortTheAuthorizationsDestinationNode(t *testing.T) {
	answer := func(f *fixture) {
		f.store.LabelResponse(func(requests.IssueReturnLabel) (requests.ReturnLabel, error) {
			return requests.ReturnLabel{TrackingNumber: "trk-rev-1", Carrier: "RN-GROUND"}, nil
		})
	}
	issue := func(t *testing.T, f *fixture, v views.ReturnAuthorization) requests.IssueReturnLabel {
		t.Helper()
		if _, err := f.svc.IssueLabel(context.Background(), requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 2}); err != nil {
			t.Fatalf("IssueLabel: %v", err)
		}
		calls := f.store.IssuedLabels()
		if len(calls) != 1 {
			t.Fatalf("label calls = %d, want 1", len(calls))
		}
		return calls[0]
	}

	t.Run("the returns centre of a NEAREST_RETURNS_CENTER return", func(t *testing.T) {
		f := newFixture(t)
		f.placeOrder(t, "ord-1")
		v := f.authorize(t, "ord-1", 1, 2000)
		answer(f)
		want := requests.IssueReturnLabel{
			ReturnAuthorizationID: v.AuthorizationID, OrderID: "ord-1", PackageNo: 2, DestinationNodeID: "node-rc-1",
		}
		if got := issue(t, f, v); got != want || v.DestinationNodeID != "node-rc-1" {
			t.Fatalf("label call = %+v, want %+v", got, want)
		}
	})

	t.Run("the fulfilling node of an ORIGIN_NODE return", func(t *testing.T) {
		f := newFixture(t)
		f.seeded = map[string]bool{"topo": true} // no returns centre, no service area
		f.placeOrder(t, "ord-1")
		if err := f.svc.OnOriginFact(context.Background(), "evt-of-1", "FulfillmentLocked", requests.OriginFact{
			OrderID: "ord-1", GroupNo: 1, NodeID: "node-fc-1", Locked: true,
		}); err != nil {
			t.Fatalf("OnOriginFact: %v", err)
		}
		v := f.authorize(t, "ord-1", 1, 2000)
		if v.Disposition != returnauthorization.OriginNode {
			t.Fatalf("setup: disposition = %s, want ORIGIN_NODE", v.Disposition)
		}
		answer(f)
		want := requests.IssueReturnLabel{
			ReturnAuthorizationID: v.AuthorizationID, OrderID: "ord-1", PackageNo: 2, DestinationNodeID: "node-fc-1",
		}
		if got := issue(t, f, v); got != want {
			t.Fatalf("label call = %+v, want %+v", got, want)
		}
	})
}

// The label does not read the order group view: whatever prefix the group
// row holds when the label is asked for, the request names the node.
func TestIssueLabelDoesNotReadTheOrderGroupView(t *testing.T) {
	route := newLabelRoute(t, 0)
	f, v := authorizedToTheReturnsCentre(t, route)
	group, err := f.store.GroupSaver().Group(context.Background(), "ord-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	group.DestinationPostalPrefix = "99"
	if err := f.store.GroupSaver().SaveGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.IssueLabel(context.Background(), requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1}); err != nil {
		t.Fatalf("IssueLabel: %v", err)
	}
	if raw := route.calls[0].raw; strings.Contains(raw, `"99"`) || route.calls[0].body["destinationNodeId"] != "node-rc-1" {
		t.Fatalf("the label request follows the group view: %s", raw)
	}
}
