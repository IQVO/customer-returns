package usecases_test

// The reverse-label call names the RETURN DESTINATION's prefix (ADR 0005,
// addendum 2026-11-17): the label prefix of the node the goods go back to,
// read from this context's own node read model. It is never the order group's
// prefix, which is where the shopper lives. Every fixture below gives the
// group and the node DIFFERENT prefixes, so sending the group's fails.

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

const (
	// groupPrefix is the order's destination prefix: the shopper's.
	groupPrefix = "03"
	// The postal codes node-topology registered the two nodes with, and the
	// label prefixes the provider's rule makes of them.
	returnsCentreCode   = "07750-000"
	returnsCentrePrefix = "07750"
	originNodeCode      = "13210-000"
	originNodePrefix    = "13210"
)

// answerLabels makes the label double answer a tracking number.
func (f *fixture) answerLabels() {
	f.store.LabelResponse(func(requests.IssueReturnLabel) (requests.ReturnLabel, error) {
		return requests.ReturnLabel{TrackingNumber: "trk-rev-1", Carrier: "RN-GROUND"}, nil
	})
}

// originOnly places ord-1 in a network with NO returns centre and a lock fact
// naming node-fc-1, so decide answers ORIGIN_NODE; register says what the node
// read model holds about node-fc-1 (nil: the node was never registered here).
func originOnly(t *testing.T, register *requests.NodeRegistered) (*fixture, views.ReturnAuthorization) {
	t.Helper()
	f := newFixture(t)
	f.seeded = map[string]bool{"topo": true} // no returns centre, no service area
	ctx := context.Background()
	f.placeOrder(t, "ord-1")
	if err := f.svc.OnOriginFact(ctx, "evt-of-1", "FulfillmentLocked", requests.OriginFact{
		OrderID: "ord-1", GroupNo: 1, NodeID: "node-fc-1", Locked: true,
	}); err != nil {
		t.Fatalf("OnOriginFact: %v", err)
	}
	if register != nil {
		if err := f.svc.OnNodeRegistered(ctx, "evt-nf-fc", "NodeRegistered", *register); err != nil {
			t.Fatalf("OnNodeRegistered: %v", err)
		}
	}
	v := f.authorize(t, "ord-1", 1, 2000)
	if v.Disposition != returnauthorization.OriginNode || v.DestinationNodeID != "node-fc-1" {
		t.Fatalf("setup: disposition/destination = %s/%s, want ORIGIN_NODE/node-fc-1", v.Disposition, v.DestinationNodeID)
	}
	f.answerLabels()
	return f, v
}

func TestIssueLabelSendsTheReturnsCentresPrefixNotTheGroups(t *testing.T) {
	f := newFixture(t)
	f.placeOrder(t, "ord-1") // the returns centre of the region, the group's prefix groupPrefix
	v := f.authorize(t, "ord-1", 1, 2000)
	if v.Disposition != returnauthorization.NearestReturnsCenter || v.DestinationNodeID != "node-rc-1" {
		t.Fatalf("setup: disposition/destination = %s/%s", v.Disposition, v.DestinationNodeID)
	}
	f.answerLabels()

	got, err := f.svc.IssueLabel(context.Background(), requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1})
	if err != nil {
		t.Fatalf("IssueLabel: %v", err)
	}
	calls := f.store.IssuedLabels()
	if len(calls) != 1 {
		t.Fatalf("label calls = %d, want 1", len(calls))
	}
	want := requests.IssueReturnLabel{
		ReturnAuthorizationID: v.AuthorizationID, OrderID: "ord-1", PackageNo: 1,
		DestinationPostalPrefix: returnsCentrePrefix,
	}
	if calls[0] != want {
		t.Fatalf("label call = %+v, want %+v (the group's prefix is %q)", calls[0], want, groupPrefix)
	}
	if len(got.TrackingNumbers) != 1 || got.TrackingNumbers[0] != "trk-rev-1" {
		t.Fatalf("trackingNumbers = %v", got.TrackingNumbers)
	}
}

func TestIssueLabelSendsTheOriginNodesPrefixForAnOriginNodeReturn(t *testing.T) {
	f, v := originOnly(t, &requests.NodeRegistered{
		NodeID: "node-fc-1", NodeType: network.TypeFulfillmentCenter, RegionID: "reg-9", PostalCode: originNodeCode,
	})
	if _, err := f.svc.IssueLabel(context.Background(), requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1}); err != nil {
		t.Fatalf("IssueLabel: %v", err)
	}
	calls := f.store.IssuedLabels()
	if len(calls) != 1 || calls[0].DestinationPostalPrefix != originNodePrefix {
		t.Fatalf("label calls = %+v, want one with the origin node's prefix %q", calls, originNodePrefix)
	}
}

// The destination node is not in the node read model, or is there without a
// label prefix (registered before the view kept it, or with a postal code that
// holds no letter or digit): the answer is the tolerant label-unavailable, no
// call is made and nothing changes.
func TestIssueLabelIsUnavailableWithoutTheDestinationNodesPrefix(t *testing.T) {
	cases := []struct {
		name     string
		register *requests.NodeRegistered
	}{
		{"the destination node is unknown to the view", nil},
		{"the node has no postal code yet", &requests.NodeRegistered{
			NodeID: "node-fc-1", NodeType: network.TypeFulfillmentCenter, RegionID: "reg-9",
		}},
		{"nothing is left of the node's postal code", &requests.NodeRegistered{
			NodeID: "node-fc-1", NodeType: network.TypeFulfillmentCenter, RegionID: "reg-9", PostalCode: "--- ---",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, v := originOnly(t, c.register)
			eventsBefore := len(f.store.Events())

			_, err := f.svc.IssueLabel(context.Background(), requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1})
			if !errors.Is(err, ports.ErrLabelUnavailable) {
				t.Fatalf("IssueLabel = %v, want ErrLabelUnavailable", err)
			}
			if calls := f.store.IssuedLabels(); len(calls) != 0 {
				t.Fatalf("the label route was called without a destination prefix: %+v", calls)
			}
			if n := len(f.store.Events()); n != eventsBefore {
				t.Fatalf("events appended = %d, want none", n-eventsBefore)
			}
			after, err := f.svc.GetReturnAuthorization(context.Background(), v.AuthorizationID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if after.State != v.State || len(after.TrackingNumbers) != 0 {
				t.Fatalf("the authorization changed: state %v, trackingNumbers %v", after.State, after.TrackingNumbers)
			}
		})
	}
}
