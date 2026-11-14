// Package usecases holds the application services: one method per command and
// query of the model and per REST operation, plus the consumers' policies.
// They orchestrate domain aggregates through ports; they perform no I/O of
// their own (the ONE exception is the reverse-label call, an in-org route,
// which the label use case makes OUTSIDE its unit of work). Every command runs
// in ONE unit of work that saves the aggregates and appends their events to
// the outbox (and, for a consumer, claims the CloudEvents id in the inbox).
package usecases

import (
	"context"
	"errors"
	"log/slog"

	"github.com/IQVO/customer-returns/internal/application/ports"
)

// Config tunes the service.
type Config struct {
	Logger *slog.Logger
}

// Deps are the out ports the service needs.
type Deps struct {
	UoW            ports.UnitOfWork
	Authorizations ports.AuthorizationRepository
	Groups         ports.GroupViewRepository
	Network        ports.NetworkViewRepository
	Outcomes       ports.OutcomeFacts
	Labels         ports.LabelIssuer
	Outbox         ports.EventOutbox
	Inbox          ports.ProcessedEvents
	Clock          ports.Clock
	IDs            ports.IDGenerator
	// Metrics is the Tier 2 business counter; nil is a no-op.
	Metrics ports.ReturnMetrics
}

// Service is the application service of customer-returns.
type Service struct {
	d   Deps
	cfg Config
}

// New builds the service, filling the Config defaults. A nil Metrics dep
// becomes a no-op (the composition root always sets the real counter).
func New(d Deps, cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if d.Metrics == nil {
		d.Metrics = noopMetrics{}
	}
	return &Service{d: d, cfg: cfg}
}

// noopMetrics counts nothing (tests that do not assert on counters).
type noopMetrics struct{}

// ReturnAuthorized implements ports.ReturnMetrics.
func (noopMetrics) ReturnAuthorized(context.Context, string) {}

// ReturnRejected implements ports.ReturnMetrics.
func (noopMetrics) ReturnRejected(context.Context, string) {}

// OutcomeApplied implements ports.ReturnMetrics.
func (noopMetrics) OutcomeApplied(context.Context, int) {}

// atomically runs fn in one unit of work and re-runs the whole unit once when
// a save lost an optimistic-concurrency race (the caller re-reads and retries).
func (s *Service) atomically(ctx context.Context, fn func(ctx context.Context) error) error {
	err := s.d.UoW.Do(ctx, fn)
	if errors.Is(err, ports.ErrConcurrentUpdate) {
		err = s.d.UoW.Do(ctx, fn)
	}
	return err
}

// consume is the inbox wrapper of every consumer policy: claim the event id,
// run fn, commit both together. A duplicate id is skipped.
func (s *Service) consume(ctx context.Context, eventID, eventType string, fn func(ctx context.Context) error) error {
	return s.atomically(ctx, func(ctx context.Context) error {
		first, err := s.d.Inbox.Claim(ctx, eventID, eventType, s.d.Clock.Now())
		if err != nil {
			return err
		}
		if !first {
			return nil
		}
		return fn(ctx)
	})
}
