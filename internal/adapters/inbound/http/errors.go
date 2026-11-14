package http

import (
	"errors"
	"net/http"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type, a malformed query value).
var errMalformedRequest = errors.New("request is not valid for this operation")

// problem is the fixed (status, slug, title) of one error category; the
// detail comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// Problems the middlewares write themselves, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid"}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
)

// problemCatalogue maps every typed error to its problem, in match order: the
// single REST error table of ADR 0002. The slug is the kebab-case of the Err
// name (the shared types are not-found, illegal-transition, concurrent-update
// and validation-failed).
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}},
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}},
	{ports.ErrUnknownLine, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}},
	{ports.ErrGroupNotShipped, problem{http.StatusUnprocessableEntity, "group-not-shipped", "The order group is not shipped or delivered yet; retry later with the same Idempotency-Key"}},
	{returnauthorization.ErrNoReturnDestination, problem{http.StatusUnprocessableEntity, "no-return-destination", "No returns centre serves the region and no origin node is known; retry later"}},
	{ports.ErrLabelUnavailable, problem{http.StatusUnprocessableEntity, "label-unavailable", "The reverse-label route is not available yet; retry later"}},
	{usecases.ErrAuthorizationExpired, problem{http.StatusUnprocessableEntity, "authorization-expired", "The authorization expired and was cancelled"}},
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Not found"}},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "Illegal transition"}},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; re-fetch it and retry"}},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
