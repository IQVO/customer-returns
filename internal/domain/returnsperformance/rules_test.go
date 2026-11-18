package returnsperformance_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

func TestKPIRulesArePinned(t *testing.T) {
	if returnsperformance.TimeToReceiveP50() != 0.5 || returnsperformance.TimeToReceiveP95() != 0.95 {
		t.Fatalf("ranks = %v, %v; want 0.5, 0.95", returnsperformance.TimeToReceiveP50(), returnsperformance.TimeToReceiveP95())
	}
	if returnsperformance.ParetoLimit() != 5 {
		t.Fatalf("ParetoLimit = %d, want 5", returnsperformance.ParetoLimit())
	}
}

func TestSecondsIsTheNonNegativeDuration(t *testing.T) {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		to   time.Time
		want float64
	}{
		{"six days", at.Add(144 * time.Hour), 518400},
		{"same instant", at, 0},
		{"one second", at.Add(time.Second), 1},
		{"skewed clocks clamp to zero", at.Add(-90 * time.Second), 0},
		{"half a second", at.Add(500 * time.Millisecond), 0.5},
	}
	for _, c := range cases {
		if got := returnsperformance.Seconds(at, c.to); got != c.want {
			t.Errorf("%s: Seconds = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDayOfIsTheUTCDay(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	cases := []struct {
		in   time.Time
		want time.Time
	}{
		{time.Date(2026, 11, 14, 23, 59, 59, 0, time.UTC), utcDay(2026, 11, 14)},
		{time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), utcDay(2026, 11, 15)},
		{time.Date(2026, 11, 14, 22, 0, 0, 0, brt), utcDay(2026, 11, 15)}, // 01:00 UTC the next day
	}
	for _, c := range cases {
		if got := returnsperformance.DayOf(c.in); !got.Equal(c.want) || got.Location() != time.UTC {
			t.Errorf("DayOf(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func validEvent(kind returnsperformance.Kind) returnsperformance.Event {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	return returnsperformance.Event{Kind: kind, EventID: "e1", At: at, AuthorizationID: "ret-1", OccurredAt: at, Reason: "DEFECTIVE"}
}

func TestValidateAcceptsBothKinds(t *testing.T) {
	for _, k := range []returnsperformance.Kind{returnsperformance.KindAuthorized, returnsperformance.KindCompleted} {
		if err := validEvent(k).Validate(); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	completed := validEvent(returnsperformance.KindCompleted)
	completed.Reason = "" // a receipt carries no reason
	if err := completed.Validate(); err != nil {
		t.Errorf("a ReturnCompleted needs no reason: %v", err)
	}
}

func TestValidateNamesTheFirstMissingField(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*returnsperformance.Event)
		want   string
	}{
		{"id", func(e *returnsperformance.Event) { e.EventID = "" }, "id is required"},
		{"time", func(e *returnsperformance.Event) { e.At = time.Time{} }, "time is required"},
		{"authorizationId", func(e *returnsperformance.Event) { e.AuthorizationID = "" }, "authorizationId is required"},
		{"instant", func(e *returnsperformance.Event) { e.OccurredAt = time.Time{} }, "the business instant is required"},
		{"reason", func(e *returnsperformance.Event) { e.Reason = "" }, "reason is required"},
		{"kind", func(e *returnsperformance.Event) { e.Kind = "ReturnCancelled" }, "a known kind is required"},
	}
	for _, c := range cases {
		e := validEvent(returnsperformance.KindAuthorized)
		c.mutate(&e)
		err := e.Validate()
		if !errors.Is(err, returnsperformance.ErrInvalidEvent) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want ErrInvalidEvent containing %q", c.name, err, c.want)
		}
	}
}
