package usecases

// In-package: the no-op metrics of the zero Deps (ADR 0008) and the label
// refusal branches the HTTP/BDD paths do not reach.

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// authorizedOf builds a live AUTHORIZATION for the label checks.
func authorizedOf(t *testing.T, disposition, method string) *returnauthorization.ReturnAuthorization {
	t.Helper()
	a, err := returnauthorization.Authorize(returnauthorization.NewAuthorization{
		ID: "ret-l1", OrderID: "ord-1", GroupNo: 1,
		Lines:             []returnauthorization.Line{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}},
		Reason:            returnauthorization.ReasonDefective,
		Disposition:       returnauthorization.Disposition(disposition),
		Method:            returnauthorization.Method(method),
		DestinationNodeID: "node-rc-1",
		AuthorizedAt:      time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
	}, time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	return a
}

func cancelledOf(t *testing.T) *returnauthorization.ReturnAuthorization {
	t.Helper()
	a := authorizedOf(t, "NEAREST_RETURNS_CENTER", "DROP_OFF")
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	if err := a.Cancel(returnauthorization.CancelShopperRequested, at); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	return a
}

func keepOf(t *testing.T) *returnauthorization.ReturnAuthorization {
	t.Helper()
	// KEEP closes at once, needs no destination and needs priced lines.
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	c := int64(4000)
	a, err := returnauthorization.Authorize(returnauthorization.NewAuthorization{
		ID: "ret-k1", OrderID: "ord-1", GroupNo: 1,
		Lines:  []returnauthorization.Line{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2, UnitPriceCents: &c}},
		Reason: returnauthorization.ReasonDefective, Disposition: returnauthorization.KeepDisposition,
		Method: returnauthorization.MethodDropOff, AuthorizedAt: at,
	}, at)
	if err != nil {
		t.Fatalf("Authorize KEEP: %v", err)
	}
	return a
}

func TestNoopMetricsCountsNothingButNeverPanics(t *testing.T) {
	var m ports.ReturnMetrics = noopMetrics{}
	m.ReturnAuthorized(context.Background(), "NEAREST_RETURNS_CENTER")
	m.ReturnRejected(context.Background(), "validation-failed")
	m.OutcomeApplied(context.Background(), 0)
}

func TestCheckLabelableRefusesClosedAndKeep(t *testing.T) {
	s := &Service{}
	if err := s.checkLabelable(authorizedOf(t, "NEAREST_RETURNS_CENTER", "DROP_OFF")); err != nil {
		t.Fatalf("an AUTHORIZED drop-off return is labelable: %v", err)
	}
	if err := s.checkLabelable(cancelledOf(t)); err == nil {
		t.Fatal("a CANCELLED return is not labelable")
	}
	if err := s.checkLabelable(keepOf(t)); err == nil {
		t.Fatal("a KEEP return links no label")
	}
}
