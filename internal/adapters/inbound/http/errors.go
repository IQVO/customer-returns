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

// problem is the fixed (status, slug, title, class) of one error category; the
// detail comes from the error at write time. class is the replay class the
// Idempotency-Key middleware reads (replay_class.go, ADR 0013): every problem
// literal of this package lives in this file and declares one.
type problem struct {
	status int
	slug   string
	title  string
	class  replayClass
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error", classRetryable}

// Problems the middlewares write themselves, before any use case runs. The
// three Idempotency-Key answers are a function of the request alone and are
// written outside the store, so nothing is ever kept under their key.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required", classFinal}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid", classFinal}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request", classFinal}
	// notFoundResource is the 404 of a request path that can never name a resource
	// (a NUL in the path); it shares its slug, so its class, with the not-found of
	// the catalogue.
	notFoundResource = problem{http.StatusNotFound, "not-found", "Not found", classRetryable}
	// dependencyUnavailable is the 503 of GET /readyz.
	dependencyUnavailable = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable", classRetryable}
)

// problemCatalogue maps every typed error to its problem, in match order: the
// single REST error table of ADR 0002. The slug is the kebab-case of the Err
// name (the shared types are not-found, illegal-transition, concurrent-update
// and validation-failed).
// validationFailed is the problem title of every validation refusal.
const (
	validationFailedTitle = "Validation failed"
	validationFailedSlug  = "validation-failed"
)

var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusUnprocessableEntity, validationFailedSlug, validationFailedTitle, classRetryable}},
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, validationFailedSlug, validationFailedTitle, classRetryable}},
	{ports.ErrUnknownLine, problem{http.StatusUnprocessableEntity, validationFailedSlug, validationFailedTitle, classRetryable}},
	{ports.ErrGroupNotShipped, problem{http.StatusUnprocessableEntity, "group-not-shipped", "The order group is not shipped or delivered yet; retry later with the same Idempotency-Key", classRetryable}},
	{returnauthorization.ErrNoReturnDestination, problem{http.StatusUnprocessableEntity, "no-return-destination", "No returns centre serves the region and no origin node is known; retry later", classRetryable}},
	{ports.ErrLabelUnavailable, problem{http.StatusUnprocessableEntity, "label-unavailable", "The reverse-label route is not available yet; retry later", classRetryable}},
	{usecases.ErrAuthorizationExpired, problem{http.StatusUnprocessableEntity, "authorization-expired", "The authorization expired and was cancelled", classOutcome}},
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Not found", classRetryable}},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "Illegal transition", classRetryable}},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; re-fetch it and retry", classRetryable}},
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
