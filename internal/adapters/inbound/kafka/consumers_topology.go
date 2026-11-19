package kafka

import (
	"context"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

// nodeRegisteredWire is the part of node-topology's NodeRegistered this
// context reads: the ONE node fact that carries the node's type and region.
type nodeRegisteredWire struct {
	NodeID   string `json:"nodeId"`
	NodeType string `json:"nodeType"`
	RegionID string `json:"regionId"`
}

func handleNodeRegistered(svc *usecases.Service) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		wire, err := decodeData[nodeRegisteredWire](e)
		if err != nil {
			return err
		}
		if wire.NodeID == "" || wire.NodeType == "" {
			return errBadPayload
		}
		return svc.OnNodeRegistered(ctx, eventID(e), typeNodeRegistered, requests.NodeRegistered{
			NodeID: wire.NodeID, NodeType: network.NodeType(wire.NodeType), RegionID: wire.RegionID,
		})
	}
}

// nodeStatusWire is the part of NodeActivated, NodeSuspended and NodeRetired
// this context reads. node-topology publishes the node id and an instant on
// these three (the instant has a different name on each); it never repeats the
// node's type or region, so neither is read and neither is required.
type nodeStatusWire struct {
	NodeID string `json:"nodeId"`
}

// handleNodeStatus applies one status fact. The instant of the change is the
// CloudEvents `time` of the fact, which every producer sets to the domain
// occurred-at: it is the same attribute on all three types.
func handleNodeStatus(svc *usecases.Service, lifecycle network.NodeLifecycle, eventType string) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		wire, err := decodeData[nodeStatusWire](e)
		if err != nil {
			return err
		}
		if wire.NodeID == "" {
			return errBadPayload
		}
		return svc.OnNodeStatus(ctx, eventID(e), eventType, requests.NodeStatus{
			NodeID: wire.NodeID, Lifecycle: lifecycle, At: e.Time().UTC(),
		})
	}
}

type serviceAreaWire struct {
	PostalPrefix      string `json:"postalPrefix"`
	RegionID          string `json:"regionId"`
	AssignmentVersion int64  `json:"assignmentVersion"`
}

func handleServiceArea(svc *usecases.Service) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		wire, err := decodeData[serviceAreaWire](e)
		if err != nil {
			return err
		}
		if wire.PostalPrefix == "" || wire.RegionID == "" {
			return errBadPayload
		}
		return svc.OnPrefixFact(ctx, eventID(e), requests.PrefixFact{
			PostalPrefix: wire.PostalPrefix, RegionID: wire.RegionID,
			AssignmentVersion: wire.AssignmentVersion,
		})
	}
}
