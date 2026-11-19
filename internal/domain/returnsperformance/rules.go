package returnsperformance

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// The KPI rules of ADR 0012. They are functions, not package constants, so
// mutation testing reaches them and a caller cannot change them.

// TimeToReceiveP50 and TimeToReceiveP95 are the continuous-percentile ranks of
// the time-to-receive distribution.
func TimeToReceiveP50() float64 { return 0.5 }

// TimeToReceiveP95 is the 95th-percentile rank.
func TimeToReceiveP95() float64 { return 0.95 }

// ParetoLimit bounds the reason Pareto: at most this many entries.
func ParetoLimit() int { return 5 }

// Seconds is the time to receive in seconds, never negative: a receipt that the
// vendor dated before its authorization (clock skew) counts as zero. math.Max,
// not `if d < 0`: both yield 0 at the boundary, which would leave an equivalent
// `<` -> `<=` mutant.
func Seconds(authorizedAt, receivedAt time.Time) float64 {
	return math.Max(0, receivedAt.Sub(authorizedAt).Seconds())
}

// DayOf is the UTC calendar day (00:00 UTC) a business instant falls on.
func DayOf(t time.Time) time.Time { return t.UTC().Truncate(day()) }

// ErrInvalidEvent marks a fact that can never be projected: a required field is
// missing. It is deterministic, so the projector dead-letters it.
var ErrInvalidEvent = errors.New("returnsperformance: invalid analytics event")

// Validate checks the fields every fact and the fields its Kind needs.
func (e Event) Validate() error {
	missing := firstMissing(
		required{"id", e.EventID != ""},
		required{"time", !e.At.IsZero()},
		required{"authorizationId", e.AuthorizationID != ""},
		required{"the business instant", !e.OccurredAt.IsZero()},
	)
	if missing == "" {
		missing = e.kindMissing()
	}
	if missing != "" {
		return fmt.Errorf("%w: %s %s: %s is required", ErrInvalidEvent, e.Kind, e.EventID, missing)
	}
	return nil
}

// kindMissing names the first missing field of the Kind's own payload, or
// rejects a Kind the projection does not know.
func (e Event) kindMissing() string {
	switch e.Kind {
	case KindAuthorized:
		return firstMissing(required{"reason", e.Reason != ""})
	case KindCompleted:
		return ""
	default:
		return "a known kind"
	}
}

// required is one field check: name and whether it is present.
type required struct {
	name    string
	present bool
}

// firstMissing names the first absent field, or "" when every one is present.
func firstMissing(fields ...required) string {
	for _, f := range fields {
		if !f.present {
			return f.name
		}
	}
	return ""
}
