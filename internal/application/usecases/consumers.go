package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

// OnOrderPlaced projects CustomerOrderPlaced into the OrderGroupView: one row
// per group, status PROMISED (the terminal families arrive as notice facts).
func (s *Service) OnOrderPlaced(ctx context.Context, eventID string, e requests.OrderPlaced) error {
	return s.consume(ctx, eventID, "CustomerOrderPlaced", func(ctx context.Context) error {
		for _, g := range e.Groups {
			row := network.OrderGroup{
				OrderID: e.OrderID, GroupNo: g.GroupNo, Status: network.GroupPromised,
				DestinationPostalPrefix: g.DestinationPostalPrefix,
				WitnessNodeID:           g.WitnessNodeID,
				Lines:                   append([]network.GroupLine(nil), g.Lines...),
				UpdatedAt:               s.d.Clock.Now(),
			}
			if err := s.d.Groups.SaveGroup(ctx, row); err != nil {
				return err
			}
		}
		return nil
	})
}

// noticeStatuses maps the terminal notice families to the group status they
// assert (the fact carries the status itself; the family is the fallback).
var noticeStatuses = map[string]network.GroupStatus{
	"GROUP_SHIPPED":   network.GroupShipped,
	"GROUP_DELIVERED": network.GroupDelivered,
	"GROUP_LOST":      network.GroupLost,
	"GROUP_RETURNED":  network.GroupReturned,
}

// OnNoticeDecided projects the terminal ShopperNoticeDecided families into the
// OrderGroupView: SHIPPED and DELIVERED make the group authorizable, LOST and
// RETURNED record the fact. A fact that carries a status wins over the family.
func (s *Service) OnNoticeDecided(ctx context.Context, eventID string, e requests.NoticeDecided) error {
	return s.consume(ctx, eventID, "ShopperNoticeDecided", func(ctx context.Context) error {
		status, ok := noticeStatuses[e.Family]
		if !ok {
			return nil
		}
		if e.Status != "" {
			status = e.Status
		}
		row, err := s.d.Groups.Group(ctx, e.OrderID, e.GroupNo)
		if errors.Is(err, ports.ErrNotFound) {
			row = network.OrderGroup{OrderID: e.OrderID, GroupNo: e.GroupNo, Lines: []network.GroupLine{}}
		} else if err != nil {
			return err
		}
		row.Status = status
		row.UpdatedAt = s.d.Clock.Now()
		return s.d.Groups.SaveGroup(ctx, row)
	})
}

// OnNodeFact projects the node lifecycle facts into the ReturnsNetworkView's
// node half: only ACTIVE RETURNS_CENTERs are ever returned to.
func (s *Service) OnNodeFact(ctx context.Context, eventID, eventType string, e requests.NodeFact) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		return s.d.Network.SaveNode(ctx, network.Node{
			NodeID: e.NodeID, Type: e.NodeType, RegionID: e.RegionID, Lifecycle: e.Lifecycle,
		})
	})
}

// OnPrefixFact projects ServiceAreaAssigned into the prefix half of the
// ReturnsNetworkView (the highest assignmentVersion wins).
func (s *Service) OnPrefixFact(ctx context.Context, eventID string, e requests.PrefixFact) error {
	return s.consume(ctx, eventID, "ServiceAreaAssigned", func(ctx context.Context) error {
		return s.d.Network.SavePrefix(ctx, network.PrefixAssignment{
			PostalPrefix: e.PostalPrefix, RegionID: e.RegionID, AssignmentVersion: e.AssignmentVersion,
		})
	})
}

// OnOriginFact projects FulfillmentReleasedToNode and FulfillmentLocked into
// the origin half of the ReturnsNetworkView: the lock names the fulfilling
// node authoritatively; the release additionally carries the per-item
// ownerRefs (whose stock the lines took).
func (s *Service) OnOriginFact(ctx context.Context, eventID, eventType string, e requests.OriginFact) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		row, err := s.d.Network.Origin(ctx, e.OrderID, e.GroupNo)
		if errors.Is(err, ports.ErrNotFound) {
			row = network.GroupOrigin{OrderID: e.OrderID, GroupNo: e.GroupNo}
		} else if err != nil {
			return err
		}
		if e.NodeID != "" {
			row.NodeID = e.NodeID
		}
		if len(e.Owners) > 0 && row.OwnersByItem == nil {
			row.OwnersByItem = map[string]string{}
		}
		for item, owner := range e.Owners {
			if owner != "" {
				row.OwnersByItem[item] = owner
			}
		}
		row.UpdatedAt = s.d.Clock.Now()
		return s.d.Network.SaveOrigin(ctx, row)
	})
}
