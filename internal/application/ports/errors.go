package ports

import "errors"

var (
	// ErrNotFound is what every repository returns for a Get that matches
	// nothing (problem type not-found, 404). An adapter wraps its own miss into
	// this sentinel, so callers test errors.Is(err, ports.ErrNotFound).
	ErrNotFound = errors.New("not found")
	// ErrConcurrentUpdate is what Save returns when the stored version is not
	// the one the aggregate was loaded at (problem type concurrent-update, 409).
	ErrConcurrentUpdate = errors.New("concurrent update")
	// ErrLabelUnavailable is what LabelIssuer returns while the shipping
	// context's reverse-label route is not live (problem type
	// label-unavailable, 422): a tolerant failure, never a 500.
	ErrLabelUnavailable = errors.New("label unavailable")
	// ErrGroupNotShipped is what AuthorizeReturn returns when the order group
	// is not SHIPPED or DELIVERED yet, or the OrderGroupView has not seen it
	// (problem type group-not-shipped, 422; the shopper retries with the same
	// Idempotency-Key).
	ErrGroupNotShipped = errors.New("order group is not shipped or delivered")
	// ErrUnknownLine is what AuthorizeReturn returns for a lineNo the order
	// group does not carry (problem type validation-failed, 422).
	ErrUnknownLine = errors.New("line is not part of the order group")
)
