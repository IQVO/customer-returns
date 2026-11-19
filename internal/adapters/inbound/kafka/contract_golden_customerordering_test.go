package kafka_test

import (
	"context"
	"slices"
	"testing"

	"github.com/IQVO/customer-returns/internal/domain/network"
)

// terminalNoticeFamilies are the ShopperNoticeDecided families the
// OrderGroupView acts on (ADR 0003), with the status each one sets when the
// fact carries none.
var terminalNoticeFamilies = map[string]network.GroupStatus{
	"GROUP_SHIPPED":   network.GroupShipped,
	"GROUP_DELIVERED": network.GroupDelivered,
	"GROUP_LOST":      network.GroupLost,
	"GROUP_RETURNED":  network.GroupReturned,
}

// customerOrderingGoldens: what the two customer-ordering facts leave in the
// OrderGroupView. The ordering consumer claims an id under the event NAME.
func customerOrderingGoldens() map[string]golden {
	return map[string]golden{
		"com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced": {
			claim:  "CustomerOrderPlaced",
			verify: verifyOrderPlaced,
			replay: map[string]any{"destinationPostalPrefix": "REPLAYED"},
		},
		"com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided": {
			claim:  "ShopperNoticeDecided",
			verify: verifyNoticeDecided,
			// a terminal notice of a group nothing else names: applied, it
			// would add a row
			replay: map[string]any{"groupNo": 9001, "family": "GROUP_SHIPPED", "fact": map[string]any{"status": "SHIPPED"}},
		},
	}
}

// verifyOrderPlaced: every group of the placed order is in the view, PROMISED,
// with the ORDER's destination postal prefix (customer-ordering publishes it
// once, on the order; a group has none), its witness node and its lines.
func verifyOrderPlaced(t *testing.T, h *harness, ex pinnedExample) {
	t.Helper()
	data := ex.data()
	orderID, prefix := text(t, data, "orderId"), text(t, data, "destinationPostalPrefix")
	for _, g := range objects(t, data, "groups") {
		got, err := h.store.GroupSaver().Group(context.Background(), orderID, number(t, g["groupNo"]))
		if err != nil {
			t.Fatalf("the placed group %v is not in the view: %v", g["groupNo"], err)
		}
		if got.Status != network.GroupPromised || got.DestinationPostalPrefix != prefix || got.WitnessNodeID != text(t, g, "witnessNodeId") {
			t.Fatalf("group = %+v, want PROMISED with prefix %s and the witness of %v", got, prefix, g)
		}
		if want := placedLines(t, g); !slices.Equal(got.Lines, want) {
			t.Fatalf("lines = %+v, want %+v", got.Lines, want)
		}
	}
}

func placedLines(t *testing.T, group map[string]any) []network.GroupLine {
	t.Helper()
	var lines []network.GroupLine
	for _, l := range objects(t, group, "lines") {
		lines = append(lines, network.GroupLine{
			LineNo: number(t, l["lineNo"]), CatalogItemID: text(t, l, "catalogItemId"), Quantity: number(t, l["quantity"]),
		})
	}
	return lines
}

// verifyNoticeDecided: a terminal family sets the group's status (the fact's
// `status` wins over the family); any other family is acknowledged and no
// order group row is invented for it. The producer's one example is of a
// non-terminal family today; the terminal ones are proven on that example in
// TestTerminalNoticeFamiliesUpdateStatus.
func verifyNoticeDecided(t *testing.T, h *harness, ex pinnedExample) {
	t.Helper()
	data := ex.data()
	orderID, groupNo := text(t, data, "orderId"), number(t, data["groupNo"])
	status, terminal := terminalNoticeFamilies[text(t, data, "family")]
	if !terminal {
		if groups := h.store.Groups(); len(groups) != 0 {
			t.Fatalf("a %s notice created rows: %+v", data["family"], groups)
		}
		return
	}
	if fact := asString(asMap(data["fact"])["status"]); fact != "" {
		status = network.GroupStatus(fact)
	}
	got, err := h.store.GroupSaver().Group(context.Background(), orderID, groupNo)
	if err != nil || got.Status != status {
		t.Fatalf("group = %+v %v, want status %s", got, err, status)
	}
}

// customer-ordering stamps ONE CloudEvents id on both of its examples, and the
// inbox key is the id alone, whatever the type: delivered to one inbox, the
// second example is a redelivery of the first and is swallowed. That is why
// every golden runs on a state of its own. The notice is made terminal for the
// placed group here, so that being swallowed is visible.
func TestExamplesThatShareAnIDAreOneDeliveryToTheInbox(t *testing.T) {
	placed, notice := exampleCustomerOrderingCustomerOrderPlaced, exampleCustomerOrderingShopperNoticeDecided
	id := exampleAttr(t, placed, "id")
	if other := exampleAttr(t, notice, "id"); other != id {
		t.Logf("the producer's two examples no longer share an id (%s, %s); the id of the first is reused below", id, other)
	}
	orderID := exampleField(t, placed, "orderId")
	groupNo := number(t, objects(t, exampleData(t, placed), "groups")[0]["groupNo"])
	shipped := map[string]any{"orderId": orderID, "groupNo": groupNo, "family": "GROUP_SHIPPED", "fact": map[string]any{"status": "SHIPPED"}}
	status := func(h *harness) network.GroupStatus {
		g, err := h.store.GroupSaver().Group(context.Background(), orderID, groupNo)
		if err != nil {
			t.Fatalf("group: %v", err)
		}
		return g.Status
	}

	h := newHarness(t)
	feedExample(t, h.order, placed)
	if err := h.order.HandleMessage(context.Background(), producerEvent(t, notice, id, shipped)); err != nil {
		t.Fatalf("the notice under the placed order's id: %v", err)
	}
	if got := status(h); got != network.GroupPromised {
		t.Fatalf("a second fact under an id already claimed was applied: status = %s", got)
	}
	if claimed, _ := h.store.ClaimedType(id); claimed != "CustomerOrderPlaced" {
		t.Fatalf("the id is claimed as %q, want the first fact's claim", claimed)
	}
	// The same notice under an id of its own is a fact of its own.
	if err := h.order.HandleMessage(context.Background(), producerEvent(t, notice, "e-notice-own-id", shipped)); err != nil {
		t.Fatalf("the notice under its own id: %v", err)
	}
	if got := status(h); got != network.GroupShipped {
		t.Fatalf("status = %s, want SHIPPED", got)
	}
}
