package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/application/ports"
)

// The two event types the inbox contract claims under: the full CloudEvents
// type, exactly what the node consumer hands to the use case.
const (
	typeRegistered = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"
	typeActivated  = "com.retailnetwork.logistics.node-topology.node.NodeActivated"
)

var errUnitFailed = errors.New("the unit of work failed")

// Inbox is the contract of the consumers' inbox (processed_events), the facts
// every replay and rebuild procedure of the runbook rests on:
//
//   - an event id is claimed once: a redelivery of a claimed id answers false,
//     so the consumer skips it;
//   - a claim made inside a unit of work that fails is rolled back with it, so
//     a fact that was dead-lettered was never claimed;
//   - deleting the claims of one event type (forget: the operator's DELETE)
//     makes exactly those ids claimable again and leaves every other claim.
//
// forget deletes the claims of eventType and reports how many it deleted.
func Inbox(t *testing.T, inbox ports.ProcessedEvents, uow ports.UnitOfWork, forget func(eventType string) int) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 11, 15, 4, 40, 31, 0, time.UTC)
	claim := func(t *testing.T, ctx context.Context, id, eventType string) bool {
		t.Helper()
		first, err := inbox.Claim(ctx, id, eventType, at)
		if err != nil {
			t.Fatalf("Claim(%s): %v", id, err)
		}
		return first
	}

	t.Run("a claimed id is not claimed twice", func(t *testing.T) {
		if !claim(t, ctx, "evt-registered-1", typeRegistered) {
			t.Fatal("the first delivery of an id must be claimed")
		}
		if claim(t, ctx, "evt-registered-1", typeRegistered) {
			t.Fatal("a redelivered id must not be claimed again: the consumer would apply the fact twice")
		}
	})

	t.Run("a claim rolls back with the unit of work that fails", func(t *testing.T) {
		err := uow.Do(ctx, func(ctx context.Context) error {
			if !claim(t, ctx, "evt-failed-1", typeActivated) {
				t.Fatal("the id was claimable inside the unit")
			}
			return errUnitFailed
		})
		if !errors.Is(err, errUnitFailed) {
			t.Fatalf("Do = %v, want the unit's own error", err)
		}
		if !claim(t, ctx, "evt-failed-1", typeActivated) {
			t.Fatal("the failed unit left its claim behind: the fact could never be applied again")
		}
	})

	t.Run("deleting the claims of one type makes those ids claimable again and no other", func(t *testing.T) {
		claim(t, ctx, "evt-registered-2", typeRegistered)
		claim(t, ctx, "evt-activated-2", typeActivated)
		// evt-registered-1 and evt-registered-2 are the claims of the type.
		if n := forget(typeRegistered); n != 2 {
			t.Fatalf("forget deleted %d claims, want the 2 of the type", n)
		}
		if !claim(t, ctx, "evt-registered-2", typeRegistered) {
			t.Fatal("a forgotten id must be claimable again: the replay re-applies the fact")
		}
		if claim(t, ctx, "evt-activated-2", typeActivated) {
			t.Fatal("the claim of another type was deleted too")
		}
	})
}
