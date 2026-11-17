package returnsperformance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

// now is mid-day, so "today" is 2026-11-08 whatever the hour.
var now = time.Date(2026, 11, 8, 15, 30, 0, 0, time.FixedZone("BRT", -3*3600))

func utcDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func mustRange(t *testing.T, from, to string) returnsperformance.Range {
	t.Helper()
	r, err := returnsperformance.ParseRange(from, to, now)
	if err != nil {
		t.Fatalf("ParseRange(%q, %q): %v", from, to, err)
	}
	return r
}

func TestParseRangeDefaultsToTheThirtyDaysEndingToday(t *testing.T) {
	r := mustRange(t, "", "")
	if !r.From.Equal(utcDay(2026, 10, 10)) || !r.To.Equal(utcDay(2026, 11, 9)) {
		t.Fatalf("range = [%s, %s), want [2026-10-10, 2026-11-09)", r.From, r.To)
	}
	if r.FirstDay() != "2026-10-10" || r.LastDay() != "2026-11-08" {
		t.Fatalf("days = %s..%s", r.FirstDay(), r.LastDay())
	}
	if returnsperformance.DefaultDays() != 30 || returnsperformance.MaxDays() != 366 {
		t.Fatalf("windows = %d, %d", returnsperformance.DefaultDays(), returnsperformance.MaxDays())
	}
}

func TestParseRangeOnlyToEndsTheDefaultWindowThere(t *testing.T) {
	r := mustRange(t, "", "2026-03-31")
	if r.FirstDay() != "2026-03-02" || r.LastDay() != "2026-03-31" {
		t.Fatalf("days = %s..%s, want 2026-03-02..2026-03-31", r.FirstDay(), r.LastDay())
	}
}

func TestParseRangeOnlyFromRunsToToday(t *testing.T) {
	r := mustRange(t, "2026-11-01", "")
	if !r.From.Equal(utcDay(2026, 11, 1)) || !r.To.Equal(utcDay(2026, 11, 9)) {
		t.Fatalf("range = [%s, %s)", r.From, r.To)
	}
}

func TestParseRangeAcceptsOneDay(t *testing.T) {
	r := mustRange(t, "2026-11-03", "2026-11-03")
	if !r.From.Equal(utcDay(2026, 11, 3)) || !r.To.Equal(utcDay(2026, 11, 4)) {
		t.Fatalf("range = [%s, %s), want the single day 2026-11-03", r.From, r.To)
	}
}

func TestParseRangeMaxDaysBoundary(t *testing.T) {
	// 2025-11-08 .. 2026-11-08 is 366 days inclusive; one more is refused.
	if _, err := returnsperformance.ParseRange("2025-11-08", "2026-11-08", now); err != nil {
		t.Fatalf("366 days must be accepted: %v", err)
	}
	if _, err := returnsperformance.ParseRange("2025-11-07", "2026-11-08", now); !errors.Is(err, returnsperformance.ErrRangeTooLarge) {
		t.Fatalf("367 days: err = %v, want ErrRangeTooLarge", err)
	}
}

func TestParseRangeRefusals(t *testing.T) {
	cases := []struct {
		from, to string
		want     error
	}{
		{"2026-11-04", "2026-11-03", returnsperformance.ErrInvertedRange},
		{"2026-11-3", "", returnsperformance.ErrInvalidFrom},
		{"2026-11-03T00:00:00Z", "", returnsperformance.ErrInvalidFrom},
		{"", "yesterday", returnsperformance.ErrInvalidTo},
	}
	for _, c := range cases {
		_, err := returnsperformance.ParseRange(c.from, c.to, now)
		if !errors.Is(err, c.want) {
			t.Errorf("ParseRange(%q, %q) err = %v, want %v", c.from, c.to, err, c.want)
		}
		if !returnsperformance.IsParameterError(err) {
			t.Errorf("ParseRange(%q, %q): IsParameterError = false", c.from, c.to)
		}
	}
	if returnsperformance.IsParameterError(errors.New("db down")) || returnsperformance.IsParameterError(nil) {
		t.Error("IsParameterError must only match the range errors")
	}
}

func TestFormatDayIsUTC(t *testing.T) {
	if got := returnsperformance.FormatDay(time.Date(2026, 11, 3, 23, 0, 0, 0, time.FixedZone("x", -3*3600))); got != "2026-11-04" {
		t.Fatalf("FormatDay = %s, want 2026-11-04", got)
	}
}
