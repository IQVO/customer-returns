package usecases_test

// The lazy expiry of the label route is the one refusal of this service that COMMITS state and
// still answers an error: authorization-expired (422) is the OUTCOME replay class
// (docs/adr/0013-idempotency-replay-classes.md). These tests prove it from the use case: what the
// refusal committed, and that it is only answered when it really did commit.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/memory"
	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// pastTTL is an instant after the authorization's 30-day expiry.
func pastTTL() time.Time { return t0.Add(returnauthorization.ReturnAuthorizationTTL + time.Hour) }

func cancelEvents(f *fixture) int {
	n := 0
	for _, e := range f.store.Events() {
		if e.EventName() == "ReturnCancelled" {
			n++
		}
	}
	return n
}

func TestIssueLabelOnAnExpiredAuthorizationCommitsTheCancellationThenRefuses(t *testing.T) {
	f := newFixture(t)
	f.placeOrder(t, "ord-1")
	v := f.authorize(t, "ord-1", 1, 2000)
	if cancelEvents(f) != 0 {
		t.Fatalf("setup: a cancellation event before the expiry")
	}
	f.clock.Set(pastTTL())

	_, err := f.svc.IssueLabel(context.Background(), requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1})

	if !errors.Is(err, usecases.ErrAuthorizationExpired) {
		t.Fatalf("IssueLabel = %v, want authorization expired", err)
	}
	got, gerr := f.svc.GetReturnAuthorization(context.Background(), v.AuthorizationID)
	if gerr != nil || got.State != returnauthorization.StateCancelled {
		t.Fatalf("authorization after the refusal = %q (%v), want CANCELLED: the refusal commits the lazy expiry", got.State, gerr)
	}
	if n := cancelEvents(f); n != 1 {
		t.Fatalf("cancellation events after the refusal = %d, want 1 (the outbox row commits with it)", n)
	}
	if calls := f.store.IssuedLabels(); len(calls) != 0 {
		t.Fatalf("label calls = %d, want 0: an expired authorization asks the label owner for nothing", len(calls))
	}

	// The next label request finds a CANCELLED authorization: a plain refusal that records nothing.
	_, again := f.svc.IssueLabel(context.Background(), requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1})
	if again == nil || errors.Is(again, usecases.ErrAuthorizationExpired) {
		t.Fatalf("second IssueLabel = %v, want an illegal transition, not a second expiry", again)
	}
	if n := cancelEvents(f); n != 1 {
		t.Fatalf("cancellation events after a second request = %d, want 1", n)
	}
}

// saveFails is an authorization repository whose Save always answers err.
type saveFails struct {
	ports.AuthorizationRepository
	err error
}

func (r saveFails) Save(context.Context, *returnauthorization.ReturnAuthorization) error {
	return r.err
}

// authorization-expired is answered only when the cancellation really committed: when the lazy
// expiry cannot be saved the caller gets the save's own error (a lost race is 409 concurrent-update,
// RETRYABLE), never an OUTCOME 422 that nothing backs.
func TestIssueLabelAnswersTheSaveErrorWhenTheLazyExpiryCannotBeSaved(t *testing.T) {
	f := newFixture(t)
	f.placeOrder(t, "ord-1")
	v := f.authorize(t, "ord-1", 1, 2000)
	f.clock.Set(pastTTL())
	svc := usecases.New(usecases.Deps{
		UoW:            memory.NewUnitOfWork(f.store),
		Authorizations: saveFails{memory.NewAuthorizationRepository(f.store), ports.ErrConcurrentUpdate},
		Groups:         memory.NewGroupViewRepository(f.store), Network: memory.NewNetworkViewRepository(f.store),
		Outcomes: memory.NewOutcomeFacts(f.store), Labels: memory.NewLabelIssuer(f.store),
		Outbox: memory.NewOutbox(f.store), Inbox: memory.NewInbox(f.store),
		Clock: f.clock, IDs: memory.NewSequentialIDs(),
	}, usecases.Config{})

	_, err := svc.IssueLabel(context.Background(), requests.IssueLabel{AuthorizationID: v.AuthorizationID, PackageNo: 1})

	if !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("IssueLabel = %v, want the save's concurrent update", err)
	}
	if errors.Is(err, usecases.ErrAuthorizationExpired) {
		t.Fatalf("IssueLabel = %v: an unsaved expiry must not be answered as authorization-expired", err)
	}
	if n := cancelEvents(f); n != 0 {
		t.Fatalf("cancellation events = %d, want 0: nothing committed", n)
	}
}
