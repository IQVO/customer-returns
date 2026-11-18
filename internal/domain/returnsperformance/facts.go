// Package returnsperformance is the self-contained read-model region of the
// returns analytics read side (ADR 0012): the facts the projector folds in,
// the shape of the returns-performance report, the KPI rules (percentile ranks,
// the reason Pareto and its bound, the day merge, the duration clamp), the
// date-range rules, the freshness lag, and the writer/reader ports the
// analytical store implements. It imports no other internal package (arch
// test): the reason vocabulary is never mirrored here (a code is just text),
// and the event names are hand-mirrored from apis/asyncapi.yaml, never imported
// from the ReturnAuthorization aggregate, so the OLTP model can never leak into
// the projection.
package returnsperformance

import (
	"context"
	"errors"
	"time"
)

// Kind is which of the two projected events a fact came from. The names are
// the CloudEvents event names (the last segment of `type`).
type Kind string

// The two projected events of retailnetwork.customer-returns.events.
const (
	KindAuthorized Kind = "ReturnAuthorized"
	KindCompleted  Kind = "ReturnCompleted"
)

// Event is one decoded analytics fact ready to project. It is the union of the
// two payloads; only the fields of its Kind are set.
type Event struct {
	Kind    Kind
	EventID string    // CloudEvents id: the idempotency key
	At      time.Time // CloudEvents time: when the use case raised it (freshness)

	// AuthorizationID is the aggregate every fact is about (the CloudEvents
	// subject and the payload's authorizationId).
	AuthorizationID string
	// OccurredAt is the business instant of the fact: `authorizedAt` of a
	// ReturnAuthorized, `receivedAt` of a ReturnCompleted. The report buckets
	// days on it.
	OccurredAt time.Time

	// Reason is the enum return reason code of a ReturnAuthorized.
	Reason string
}

// Projection is the WRITER port: Apply records the event id and projects the
// fact in ONE transaction. applied is false when the id was already recorded
// (a replay): nothing changed. An error wrapping ErrRejected is deterministic
// (the store can never accept this event: retry is pointless); any other error
// is transient and the same event may be retried.
type Projection interface {
	Apply(ctx context.Context, e Event) (applied bool, err error)
}

// ErrRejected marks an event the analytical store deterministically refuses
// (a data-exception or integrity-violation class error).
var ErrRejected = errors.New("returnsperformance: event rejected by the analytical store")

// Reader is the READER port. Every method answers for the half-open instant
// range [r.From, r.To) of the business instants, bucketed by UTC day. Results
// are never nil.
type Reader interface {
	// AuthorizedPerDay counts the authorizations of each day (ordered by day).
	AuthorizedPerDay(ctx context.Context, r Range) ([]AuthorizedDay, error)
	// ReceivedPerDay counts the receipts of each day and the time to receive
	// of those whose authorization is projected too (ordered by day).
	ReceivedPerDay(ctx context.Context, r Range) ([]ReceivedDay, error)
	// ReasonCounts counts the range's authorizations per reason code, in no
	// particular order.
	ReasonCounts(ctx context.Context, r Range) ([]ReasonCount, error)
	// LastEventAt is the CloudEvents time of the newest event the projection
	// applied (nil while it applied none): the basis of the freshness lag.
	LastEventAt(ctx context.Context) (*time.Time, error)
}

// AuthorizedDay is what the store counts for one UTC day of authorizedAt.
type AuthorizedDay struct {
	Day   time.Time
	Count int
}

// ReceivedDay is what the store aggregates for one UTC day of receivedAt: the
// receipts, how many of them have their authorization projected (Samples) and
// the percentiles of their time to receive in seconds (nil without a sample).
type ReceivedDay struct {
	Day      time.Time
	Received int
	Samples  int
	P50      *float64
	P95      *float64
}

// ReasonCount is the number of authorizations of the range with one reason.
type ReasonCount struct {
	Reason string
	Count  int
}
