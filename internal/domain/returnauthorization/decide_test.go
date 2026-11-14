package returnauthorization

import (
	"errors"
	"testing"
	"time"
)

func TestDecideTable(t *testing.T) {
	cases := []struct {
		name    string
		in      DecideInput
		want    Disposition
		wantErr error
	}{
		{
			name: "keep under threshold with all prices",
			in: DecideInput{
				Lines:         []Line{{LineNo: 1, Quantity: 2, UnitPriceCents: price(400)}},
				Method:        MethodDropOff,
				NearestNodeID: "node-rc-1",
			},
			want: KeepDisposition,
		},
		{
			name: "keep at exactly the threshold",
			in: DecideInput{
				Lines:  []Line{{LineNo: 1, Quantity: 1, UnitPriceCents: price(1500)}},
				Method: MethodDropOff,
			},
			want: KeepDisposition,
		},
		{
			name: "nearest over the threshold",
			in: DecideInput{
				Lines:         []Line{{LineNo: 1, Quantity: 1, UnitPriceCents: price(1501)}},
				Method:        MethodDropOff,
				NearestNodeID: "node-rc-1",
			},
			want: NearestReturnsCenter,
		},
		{
			name: "absent price never keeps",
			in: DecideInput{
				Lines:         []Line{{LineNo: 1, Quantity: 1}, {LineNo: 2, Quantity: 1, UnitPriceCents: price(1)}},
				Method:        MethodDropOff,
				NearestNodeID: "node-rc-1",
			},
			want: NearestReturnsCenter,
		},
		{
			name: "pickup never keeps even under the threshold",
			in: DecideInput{
				Lines:         []Line{{LineNo: 1, Quantity: 1, UnitPriceCents: price(100)}},
				Method:        MethodPickup,
				NearestNodeID: "node-rc-1",
			},
			want: NearestReturnsCenter,
		},
		{
			name: "origin when no returns centre in the region",
			in: DecideInput{
				Lines:        []Line{{LineNo: 1, Quantity: 1, UnitPriceCents: price(9900)}},
				Method:       MethodDropOff,
				OriginNodeID: "node-fc-1",
			},
			want: OriginNode,
		},
		{
			name: "no destination at all",
			in: DecideInput{
				Lines:  []Line{{LineNo: 1, Quantity: 1, UnitPriceCents: price(9900)}},
				Method: MethodDropOff,
			},
			wantErr: ErrNoReturnDestination,
		},
		{
			name: "absent price with no destination is still no-destination",
			in: DecideInput{
				Lines: []Line{{LineNo: 1, Quantity: 1}},
			},
			wantErr: ErrNoReturnDestination,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decide(tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if got != tc.want {
				t.Fatalf("disposition = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDecideTotalIsQuantityTimesPrice(t *testing.T) {
	in := DecideInput{
		Lines:         []Line{{LineNo: 1, Quantity: 3, UnitPriceCents: price(600)}},
		Method:        MethodDropOff,
		NearestNodeID: "node-rc-1",
	}
	got, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got != NearestReturnsCenter {
		t.Fatalf("1800 total must not KEEP, got %s", got)
	}
}

func TestDestinationFor(t *testing.T) {
	in := DecideInput{NearestNodeID: "node-rc-1", OriginNodeID: "node-fc-1"}
	if DestinationFor(KeepDisposition, in) != "" {
		t.Fatal("KEEP destination")
	}
	if DestinationFor(NearestReturnsCenter, in) != "node-rc-1" {
		t.Fatal("NEAREST destination")
	}
	if DestinationFor(OriginNode, in) != "node-fc-1" {
		t.Fatal("ORIGIN destination")
	}
}

func TestAuthorizeValidatesInput(t *testing.T) {
	base := NewAuthorization{
		ID: "ret-1", OrderID: "ord-1", GroupNo: 1,
		Lines:  []Line{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 1, UnitPriceCents: price(1)}},
		Reason: ReasonOther, Disposition: NearestReturnsCenter, Method: MethodDropOff,
		DestinationNodeID: "node-rc-1", AuthorizedAt: now,
	}
	if _, err := Authorize(base, now); err != nil {
		t.Fatalf("valid base: %v", err)
	}
	manyLines := make([]Line, MaxLinesPerReturn+1)
	for i := range manyLines {
		manyLines[i] = Line{LineNo: i + 1, CatalogItemID: "sku-1", Quantity: 1}
	}
	bad := []struct {
		name string
		mut  func(*NewAuthorization)
	}{
		{"no lines", func(n *NewAuthorization) { n.Lines = nil }},
		{"too many lines", func(n *NewAuthorization) { n.Lines = manyLines }},
		{"bad reason", func(n *NewAuthorization) { n.Reason = "BECAUSE" }},
		{"bad disposition", func(n *NewAuthorization) { n.Disposition = "TOSS" }},
		{"bad method", func(n *NewAuthorization) { n.Method = "COURIER_PIGEON" }},
		{"nearest without destination", func(n *NewAuthorization) { n.DestinationNodeID = "" }},
		{"keep with destination", func(n *NewAuthorization) { n.Disposition = KeepDisposition; n.DestinationNodeID = "node-rc-1" }},
		{"pickup without window", func(n *NewAuthorization) { n.Method = MethodPickup }},
		{"pickup keep", func(n *NewAuthorization) {
			n.Method = MethodPickup
			n.Disposition = KeepDisposition
			n.DestinationNodeID = ""
			n.PickupWindowStart = now.Add(time.Hour)
			n.PickupWindowEnd = now.Add(2 * time.Hour)
		}},
		{"window outside ttl", func(n *NewAuthorization) {
			n.Method = MethodPickup
			n.PickupWindowStart = now.Add(720 * time.Hour)
			n.PickupWindowEnd = now.Add(721 * time.Hour)
		}},
		{"zero quantity", func(n *NewAuthorization) {
			n.Lines = []Line{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 0}}
		}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mut(&in)
			if _, err := Authorize(in, now); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}
