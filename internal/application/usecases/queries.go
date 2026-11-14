package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// DefaultListLimit is the page size of GET /return-authorizations when the
// caller sends none.
const DefaultListLimit = 20

// MaxListLimit is the largest accepted page size.
const MaxListLimit = 100

// GetReturnAuthorization answers one authorization by id (the lazy-expiry
// reporter: an AUTHORIZATION past its expiresAt is reported with its remaining
// state; only a command's touch cancels it).
func (s *Service) GetReturnAuthorization(ctx context.Context, id returnauthorization.ID) (views.ReturnAuthorization, error) {
	a, err := s.d.Authorizations.Get(ctx, id)
	if err != nil {
		return views.ReturnAuthorization{}, err
	}
	return views.FromAuthorization(a), nil
}

// ListReturnAuthorizations answers the order's authorizations, newest first,
// keyset-paged after the cursor the caller last saw ("" is page one).
func (s *Service) ListReturnAuthorizations(ctx context.Context, req requests.ListAuthorizations) (views.AuthorizationPage, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	afterAt, afterID, err := decodeCursor(req.AfterCreatedAt, req.AfterID)
	if err != nil {
		return views.AuthorizationPage{}, err
	}
	items, err := s.d.Authorizations.ListByOrder(ctx, req.OrderID, afterAt, afterID, limit+1)
	if err != nil {
		return views.AuthorizationPage{}, err
	}
	page := views.AuthorizationPage{Items: make([]views.ReturnAuthorization, 0, len(items))}
	for i, a := range items {
		if i == limit {
			last := items[limit-1]
			page.NextCursor = encodeCursor(last.AuthorizedAt(), string(last.ID()))
			break
		}
		page.Items = append(page.Items, views.FromAuthorization(a))
	}
	return page, nil
}

func decodeCursor(at time.Time, id returnauthorization.ID) (time.Time, returnauthorization.ID, error) {
	if at.IsZero() {
		return time.Time{}, "", nil
	}
	if id == "" {
		return time.Time{}, "", ports.ErrUnknownLine
	}
	return at, id, nil
}

func encodeCursor(at time.Time, id string) string {
	return fmt.Sprintf("%s|%s", at.UTC().Format(time.RFC3339Nano), id)
}

// CancelReturn cancels an authorization the shopper no longer wants (before
// IN_TRANSIT). An authorization whose TTL passed is cancelled as EXPIRED: the
// lazy expiry IS the cancellation.
func (s *Service) CancelReturn(ctx context.Context, req requests.CancelReturn) (views.ReturnAuthorization, error) {
	var out views.ReturnAuthorization
	err := s.atomically(ctx, func(ctx context.Context) error {
		a, err := s.d.Authorizations.Get(ctx, req.AuthorizationID)
		if err != nil {
			return err
		}
		now := s.d.Clock.Now()
		reason := returnauthorization.CancelShopperRequested
		if a.Expired(now) {
			reason = returnauthorization.CancelExpired
		}
		if err := a.Cancel(reason, now); err != nil {
			return err
		}
		events := a.Events()
		if err := s.d.Authorizations.Save(ctx, a); err != nil {
			return err
		}
		if err := s.d.Outbox.Append(ctx, events...); err != nil {
			return err
		}
		out = views.FromAuthorization(a)
		return nil
	})
	return out, err
}

// expireLazily cancels an expired AUTHORIZED authorization in place (ADR 0002:
// no sweeper; the next touch is the expiry) and reports whether it did.
func (s *Service) expireLazily(ctx context.Context, a *returnauthorization.ReturnAuthorization) (bool, error) {
	now := s.d.Clock.Now()
	if !a.Expired(now) {
		return false, nil
	}
	if err := a.Cancel(returnauthorization.CancelExpired, now); err != nil {
		return false, err
	}
	events := a.Events()
	if err := s.d.Authorizations.Save(ctx, a); err != nil {
		return false, err
	}
	if err := s.d.Outbox.Append(ctx, events...); err != nil {
		return false, err
	}
	return true, nil
}

// ErrAuthorizationExpired is what the label use case returns when the lazy
// expiry just closed the authorization (problem type authorization-expired,
// 422).
var ErrAuthorizationExpired = errors.New("authorization expired")
