package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// uniqueViolation is the SQLSTATE of a duplicate key.
const uniqueViolation = "23505"

// saveErr maps a failed INSERT: a duplicate key is a lost create race.
func saveErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == uniqueViolation {
		return ports.ErrConcurrentUpdate
	}
	return err
}

func updated(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func zeroIfNil(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// AuthorizationRepo is the Postgres ports.AuthorizationRepository.
type AuthorizationRepo struct{ pool *pgxpool.Pool }

// NewAuthorizationRepo builds the repository.
func NewAuthorizationRepo(pool *pgxpool.Pool) *AuthorizationRepo {
	return &AuthorizationRepo{pool: pool}
}

var _ ports.AuthorizationRepository = (*AuthorizationRepo)(nil)

const authorizationColumns = `authorization_id, order_id, group_no, lines, reason, disposition, method,
destination_node_id, origin_node_id, pickup_window_start, pickup_window_end, tracking_numbers,
state, authorized_at, expires_at, closed_at, version`

// Get returns the authorization, or ports.ErrNotFound.
func (r *AuthorizationRepo) Get(ctx context.Context, id returnauthorization.ID) (*returnauthorization.ReturnAuthorization, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT `+authorizationColumns+` FROM return_authorizations WHERE authorization_id = $1`, string(id))
	snap, err := scanAuthorization(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get return authorization: %w", err)
	}
	return returnauthorization.Restore(snap)
}

// Save persists the aggregate under the version rule of the port's comment:
// an UPDATE whose version expectation fails, or a duplicate create, is
// ports.ErrConcurrentUpdate.
func (r *AuthorizationRepo) Save(ctx context.Context, a *returnauthorization.ReturnAuthorization) error {
	s := a.Snapshot()
	lines, err := json.Marshal(s.Lines)
	if err != nil {
		return fmt.Errorf("encode lines: %w", err)
	}
	tracking, err := json.Marshal(s.TrackingNumbers)
	if err != nil {
		return fmt.Errorf("encode tracking numbers: %w", err)
	}
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		tag, err := q.Exec(ctx, `
INSERT INTO return_authorizations (authorization_id, order_id, group_no, lines, reason, disposition, method,
destination_node_id, origin_node_id, pickup_window_start, pickup_window_end, tracking_numbers,
state, authorized_at, expires_at, closed_at, version)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
			string(s.ID), s.OrderID, s.GroupNo, lines, string(s.Reason), string(s.Disposition), string(s.Method),
			s.DestinationNodeID, s.OriginNodeID, nullTime(s.PickupWindowStart), nullTime(s.PickupWindowEnd), tracking,
			string(s.State), s.AuthorizedAt, s.ExpiresAt, nullTime(s.ClosedAt), s.Version)
		return saveErr(updated(tag, err))
	}
	tag, err := q.Exec(ctx, `
UPDATE return_authorizations SET lines=$3, tracking_numbers=$4, state=$5, closed_at=$6, version=$7
WHERE authorization_id=$1 AND version=$2`,
		string(s.ID), s.Version-1, lines, tracking, string(s.State), nullTime(s.ClosedAt), s.Version)
	return updated(tag, err)
}

// ListByOrder returns the order's authorizations, newest first, keyset-paged
// after the (authorizedAt, authorizationId) row the caller last saw.
func (r *AuthorizationRepo) ListByOrder(ctx context.Context, orderID string, afterCreatedAt time.Time, afterID returnauthorization.ID, limit int) ([]*returnauthorization.ReturnAuthorization, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
SELECT `+authorizationColumns+` FROM return_authorizations
WHERE order_id = $1
  AND ($2::timestamptz IS NULL OR (authorized_at, authorization_id) < ($2, $3))
ORDER BY authorized_at DESC, authorization_id DESC
LIMIT $4`, orderID, nullTime(afterCreatedAt), string(afterID), limit)
	if err != nil {
		return nil, fmt.Errorf("list return authorizations: %w", err)
	}
	defer rows.Close()
	out := []*returnauthorization.ReturnAuthorization{}
	for rows.Next() {
		snap, err := scanAuthorization(rows)
		if err != nil {
			return nil, fmt.Errorf("scan return authorization: %w", err)
		}
		a, err := returnauthorization.Restore(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanAuthorization(row rowScanner) (returnauthorization.Snapshot, error) {
	var (
		snap         returnauthorization.Snapshot
		id, orderID  string
		groupNo      int
		lines        []byte
		reason       string
		disposition  string
		method       string
		destination  string
		origin       string
		pickupStart  *time.Time
		pickupEnd    *time.Time
		tracking     []byte
		state        string
		authorizedAt time.Time
		expiresAt    time.Time
		closedAt     *time.Time
		version      int64
	)
	err := row.Scan(&id, &orderID, &groupNo, &lines, &reason, &disposition, &method,
		&destination, &origin, &pickupStart, &pickupEnd, &tracking, &state,
		&authorizedAt, &expiresAt, &closedAt, &version)
	if err != nil {
		return snap, err
	}
	snap = returnauthorization.Snapshot{
		ID: returnauthorization.ID(id), OrderID: orderID, GroupNo: groupNo,
		Reason: returnauthorization.Reason(reason), Disposition: returnauthorization.Disposition(disposition),
		Method: returnauthorization.Method(method), DestinationNodeID: destination, OriginNodeID: origin,
		PickupWindowStart: zeroIfNil(pickupStart), PickupWindowEnd: zeroIfNil(pickupEnd),
		State: returnauthorization.State(state), AuthorizedAt: authorizedAt.UTC(), ExpiresAt: expiresAt.UTC(),
		ClosedAt: zeroIfNil(closedAt), Version: version,
	}
	if err := json.Unmarshal(lines, &snap.Lines); err != nil {
		return snap, fmt.Errorf("decode lines: %w", err)
	}
	if err := json.Unmarshal(tracking, &snap.TrackingNumbers); err != nil {
		return snap, fmt.Errorf("decode tracking numbers: %w", err)
	}
	if snap.TrackingNumbers == nil {
		snap.TrackingNumbers = []string{}
	}
	return snap, nil
}
