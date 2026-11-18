package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// IssueLabel asks the shipping context for the shopper's reverse label (the
// ONE in-org call of this context, ADR 0005), links the tracking number and
// publishes ReturnLabelIssued. The call names the return's destination by the
// label prefix of the destination node. While that route is not live, or the
// destination node's prefix is not known here, the call answers
// 422 label-unavailable: a tolerant failure, never a 500. The HTTP call runs
// OUTSIDE the unit of work; the link itself is one atomic save+append.
func (s *Service) IssueLabel(ctx context.Context, req requests.IssueLabel) (views.ReturnAuthorization, error) {
	if req.PackageNo < 1 || req.PackageNo > returnauthorization.MaxPackagesPerReturn {
		return views.ReturnAuthorization{}, shared.Invalid("packageNo is outside 1..%d", returnauthorization.MaxPackagesPerReturn)
	}
	a, err := s.d.Authorizations.Get(ctx, req.AuthorizationID)
	if err != nil {
		return views.ReturnAuthorization{}, err
	}
	if expired, err := s.expireAndReject(ctx, a); expired || err != nil {
		return views.FromAuthorization(a), errors.Join(ErrAuthorizationExpired, err)
	}
	if err := s.checkLabelable(a); err != nil {
		return views.ReturnAuthorization{}, err
	}
	prefix, err := s.labelDestinationPrefix(ctx, a)
	if err != nil {
		return views.ReturnAuthorization{}, err
	}
	label, err := s.d.Labels.IssueReturnLabel(ctx, requests.IssueReturnLabel{
		ReturnAuthorizationID: a.ID(), OrderID: a.OrderID(), PackageNo: req.PackageNo,
		DestinationPostalPrefix: prefix,
	})
	if err != nil {
		return views.ReturnAuthorization{}, err
	}
	var out views.ReturnAuthorization
	err = s.atomically(ctx, func(ctx context.Context) error {
		fresh, err := s.d.Authorizations.Get(ctx, req.AuthorizationID)
		if err != nil {
			return err
		}
		if err := fresh.LinkLabel(label.TrackingNumber, s.d.Clock.Now()); err != nil {
			return err
		}
		events := fresh.Events()
		if err := s.d.Authorizations.Save(ctx, fresh); err != nil {
			return err
		}
		if err := s.d.Outbox.Append(ctx, events...); err != nil {
			return err
		}
		out = views.FromAuthorization(fresh)
		return nil
	})
	return out, err
}

// labelDestinationPrefix answers the destination prefix of the reverse label:
// the label prefix of the node the goods go back to (a.DestinationNodeID), as
// the node read model holds it from the node's NodeRegistered. It is NOT the
// order group's prefix: that one is the outbound destination, i.e. where the
// shopper lives, and the label route asks for the RETURN destination, which
// only this context knows (ADR 0005, addendum 2026-11-17). A destination node
// the view does not hold, or holds without a label prefix, is the tolerant
// label-unavailable: the route is never called without its destination.
func (s *Service) labelDestinationPrefix(ctx context.Context, a *returnauthorization.ReturnAuthorization) (string, error) {
	node, err := s.d.Network.Node(ctx, a.DestinationNodeID())
	if err != nil || node.LabelPostalPrefix == "" {
		return "", ports.ErrLabelUnavailable
	}
	return node.LabelPostalPrefix, nil
}

func (s *Service) checkLabelable(a *returnauthorization.ReturnAuthorization) error {
	if a.State() != returnauthorization.StateAuthorized {
		return shared.Illegal("%s: label only while AUTHORIZED", a.State())
	}
	if a.Disposition() == returnauthorization.KeepDisposition {
		return shared.Illegal("KEEP links no label")
	}
	return nil
}

// expireAndReject lazily cancels an expired authorization and reports that the
// command must not proceed (the nil error carries no extra cause).
func (s *Service) expireAndReject(ctx context.Context, a *returnauthorization.ReturnAuthorization) (bool, error) {
	if !a.Expired(s.d.Clock.Now()) {
		return false, nil
	}
	if err := s.expireLazily(ctx, a); err != nil {
		return true, err
	}
	return true, nil
}
