package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// OnOutcomeSubmitted projects the vendor-connectivity VendorReturnOutcome
// Submitted fact: the returns centre's graded outcome. Lines beyond the
// authorization are recorded on the outcome facts as unexpected and never
// restocked (the warehouse fleet's exception flow owns them). An outcome for an
// unknown or closed authorization is recorded and acknowledged -- never a
// consumer error. The first outcome that fits completes the authorization and
// publishes ReturnCompleted, the restock fact.
func (s *Service) OnOutcomeSubmitted(ctx context.Context, eventID string, e requests.OutcomeSubmitted) error {
	return s.consume(ctx, eventID, "VendorReturnOutcomeSubmitted", func(ctx context.Context) error {
		a, err := s.d.Authorizations.Get(ctx, e.AuthorizationID)
		if errors.Is(err, ports.ErrNotFound) {
			return s.d.Outcomes.Record(ctx, factOf(e, allUnexpected(e)))
		}
		if err != nil {
			return err
		}
		if err := s.expireLazily(ctx, a); err != nil {
			return err
		}
		known, unexpected := splitOutcome(a, e.Lines)
		open := a.State() == returnauthorization.StateAuthorized || a.State() == returnauthorization.StateInTransit
		if len(known) == 0 || !open {
			return s.d.Outcomes.Record(ctx, factOf(e, unexpected))
		}
		if err := a.ApplyOutcome(known, e.NodeID, e.ReceivedAt); err != nil {
			if errors.Is(err, shared.ErrValidation) {
				return s.recordUnexpected(ctx, e)
			}
			return err
		}
		s.d.Metrics.OutcomeApplied(ctx, len(unexpected))
		return s.commitOutcome(ctx, a, factOf(e, unexpected))
	})
}

// commitOutcome persists the applied authorization, its outcome fact and the
// outbox batch as one consume step.
func (s *Service) commitOutcome(ctx context.Context, a *returnauthorization.ReturnAuthorization, fact requests.OutcomeFact) error {
	events := a.Events()
	if err := s.d.Authorizations.Save(ctx, a); err != nil {
		return err
	}
	if err := s.d.Outcomes.Record(ctx, fact); err != nil {
		return err
	}
	return s.d.Outbox.Append(ctx, events...)
}

// splitOutcome divides the submitted lines into the ones this authorization
// covers (known) and the ones it never did (unexpected, kept for the facts).
func splitOutcome(a *returnauthorization.ReturnAuthorization, lines []requests.OutcomeLine) ([]returnauthorization.OutcomeLine, []requests.OutcomeFactLine) {
	authorized := make(map[string]int, len(a.Lines()))
	ownerOf := make(map[string]string, len(a.Lines()))
	for _, l := range a.Lines() {
		authorized[l.CatalogItemID] += l.Quantity
		if l.OwnerRef != "" {
			ownerOf[l.CatalogItemID] = l.OwnerRef
		}
	}
	var known []returnauthorization.OutcomeLine
	var unexpected []requests.OutcomeFactLine
	for _, l := range lines {
		total := l.QtyRestockable + l.QtyUnsellable
		factLine := requests.OutcomeFactLine{
			CatalogItemID: l.CatalogItemID, OwnerRef: l.OwnerRef,
			QtyRestockable: l.QtyRestockable, QtyUnsellable: l.QtyUnsellable,
		}
		if authorized[l.CatalogItemID] >= total && total > 0 {
			if l.OwnerRef == "" {
				factLine.OwnerRef = ownerOf[l.CatalogItemID]
			}
			known = append(known, returnauthorization.OutcomeLine{
				CatalogItemID: l.CatalogItemID, OwnerRef: factLine.OwnerRef,
				QtyRestockable: l.QtyRestockable, QtyUnsellable: l.QtyUnsellable,
			})
		} else {
			factLine.Unexpected = true
			unexpected = append(unexpected, factLine)
		}
	}
	return known, unexpected
}

func allUnexpected(e requests.OutcomeSubmitted) []requests.OutcomeFactLine {
	out := make([]requests.OutcomeFactLine, 0, len(e.Lines))
	for _, l := range e.Lines {
		out = append(out, requests.OutcomeFactLine{
			CatalogItemID: l.CatalogItemID, OwnerRef: l.OwnerRef,
			QtyRestockable: l.QtyRestockable, QtyUnsellable: l.QtyUnsellable,
			Unexpected: true,
		})
	}
	return out
}

func factOf(e requests.OutcomeSubmitted, unexpected []requests.OutcomeFactLine) requests.OutcomeFact {
	known, rest := splitFactLines(e, unexpected)
	return requests.OutcomeFact{
		AuthorizationID: e.AuthorizationID, NodeID: e.NodeID, ReceivedAt: e.ReceivedAt,
		Lines: append(known, rest...),
	}
}

func splitFactLines(e requests.OutcomeSubmitted, unexpected []requests.OutcomeFactLine) ([]requests.OutcomeFactLine, []requests.OutcomeFactLine) {
	flags := make(map[string]bool, len(unexpected))
	for _, u := range unexpected {
		flags[u.CatalogItemID] = true
	}
	var known []requests.OutcomeFactLine
	for _, l := range e.Lines {
		if flags[l.CatalogItemID] {
			continue
		}
		known = append(known, requests.OutcomeFactLine{
			CatalogItemID: l.CatalogItemID, OwnerRef: l.OwnerRef,
			QtyRestockable: l.QtyRestockable, QtyUnsellable: l.QtyUnsellable,
		})
	}
	return known, unexpected
}

// recordUnexpected stores the outcome as an all-unexpected fact (the
// authorization refused the lines).
func (s *Service) recordUnexpected(ctx context.Context, e requests.OutcomeSubmitted) error {
	return s.d.Outcomes.Record(ctx, factOf(e, allUnexpected(e)))
}
