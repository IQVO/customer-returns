package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/customer-returns/internal/application/ports"
)

// ProcessedEventRepo is the Postgres ports.ProcessedEvents (the inbox).
type ProcessedEventRepo struct{ pool *pgxpool.Pool }

// NewProcessedEventRepo constructs a ProcessedEventRepo over pool.
func NewProcessedEventRepo(pool *pgxpool.Pool) *ProcessedEventRepo {
	return &ProcessedEventRepo{pool: pool}
}

var _ ports.ProcessedEvents = (*ProcessedEventRepo)(nil)

// Claim records the CloudEvents id and reports whether it was new. Inside a
// unit of work the INSERT shares the transaction of the event's effects, so a
// rollback un-claims it. A concurrent claim of the same id blocks on the
// primary key until the other transaction ends.
func (r *ProcessedEventRepo) Claim(ctx context.Context, eventID, eventType string, at time.Time) (bool, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO processed_events (event_id, event_type, processed_at) VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING`, eventID, eventType, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
