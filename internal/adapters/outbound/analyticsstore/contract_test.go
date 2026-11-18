package analyticsstore_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

// store is what both twins implement: the writer and the reader port.
type store interface {
	returnsperformance.Projection
	returnsperformance.Reader
}

// runContract is THE contract of the analytical store (ADR 0012). It runs
// against MemoryStore (memory_test.go) and against real Postgres
// (postgres_integration_test.go); newStore returns an EMPTY store.
func runContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Run("an empty store answers empty, non-nil reports", func(t *testing.T) { contractEmpty(t, newStore(t)) })
	t.Run("a replayed id is a no-op", func(t *testing.T) { contractReplay(t, newStore(t)) })
	t.Run("a new event id for the same authorization replaces its fact", func(t *testing.T) { contractUpsert(t, newStore(t)) })
	t.Run("authorizations and receipts per day", func(t *testing.T) { contractDays(t, newStore(t)) })
	t.Run("the reason counts of the range", func(t *testing.T) { contractReasons(t, newStore(t)) })
	t.Run("the freshness basis is the newest CloudEvents time", func(t *testing.T) { contractLastEvent(t, newStore(t)) })
	t.Run("an invalid fact is rejected and leaves nothing", func(t *testing.T) { contractRejected(t, newStore(t)) })
}

var (
	ctx       = context.Background()
	nov3      = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	nov4      = nov3.Add(24 * time.Hour)
	nov5      = nov4.Add(24 * time.Hour)
	fullRange = returnsperformance.Range{From: nov3, To: nov5}
)

// facts builds the events of a store; every event gets a fresh id and a
// CloudEvents time one minute after its business instant.
type facts struct {
	t   *testing.T
	s   store
	seq int
}

func (f *facts) apply(kind returnsperformance.Kind, authorizationID, reason string, at time.Time) {
	f.t.Helper()
	f.seq++
	e := returnsperformance.Event{
		Kind: kind, EventID: fmt.Sprintf("evt-%s-%d", authorizationID, f.seq), At: at.Add(time.Minute),
		AuthorizationID: authorizationID, OccurredAt: at, Reason: reason,
	}
	applied, err := f.s.Apply(ctx, e)
	if err != nil || !applied {
		f.t.Fatalf("apply %s %s: applied=%v err=%v", kind, e.EventID, applied, err)
	}
}

func (f *facts) authorized(id, reason string, at time.Time) {
	f.t.Helper()
	f.apply(returnsperformance.KindAuthorized, id, reason, at)
}

func (f *facts) received(id string, at time.Time) {
	f.t.Helper()
	f.apply(returnsperformance.KindCompleted, id, "", at)
}

// seedScenario writes the fixture both report contracts read (see the ADR's
// KPI definitions). Range [nov3, nov5).
func seedScenario(t *testing.T, s store) {
	t.Helper()
	f := &facts{t: t, s: s}
	// R1, R2: DEFECTIVE on nov3, received on nov4 after 24h and 26h.
	f.authorized("R1", "DEFECTIVE", nov3.Add(8*time.Hour))
	f.received("R1", nov4.Add(8*time.Hour))
	f.authorized("R2", "DEFECTIVE", nov3.Add(20*time.Hour))
	f.received("R2", nov4.Add(22*time.Hour))
	// R3 never comes back; R4 is authorized one second before To and received exactly at To (exclusive).
	f.authorized("R3", "NOT_NEEDED", nov4.Add(10*time.Hour))
	f.authorized("R4", "WRONG_ITEM", nov5.Add(-time.Second))
	f.received("R4", nov5)
	// R5 was authorized one second before From and received inside the range.
	f.authorized("R5", "OTHER", nov3.Add(-time.Second))
	f.received("R5", nov3.Add(time.Hour))
	// R6 is a legacy receipt: its authorization was never projected.
	f.received("R6", nov4.Add(3*time.Hour))
	// R7 was dated received before it was authorized (clock skew): zero seconds.
	f.authorized("R7", "OTHER", nov4.Add(12*time.Hour))
	f.received("R7", nov4.Add(11*time.Hour))
	// R8 is authorized exactly at To (exclusive).
	f.authorized("R8", "DEFECTIVE", nov5)
}

func contractEmpty(t *testing.T, s store) {
	auth, err := s.AuthorizedPerDay(ctx, fullRange)
	if err != nil || auth == nil || len(auth) != 0 {
		t.Fatalf("authorized = %#v, %v; want empty non-nil", auth, err)
	}
	recv, err := s.ReceivedPerDay(ctx, fullRange)
	if err != nil || recv == nil || len(recv) != 0 {
		t.Fatalf("received = %#v, %v; want empty non-nil", recv, err)
	}
	reasons, err := s.ReasonCounts(ctx, fullRange)
	if err != nil || reasons == nil || len(reasons) != 0 {
		t.Fatalf("reasons = %#v, %v; want empty non-nil", reasons, err)
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("last = %v, %v; want nil", last, err)
	}
}

func contractReplay(t *testing.T, s store) {
	e := returnsperformance.Event{Kind: returnsperformance.KindAuthorized, EventID: "evt-1", AuthorizationID: "R1", Reason: "OTHER", At: nov3.Add(time.Hour), OccurredAt: nov3}
	if applied, err := s.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("first apply: %v %v", applied, err)
	}
	if applied, err := s.Apply(ctx, e); err != nil || applied {
		t.Fatalf("replay: applied=%v err=%v; want a no-op", applied, err)
	}
	auth, err := s.AuthorizedPerDay(ctx, fullRange)
	if err != nil || len(auth) != 1 || auth[0].Count != 1 {
		t.Fatalf("authorized after a replay = %+v, %v; want one", auth, err)
	}
}

func contractUpsert(t *testing.T, s store) {
	f := &facts{t: t, s: s}
	f.authorized("R1", "OTHER", nov3.Add(time.Hour))
	f.authorized("R1", "DEFECTIVE", nov4.Add(time.Hour))
	auth, err := s.AuthorizedPerDay(ctx, fullRange)
	if err != nil || len(auth) != 1 || !auth[0].Day.Equal(nov4) || auth[0].Count != 1 {
		t.Fatalf("authorized = %+v, %v; want the single fact on nov4", auth, err)
	}
	reasons, err := s.ReasonCounts(ctx, fullRange)
	if err != nil || !reflect.DeepEqual(reasons, []returnsperformance.ReasonCount{{Reason: "DEFECTIVE", Count: 1}}) {
		t.Fatalf("reasons = %+v, %v; want DEFECTIVE only", reasons, err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func nearPtr(got *float64, want float64) bool { return got != nil && near(*got, want) }

func contractDays(t *testing.T, s store) {
	seedScenario(t, s)
	auth, err := s.AuthorizedPerDay(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	wantAuth := []returnsperformance.AuthorizedDay{{Day: nov3, Count: 2}, {Day: nov4, Count: 3}}
	if !sameAuthorized(auth, wantAuth) {
		t.Fatalf("authorized = %+v\nwant %+v", auth, wantAuth)
	}

	recv, err := s.ReceivedPerDay(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	// nov3: R5 alone (3601s). nov4: R1 86400, R2 93600, R7 0 (clamped) and the legacy R6 (no sample):
	// p50 = 86400 and p95 = 86400 + 0.9 * (93600 - 86400) = 92880.
	want := []returnsperformance.ReceivedDay{
		{Day: nov3, Received: 1, Samples: 1, P50: ptr(3601), P95: ptr(3601)},
		{Day: nov4, Received: 4, Samples: 3, P50: ptr(86400), P95: ptr(92880)},
	}
	if len(recv) != len(want) {
		t.Fatalf("received = %+v, want %d days", recv, len(want))
	}
	for i := range want {
		if !sameReceived(recv[i], want[i]) {
			t.Errorf("received row %d = %+v, want %+v", i, recv[i], want[i])
		}
	}
	one, err := s.ReceivedPerDay(ctx, returnsperformance.Range{From: nov4, To: nov5})
	if err != nil || len(one) != 1 || !one[0].Day.Equal(nov4) {
		t.Fatalf("a one-day range = %+v, %v", one, err)
	}
}

func ptr(v float64) *float64 { return &v }

// sameAuthorized compares the days as instants and demands them in UTC.
func sameAuthorized(got, want []returnsperformance.AuthorizedDay) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if !got[i].Day.Equal(want[i].Day) || got[i].Day.Location() != time.UTC || got[i].Count != want[i].Count {
			return false
		}
	}
	return true
}

func sameReceived(got, want returnsperformance.ReceivedDay) bool {
	return got.Day.Equal(want.Day) && got.Day.Location() == time.UTC && got.Received == want.Received && got.Samples == want.Samples &&
		nearPtr(got.P50, *want.P50) && nearPtr(got.P95, *want.P95)
}

func contractReasons(t *testing.T, s store) {
	seedScenario(t, s)
	got, err := s.ReasonCounts(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Reason < got[j].Reason })
	want := []returnsperformance.ReasonCount{{Reason: "DEFECTIVE", Count: 2}, {Reason: "NOT_NEEDED", Count: 1}, {Reason: "OTHER", Count: 1}, {Reason: "WRONG_ITEM", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reasons = %+v\nwant %+v", got, want)
	}
}

func contractLastEvent(t *testing.T, s store) {
	f := &facts{t: t, s: s}
	f.authorized("R1", "OTHER", nov3.Add(5*time.Hour))
	f.received("R1", nov3.Add(2*time.Hour))
	last, err := s.LastEventAt(ctx)
	newest := nov3.Add(5*time.Hour + time.Minute)
	if err != nil || last == nil || !last.Equal(newest) || last.Location() != time.UTC {
		t.Fatalf("last = %v, %v; want %v UTC (the newest, not the last applied)", last, err, newest)
	}
}

func contractRejected(t *testing.T, s store) {
	bad := []returnsperformance.Event{
		{Kind: returnsperformance.KindAuthorized, EventID: "evt-bad-1", At: nov3, OccurredAt: nov3, AuthorizationID: "R1"},
		{Kind: "ReturnCancelled", EventID: "evt-bad-2", AuthorizationID: "R1", At: nov3, OccurredAt: nov3},
		{Kind: returnsperformance.KindCompleted, EventID: "evt-bad-3", At: nov3, OccurredAt: nov3},
	}
	for _, e := range bad {
		if applied, err := s.Apply(ctx, e); applied || !errors.Is(err, returnsperformance.ErrRejected) {
			t.Errorf("%s: applied=%v err=%v; want ErrRejected", e.EventID, applied, err)
		}
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("a rejected fact must leave no mark, last = %v, %v", last, err)
	}
}
