package analyticsstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

// Reader is the Postgres READER (returnsperformance.Reader). Every query
// filters the fact's business instant with `>= from AND < to`, and days are
// UTC calendar days. SQL only counts, groups and takes percentile_cont; the
// ranks, the Pareto ordering and bound, the shares and the day merge come from
// internal/domain/returnsperformance.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs a Reader over the (read-only) reports pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

var _ returnsperformance.Reader = (*Reader)(nil)

// authorizedSQL: $1 from, $2 to.
const authorizedSQL = `
	SELECT (authorized_at AT TIME ZONE 'UTC')::date AS day, count(*)
	FROM return_authorizations
	WHERE authorized_at >= $1 AND authorized_at < $2
	GROUP BY day
	ORDER BY day`

// AuthorizedPerDay implements returnsperformance.Reader.
func (r *Reader) AuthorizedPerDay(ctx context.Context, rg returnsperformance.Range) ([]returnsperformance.AuthorizedDay, error) {
	return collect(ctx, r.pool, authorizedSQL, func(rows pgx.Rows) (returnsperformance.AuthorizedDay, error) {
		var d returnsperformance.AuthorizedDay
		err := rows.Scan(&d.Day, &d.Count)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To)
}

// receivedSQL: the receipts of the range left-joined to their authorization
// (a receipt whose authorization is not projected counts as received but has
// no time to receive), then per day the receipts, the samples and the two
// percentiles of the non-negative seconds between the two instants.
//
//	$1 from, $2 to, $3 p50 rank, $4 p95 rank
const receivedSQL = `
	WITH per_receipt AS (
		SELECT (r.received_at AT TIME ZONE 'UTC')::date AS day,
		       CASE WHEN a.authorization_id IS NULL THEN NULL
		            ELSE GREATEST(0, EXTRACT(EPOCH FROM (r.received_at - a.authorized_at)))::float8 END AS seconds
		FROM return_receipts r
		LEFT JOIN return_authorizations a ON a.authorization_id = r.authorization_id
		WHERE r.received_at >= $1 AND r.received_at < $2
	)
	SELECT day, count(*), count(seconds),
	       percentile_cont($3::float8) WITHIN GROUP (ORDER BY seconds),
	       percentile_cont($4::float8) WITHIN GROUP (ORDER BY seconds)
	FROM per_receipt
	GROUP BY day
	ORDER BY day`

// ReceivedPerDay implements returnsperformance.Reader. percentile_cont ignores
// the NULL seconds of the receipts without an authorization and is NULL when
// the day has no sample.
func (r *Reader) ReceivedPerDay(ctx context.Context, rg returnsperformance.Range) ([]returnsperformance.ReceivedDay, error) {
	return collect(ctx, r.pool, receivedSQL, func(rows pgx.Rows) (returnsperformance.ReceivedDay, error) {
		var d returnsperformance.ReceivedDay
		err := rows.Scan(&d.Day, &d.Received, &d.Samples, &d.P50, &d.P95)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To, returnsperformance.TimeToReceiveP50(), returnsperformance.TimeToReceiveP95())
}

// reasonsSQL: $1 from, $2 to.
const reasonsSQL = `
	SELECT reason, count(*)
	FROM return_authorizations
	WHERE authorized_at >= $1 AND authorized_at < $2
	GROUP BY reason`

// ReasonCounts implements returnsperformance.Reader.
func (r *Reader) ReasonCounts(ctx context.Context, rg returnsperformance.Range) ([]returnsperformance.ReasonCount, error) {
	return collect(ctx, r.pool, reasonsSQL, func(rows pgx.Rows) (returnsperformance.ReasonCount, error) {
		var c returnsperformance.ReasonCount
		err := rows.Scan(&c.Reason, &c.Count)
		return c, err
	}, rg.From, rg.To)
}

// LastEventAt implements returnsperformance.Reader: the newest CloudEvents
// time among the applied events, nil while none was applied.
func (r *Reader) LastEventAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(occurred_at) FROM analytics_processed_events`).Scan(&at); err != nil {
		return nil, fmt.Errorf("analyticsstore: last event: %w", err)
	}
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at, nil
}

// Ping checks the analytical database answers (the reports' /readyz).
func (r *Reader) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

// collect runs query with args and scans every row; the result is a non-nil
// slice even when empty.
func collect[T any](ctx context.Context, pool *pgxpool.Pool, query string, scan func(pgx.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("analyticsstore: query: %w", err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("analyticsstore: scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analyticsstore: rows: %w", err)
	}
	return out, nil
}
