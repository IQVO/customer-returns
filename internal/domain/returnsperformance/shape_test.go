package returnsperformance_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

func ptr(v float64) *float64 { return &v }

func TestReturnsPerformanceMergesBothSidesByDay(t *testing.T) {
	d14, d15, d16 := utcDay(2026, 11, 14), utcDay(2026, 11, 15), utcDay(2026, 11, 16)
	got := returnsperformance.ReturnsPerformance(
		[]returnsperformance.AuthorizedDay{{Day: d16, Count: 2}, {Day: d14, Count: 3}},
		[]returnsperformance.ReceivedDay{
			{Day: d15, Received: 2},
			{Day: d14, Received: 1, Samples: 1, P50: ptr(518400), P95: ptr(600000)},
		},
	)
	want := []returnsperformance.DayRow{
		{Day: d14, Authorized: 3, Received: 1, Samples: 1, P50: ptr(518400), P95: ptr(600000)},
		{Day: d15, Received: 2},
		{Day: d16, Authorized: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v\nwant %+v", got, want)
	}
}

func TestReturnsPerformanceNormalisesTheDayToUTC(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	// 22:00 BRT on the 14th is 01:00 UTC on the 15th, the same UTC day as the receipt.
	got := returnsperformance.ReturnsPerformance(
		[]returnsperformance.AuthorizedDay{{Day: time.Date(2026, 11, 14, 22, 0, 0, 0, brt), Count: 1}},
		[]returnsperformance.ReceivedDay{{Day: utcDay(2026, 11, 15), Received: 4}},
	)
	if len(got) != 1 || got[0].Authorized != 1 || got[0].Received != 4 || !got[0].Day.Equal(utcDay(2026, 11, 15)) {
		t.Fatalf("rows = %+v, want one merged 2026-11-15 row", got)
	}
}

func TestReturnsPerformanceEmptyIsNonNil(t *testing.T) {
	if got := returnsperformance.ReturnsPerformance(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("nil inputs must yield an empty, non-nil slice, got %#v", got)
	}
}

func TestReasonParetoRanksSharesAndAccumulates(t *testing.T) {
	got := returnsperformance.ReasonPareto([]returnsperformance.ReasonCount{
		{Reason: "OTHER", Count: 1}, {Reason: "DEFECTIVE", Count: 4}, {Reason: "NOT_NEEDED", Count: 3}, {Reason: "WRONG_ITEM", Count: 2},
	})
	want := []returnsperformance.ParetoRow{
		{Reason: "DEFECTIVE", Returns: 4, Share: 0.4, CumulativeShare: 0.4},
		{Reason: "NOT_NEEDED", Returns: 3, Share: 0.3, CumulativeShare: 0.7},
		{Reason: "WRONG_ITEM", Returns: 2, Share: 0.2, CumulativeShare: 0.9},
		{Reason: "OTHER", Returns: 1, Share: 0.1, CumulativeShare: 1},
	}
	for i := range want {
		if got[i].Reason != want[i].Reason || got[i].Returns != want[i].Returns ||
			!near(got[i].Share, want[i].Share) || !near(got[i].CumulativeShare, want[i].CumulativeShare) {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
}

func near(a, b float64) bool { return a > b-1e-9 && a < b+1e-9 }

func TestReasonParetoBreaksTiesByReason(t *testing.T) {
	got := returnsperformance.ReasonPareto([]returnsperformance.ReasonCount{
		{Reason: "WRONG_ITEM", Count: 2}, {Reason: "DEFECTIVE", Count: 2}, {Reason: "OTHER", Count: 5},
	})
	order := []string{got[0].Reason, got[1].Reason, got[2].Reason}
	if !reflect.DeepEqual(order, []string{"OTHER", "DEFECTIVE", "WRONG_ITEM"}) {
		t.Fatalf("order = %v, want OTHER (5), then the 2-ties by reason", order)
	}
}

func TestReasonParetoIsBoundedToFiveButSharesStayOverTheWholeRange(t *testing.T) {
	counts := []returnsperformance.ReasonCount{
		{Reason: "A", Count: 6}, {Reason: "B", Count: 5}, {Reason: "C", Count: 4}, {Reason: "D", Count: 3},
		{Reason: "E", Count: 2}, {Reason: "F", Count: 1}, {Reason: "G", Count: 1},
	}
	got := returnsperformance.ReasonPareto(counts)
	if len(got) != 5 {
		t.Fatalf("len = %d, want the bound 5", len(got))
	}
	if got[4].Reason != "E" || !near(got[4].Share, 2.0/22) || !near(got[4].CumulativeShare, 20.0/22) {
		t.Fatalf("5th = %+v: shares must be over all 22 authorizations, so the cumulative stops at 20/22", got[4])
	}
	exactly := returnsperformance.ReasonPareto(counts[:5])
	if len(exactly) != 5 || !near(exactly[4].CumulativeShare, 1) {
		t.Fatalf("exactly five reasons must all be kept and sum to 1, got %+v", exactly)
	}
	if counts[0].Reason != "A" || counts[5].Reason != "F" {
		t.Fatal("ReasonPareto must not reorder its input")
	}
}

func TestReasonParetoEmptyIsNonNil(t *testing.T) {
	if got := returnsperformance.ReasonPareto(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", got)
	}
}

func TestRate(t *testing.T) {
	cases := []struct {
		num, den int
		want     float64
	}{{1, 4, 0.25}, {3, 3, 1}, {0, 5, 0}, {2, 0, 0}, {2, -1, 0}, {1, 1, 1}}
	for _, c := range cases {
		if got := returnsperformance.Rate(c.num, c.den); got != c.want {
			t.Errorf("Rate(%d, %d) = %v, want %v", c.num, c.den, got, c.want)
		}
	}
}

func TestPercentileMatchesPercentileCont(t *testing.T) {
	values := []float64{7, 1, 3, 5}
	cases := []struct{ p, want float64 }{{0.5, 4}, {0.95, 6.7}, {0, 1}, {1, 7}, {0.25, 2.5}}
	for _, c := range cases {
		if got := returnsperformance.Percentile(values, c.p); !near(got, c.want) {
			t.Errorf("Percentile(%v) = %v, want %v", c.p, got, c.want)
		}
	}
	if !reflect.DeepEqual(values, []float64{7, 1, 3, 5}) {
		t.Fatalf("Percentile modified its input: %v", values)
	}
	if got := returnsperformance.Percentile(nil, 0.5); got != 0 {
		t.Fatalf("empty = %v, want 0", got)
	}
	if got := returnsperformance.Percentile([]float64{3}, 0.95); got != 3 {
		t.Fatalf("single = %v, want 3", got)
	}
}

func TestComputeFreshness(t *testing.T) {
	if f := returnsperformance.ComputeFreshness(nil, now); f.AsOf != nil || f.LagSeconds != nil {
		t.Fatalf("nothing applied must give nil/nil, got %+v", f)
	}
	asOf := time.Date(2026, 11, 8, 18, 29, 0, 0, time.UTC) // now is 18:30 UTC
	f := returnsperformance.ComputeFreshness(&asOf, now)
	if f.AsOf == nil || !f.AsOf.Equal(asOf) || f.AsOf.Location() != time.UTC || f.LagSeconds == nil || *f.LagSeconds != 60 {
		t.Fatalf("freshness = %+v", f)
	}
	future := now.Add(time.Minute)
	if f := returnsperformance.ComputeFreshness(&future, now); *f.LagSeconds != 0 {
		t.Fatalf("a clock behind the event must read zero lag, got %v", *f.LagSeconds)
	}
}
