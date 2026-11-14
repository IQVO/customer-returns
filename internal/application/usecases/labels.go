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
// publishes ReturnLabelIssued. While that route is not live the call answers
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
	group, err := s.d.Groups.Group(ctx, a.OrderID(), a.GroupNo())
	if err != nil {
		return views.ReturnAuthorization{}, ports.ErrLabelUnavailable
	}
	label, err := s.d.Labels.IssueReturnLabel(ctx, requests.IssueReturnLabel{
		ReturnAuthorizationID: a.ID(), OrderID: a.OrderID(), PackageNo: req.PackageNo,
		DestinationPostalPrefix: group.DestinationPostalPrefix,
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
	if _, err := s.expireLazily(ctx, a); err != nil {
		return true, err
	}
	return true, nil
}
