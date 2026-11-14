package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/IQVO/customer-returns/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/IQVO/customer-returns"

// returnsAuthorizedName is the Tier 2 business metric of the context (ADR
// 0008, the fleet's standard metrics convention): every authorize attempt,
// by outcome.
const returnsAuthorizedName = "customer_returns.return.authorized"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeAuthorized = "authorized"
	outcomeRejected   = "rejected"
)

// ReturnMetrics implements ports.ReturnMetrics against the global
// MeterProvider. Until Setup installs a real provider the global one is a
// no-op, so recording is cheap and safe in tests and local runs.
type ReturnMetrics struct {
	authorized metric.Int64Counter
}

var _ ports.ReturnMetrics = (*ReturnMetrics)(nil)

// NewReturnMetrics registers the counters. It only fails if an instrument
// name is invalid, which is a programming error, not a runtime condition.
// Call it after Setup so the instruments bind to the real MeterProvider.
func NewReturnMetrics() (*ReturnMetrics, error) {
	authorized, err := otel.Meter(meterName).Int64Counter(
		returnsAuthorizedName,
		metric.WithDescription("Authorize attempts by outcome: authorized (disposition says which was decided: KEEP returnless, "+
			"NEAREST_RETURNS_CENTER or ORIGIN_NODE) or rejected (reason is the problem slug, such as group-not-shipped, "+
			"no-return-destination or validation-failed). A rising group-not-shipped means the OrderGroupView lags the "+
			"shopper; a rising no-return-destination means regions lack returns centres; a rising KEEP share near the "+
			"value threshold is expected returnless behaviour."),
		metric.WithUnit("{return}"),
	)
	if err != nil {
		return nil, err
	}
	return &ReturnMetrics{authorized: authorized}, nil
}

// ReturnAuthorized implements ports.ReturnMetrics.
func (m *ReturnMetrics) ReturnAuthorized(ctx context.Context, disposition string) {
	if m == nil || m.authorized == nil {
		return
	}
	m.authorized.Add(ctx, 1, metric.WithAttributes(
		outcomeKey.String(outcomeAuthorized),
		attribute.String("disposition", disposition),
	))
}

// ReturnRejected implements ports.ReturnMetrics.
func (m *ReturnMetrics) ReturnRejected(ctx context.Context, reason string) {
	if m == nil || m.authorized == nil {
		return
	}
	m.authorized.Add(ctx, 1, metric.WithAttributes(
		outcomeKey.String(outcomeRejected),
		reasonKey.String(reason),
	))
}

// OutcomeApplied implements ports.ReturnMetrics.
func (m *ReturnMetrics) OutcomeApplied(context.Context, int) {
	// Counted on the same counter's authorized side by the completion itself;
	// no separate instrument in v1.
}
