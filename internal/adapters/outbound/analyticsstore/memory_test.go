package analyticsstore_test

import (
	"errors"
	"testing"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/analyticsstore"
	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

func TestMemoryStoreContract(t *testing.T) {
	runContract(t, func(*testing.T) store { return analyticsstore.NewMemoryStore() })
}

func TestMemoryStoreFailIsReturnedOnce(t *testing.T) {
	m := analyticsstore.NewMemoryStore()
	boom := errors.New("database down")
	m.Fail = boom
	e := returnsperformance.Event{Kind: returnsperformance.KindCompleted, EventID: "e", AuthorizationID: "R1", At: nov3, OccurredAt: nov3}
	if _, err := m.Apply(ctx, e); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the injected failure", err)
	}
	if applied, err := m.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("the retry must apply: %v %v", applied, err)
	}
	if err := m.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}
