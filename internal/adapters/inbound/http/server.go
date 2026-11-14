// Package http is the REST inbound adapter: the seller API of
// apis/openapi.yaml on a chi router, RFC 9457 problem documents for every
// error, and the liveness/readiness endpoints. It has no auth middleware
// (fleet decision; internal/architecture's TestNoAuthMiddlewareReintroduced):
// the seller is named in the request body and the aggregates check it is ACTIVE.
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/IQVO/customer-returns/internal/adapters/inbound/requestlog"
	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/usecases"
)

// Server holds the application service the handlers call.
type Server struct {
	Service *usecases.Service

	// Idempotency wraps every write (the fleet Idempotency-Key middleware,
	// built by cmd/ over the Postgres pool); the shipped binary always sets it,
	// nil (handler unit tests) leaves the routes unwrapped.
	Idempotency func(http.Handler) http.Handler
	// Readiness backs GET /readyz: the drain gate plus the Postgres and Kafka
	// checks (NewReadiness). The shipped binary always builds it with
	// NewReadiness; nil (handler unit tests) means always ready.
	Readiness *Readiness
	// Metrics is the Tier 2 business counter (nil in handler unit tests).
	Metrics ports.ReturnMetrics
}

// DefaultServiceName names the server spans and the HTTP metrics when the
// composition root passes no service name.
const DefaultServiceName = "customer-returns"

// NewRouter builds the chi router for every operation of apis/openapi.yaml. A
// nil logger defaults to slog.Default(); an empty serviceName to
// DefaultServiceName.
//
// Middleware order is the fleet observability standard: RequestID, then otelchi
// (the server span, named after the chi route pattern, so one span name per
// route and not per id), then the request-duration histogram, then
// RequestLogger (it runs inside the span, so its record carries trace_id and
// span_id), then Recoverer. telemetry.Setup must have run before this call:
// otelchimetric binds the global MeterProvider when it is constructed.
func NewRouter(s *Server, logger *slog.Logger, serviceName string) http.Handler {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	// A NUL (U+0000) in the path, the query or a body is a uniform client
	// error here, never a Postgres 22021 -> 500; it runs before the
	// idempotency middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	write := func(h http.HandlerFunc) http.Handler {
		if s.Idempotency == nil {
			return h
		}
		return s.Idempotency(h)
	}

	r.Method(http.MethodPost, "/return-authorizations", write(s.handleAuthorizeReturn))
	r.Get("/return-authorizations", s.handleListAuthorizations)
	r.Get("/return-authorizations/{authorizationId}", s.handleGetAuthorization)
	r.Method(http.MethodPost, "/return-authorizations/{authorizationId}/cancellations", write(s.handleCancelReturn))
	r.Method(http.MethodPost, "/return-authorizations/{authorizationId}/labels", write(s.handleIssueLabel))
	return r
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}
