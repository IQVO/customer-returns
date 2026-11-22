package usecases_test

// CancelReturn on an authorization past its TTL is NOT refused: the lazy expiry IS the
// cancellation. The use case cancels it with reason EXPIRED and answers the CANCELLED
// authorization (200 on REST) — never authorization-expired, which only IssueLabel answers
// (ADR 0002 A11). This pins the as-built behaviour the OpenAPI spec and the docs describe.

import (
	"context"
	"testing"

	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

func cancelledReasons(f *fixture) []returnauthorization.CancelReason {
	var out []returnauthorization.CancelReason
	for _, e := range f.store.Events() {
		if c, ok := e.(returnauthorization.Cancelled); ok {
			out = append(out, c.Reason)
		}
	}
	return out
}

func TestCancelReturnOnAnExpiredAuthorizationCancelsAsExpiredAndSucceeds(t *testing.T) {
	f := newFixture(t)
	f.placeOrder(t, "ord-1")
	v := f.authorize(t, "ord-1", 1, 2000)
	f.clock.Set(pastTTL())

	got, err := f.svc.CancelReturn(context.Background(), requests.CancelReturn{AuthorizationID: v.AuthorizationID})

	if err != nil {
		t.Fatalf("CancelReturn on an expired authorization = %v, want success (the expiry is the cancellation)", err)
	}
	if got.State != returnauthorization.StateCancelled {
		t.Fatalf("state = %q, want CANCELLED", got.State)
	}
	reasons := cancelledReasons(f)
	if len(reasons) != 1 || reasons[0] != returnauthorization.CancelExpired {
		t.Fatalf("ReturnCancelled reasons = %v, want exactly [EXPIRED]", reasons)
	}
}

func TestCancelReturnBeforeExpiryIsShopperRequested(t *testing.T) {
	f := newFixture(t)
	f.placeOrder(t, "ord-1")
	v := f.authorize(t, "ord-1", 1, 2000)

	if _, err := f.svc.CancelReturn(context.Background(), requests.CancelReturn{AuthorizationID: v.AuthorizationID}); err != nil {
		t.Fatalf("CancelReturn = %v", err)
	}
	reasons := cancelledReasons(f)
	if len(reasons) != 1 || reasons[0] != returnauthorization.CancelShopperRequested {
		t.Fatalf("ReturnCancelled reasons = %v, want exactly [SHOPPER_REQUESTED]", reasons)
	}
}
