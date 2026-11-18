package kafka_test

import (
	"testing"

	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// vendorConnectivityGoldens: what the vendors' graded outcome leaves behind.
// The vendor consumer claims an id under the event name.
func vendorConnectivityGoldens() map[string]golden {
	return map[string]golden{
		"com.retailnetwork.partner.vendor-connectivity.returnoutcome.VendorReturnOutcomeSubmitted": {
			claim:  "VendorReturnOutcomeSubmitted",
			verify: verifyOutcomeSubmitted,
			// applied, the redelivery would append a second fact
			replay: map[string]any{"nodeId": "node-replayed"},
		},
	}
}

// verifyOutcomeSubmitted: the authorization of the example does not exist in
// a fresh state, so the outcome is recorded as ONE fact with every line the
// vendor sent, owner included, and no domain event is published for it.
func verifyOutcomeSubmitted(t *testing.T, h *harness, ex pinnedExample) {
	t.Helper()
	data := ex.data()
	facts := h.store.OutcomeFactRows()
	if len(facts) != 1 {
		t.Fatalf("facts = %+v, want exactly one", facts)
	}
	fact := facts[0]
	if fact.AuthorizationID != returnauthorization.ID(text(t, data, "authorizationId")) || fact.NodeID != text(t, data, "nodeId") ||
		!fact.ReceivedAt.Equal(instant(t, data["receivedAt"])) {
		t.Fatalf("fact = %+v, want the authorization, node and receivedAt of %v", fact, data)
	}
	want := objects(t, data, "lines")
	if len(fact.Lines) != len(want) {
		t.Fatalf("lines = %+v, want %d", fact.Lines, len(want))
	}
	for i, l := range want {
		assertOutcomeLine(t, fact.Lines[i], l)
	}
	if events := h.store.Events(); len(events) != 0 {
		t.Fatalf("an outcome of an unknown authorization published %d event(s)", len(events))
	}
}

func assertOutcomeLine(t *testing.T, got requests.OutcomeFactLine, want map[string]any) {
	t.Helper()
	if got.CatalogItemID != text(t, want, "catalogItemId") || got.OwnerRef != asString(want["ownerRef"]) ||
		got.QtyRestockable != number(t, want["qtyRestockable"]) || got.QtyUnsellable != number(t, want["qtyUnsellable"]) {
		t.Fatalf("line = %+v, want %v", got, want)
	}
}
