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
				DestinationPostalPrefix: e.DestinationPostalPrefix,
				WitnessNodeID:           g.WitnessNodeID,
				Lines:                   append([]network.GroupLine(nil), g.Lines...),
				UpdatedAt:               s.d.Clock.Now(),
			}
			// Order tolerance: a notice fact may already have set a terminal
			// status (the two events have no ordering guarantee). The placed
			// lines, prefix and witness fill the row, but a fact already seen
			// is never demoted back to PROMISED.
			if held, err := s.d.Groups.Group(ctx, e.OrderID, g.GroupNo); err == nil && held.Status != network.GroupPromised {
				row.Status = held.Status
			} else if err != nil && !errors.Is(err, ports.ErrNotFound) {
				return err
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

// OnNodeRegistered projects NodeRegistered into the ReturnsNetworkView's node
// half: the fact that carries the node's type, region and postal code. Of the
// postal code only the label prefix is kept (the destination prefix of a
// reverse label addressed to the node, ADR 0005). A node never seen starts
// PENDING; a registration applied again (a rebuild of the view) keeps the
// lifecycle a status fact already set.
func (s *Service) OnNodeRegistered(ctx context.Context, eventID, eventType string, e requests.NodeRegistered) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		return s.d.Network.RegisterNode(ctx, network.Node{
			NodeID: e.NodeID, Type: e.NodeType, RegionID: e.RegionID,
			LabelPostalPrefix: network.LabelPostalPrefix(e.PostalCode),
		})
	})
}

// OnNodeStatus projects NodeActivated, NodeSuspended and NodeRetired: the
// facts name the node only, so they move the lifecycle and never touch what
// the registration wrote (only ACTIVE RETURNS_CENTERs are ever returned to).
// Order tolerance (ADR 0003): a status fact for a node this view never saw
// keeps a lifecycle-only row the registration completes, and a fact older than
// the one already applied is a no-op.
func (s *Service) OnNodeStatus(ctx context.Context, eventID, eventType string, e requests.NodeStatus) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		return s.d.Network.SetNodeLifecycle(ctx, e.NodeID, e.Lifecycle, e.At)
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
// ownerRefs (whose stock the lines took). One event may speak for several
// groups (a merged release names every member group): all of them are
// projected under the one inbox claim of the event.
func (s *Service) OnOriginFact(ctx context.Context, eventID, eventType string, facts ...requests.OriginFact) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		for _, e := range facts {
			if err := s.projectOrigin(ctx, e); err != nil {
				return err
			}
		}
		return nil
	})
}

// projectOrigin merges one group's origin fact into its row.
func (s *Service) projectOrigin(ctx context.Context, e requests.OriginFact) error {
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
}
