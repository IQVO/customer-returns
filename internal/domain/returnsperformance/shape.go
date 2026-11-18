package returnsperformance

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// DayRow is one report day: the authorizations and receipts of the UTC day and
// the time-to-receive percentiles of the receipts with a projected
// authorization (nil without a sample).
type DayRow struct {
	Day        time.Time
	Authorized int
	Received   int
	Samples    int
	P50        *float64
	P95        *float64
}

// ReturnsPerformance merges the per-day authorization counts and receipt
// aggregates into one row per day that has either, ordered by day. The store
// yields at most one entry per day in each input. Nil inputs yield an empty,
// non-nil slice.
func ReturnsPerformance(authorized []AuthorizedDay, received []ReceivedDay) []DayRow {
	byDay := map[time.Time]*DayRow{}
	row := func(d time.Time) *DayRow {
		k := DayOf(d)
		if byDay[k] == nil {
			byDay[k] = &DayRow{Day: k}
		}
		return byDay[k]
	}
	for _, a := range authorized {
		row(a.Day).Authorized = a.Count
	}
	for _, r := range received {
		d := row(r.Day)
		d.Received, d.Samples = r.Received, r.Samples
		d.P50, d.P95 = r.P50, r.P95
	}
	out := make([]DayRow, 0, len(byDay))
	for _, d := range byDay {
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b DayRow) int { return a.Day.Compare(b.Day) })
	return out
}

// ParetoRow is one entry of the reason Pareto: its count, its share of ALL the
// range's authorizations and the running sum of the shares up to and including
// this entry.
type ParetoRow struct {
	Reason          string
	Returns         int
	Share           float64
	CumulativeShare float64
}

// ReasonPareto ranks the reasons by count (descending, ties by reason
// ascending) and keeps the first ParetoLimit. Shares are over the total of ALL
// counts, so a truncated Pareto does not sum to 1. Nil input yields an empty,
// non-nil slice.
func ReasonPareto(counts []ReasonCount) []ParetoRow {
	total := 0
	for _, c := range counts {
		total += c.Count
	}
	sorted := slices.Clone(counts)
	slices.SortFunc(sorted, func(a, b ReasonCount) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return strings.Compare(a.Reason, b.Reason)
	})
	sorted = sorted[:min(len(sorted), ParetoLimit())]
	out := make([]ParetoRow, 0, len(sorted))
	running := 0
	for _, c := range sorted {
		running += c.Count
		out = append(out, ParetoRow{
			Reason: c.Reason, Returns: c.Count,
			Share: Rate(c.Count, total), CumulativeShare: Rate(running, total),
		})
	}
	return out
}

// Rate is num/den, or 0 when den is not positive (nothing to rate).
func Rate(num, den int) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// Percentile is the continuous percentile (linear interpolation between
// closest ranks, p in [0,1]) of values, the same definition as Postgres'
// percentile_cont. values need not be sorted; it is not modified. An empty
// input yields 0.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := p * float64(len(sorted)-1)
	lo := math.Floor(rank)
	hi := math.Ceil(rank)
	return sorted[int(lo)] + (sorted[int(hi)]-sorted[int(lo)])*(rank-lo)
}

// Freshness is the projection's freshness: AsOf is the CloudEvents time of the
// newest event applied and LagSeconds is now - AsOf. Both are nil while the
// projection has applied nothing.
type Freshness struct {
	AsOf       *time.Time
	LagSeconds *float64
}

// ComputeFreshness derives the lag from the newest applied event time. A clock
// that is behind the event (skew) reads as zero lag, never negative.
func ComputeFreshness(asOf *time.Time, now time.Time) Freshness {
	if asOf == nil {
		return Freshness{}
	}
	lag := math.Max(0, now.Sub(*asOf).Seconds())
	utc := asOf.UTC()
	return Freshness{AsOf: &utc, LagSeconds: &lag}
}
