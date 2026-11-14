package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/views"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// AuthorizeReturn authorizes one order group's return: it checks the group is
// SHIPPED or DELIVERED (the OrderGroupView may lag -- the shopper retries with
// the same Idempotency-Key), resolves the lines against the placed group,
// decides the disposition with the read models' destinations and saves the
// aggregate with its ReturnAuthorized event.
func (s *Service) AuthorizeReturn(ctx context.Context, req requests.AuthorizeReturn) (views.ReturnAuthorization, error) {
	var out views.ReturnAuthorization
	err := s.atomically(ctx, func(ctx context.Context) error {
		group, err := s.d.Groups.Group(ctx, req.OrderID, req.GroupNo)
		if errors.Is(err, ports.ErrNotFound) {
			return ports.ErrGroupNotShipped
		}
		if err != nil {
			return err
		}
		if !group.Status.Authorizable() {
			return ports.ErrGroupNotShipped
		}
		lines, err := s.resolveLines(ctx, group, req)
		if err != nil {
			return err
		}
		origin, nearest := s.destinations(ctx, group)
		disposition, err := returnauthorization.Decide(returnauthorization.DecideInput{
			Lines: lines, Method: req.Method,
			NearestNodeID: nearest, OriginNodeID: originNode(origin),
		})
		if err != nil {
			return err
		}
		authorizedAt := s.d.Clock.Now()
		a, err := returnauthorization.Authorize(returnauthorization.NewAuthorization{
			ID:      returnauthorization.ID(s.d.IDs.NewID("ret")),
			OrderID: req.OrderID, GroupNo: req.GroupNo, Lines: lines,
			Reason: req.Reason, Disposition: disposition, Method: req.Method,
			DestinationNodeID: returnauthorization.DestinationFor(disposition, returnauthorization.DecideInput{
				NearestNodeID: nearest, OriginNodeID: originNode(origin),
			}),
			OriginNodeID:      originNode(origin),
			PickupWindowStart: req.PickupStart, PickupWindowEnd: req.PickupEnd,
			AuthorizedAt: authorizedAt,
		}, authorizedAt)
		if err != nil {
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
		s.d.Metrics.ReturnAuthorized(ctx, string(disposition))
		return nil
	})
	return out, err
}

// resolveLines matches the requested lineNos against the placed group and
// decorates them with the caller-declared prices and the release's ownerRefs.
func (s *Service) resolveLines(ctx context.Context, group network.OrderGroup, req requests.AuthorizeReturn) ([]returnauthorization.Line, error) {
	byNo := make(map[int]network.GroupLine, len(group.Lines))
	for _, l := range group.Lines {
		byNo[l.LineNo] = l
	}
	origin, _ := s.d.Network.Origin(ctx, group.OrderID, group.GroupNo)
	if len(origin.OwnersByItem) == 0 {
		origin = network.GroupOrigin{}
	}
	lines := make([]returnauthorization.Line, 0, len(req.Lines))
	for _, rl := range req.Lines {
		gl, ok := byNo[rl.LineNo]
		if !ok {
			return nil, ports.ErrUnknownLine
		}
		lines = append(lines, returnauthorization.Line{
			LineNo: rl.LineNo, CatalogItemID: gl.CatalogItemID,
			Quantity: gl.Quantity, UnitPriceCents: rl.UnitPriceCents,
			OwnerRef: origin.OwnerOf(gl.CatalogItemID),
		})
	}
	return lines, nil
}

// destinations answers the two destinations decide may use: the group's origin
// ("" until a lock/release fact was seen) and the region's returns centre (""
// when the region has none).
func (s *Service) destinations(ctx context.Context, group network.OrderGroup) (network.GroupOrigin, string) {
	origin, err := s.d.Network.Origin(ctx, group.OrderID, group.GroupNo)
	if errors.Is(err, ports.ErrNotFound) {
		origin = network.GroupOrigin{}
	}
	nearest := ""
	region, err := s.d.Network.RegionOfPrefix(ctx, group.DestinationPostalPrefix)
	if err == nil {
		if rc, err := s.d.Network.ReturnsCenterForRegion(ctx, region); err == nil {
			nearest = rc.NodeID
		}
	}
	return origin, nearest
}

func originNode(o network.GroupOrigin) string { return o.NodeID }
