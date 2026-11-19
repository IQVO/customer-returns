package kafka

import (
	"context"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// The consumed contracts (ADR 0003): every type below is another context's
// published integration event; CR projects each into a local read model and
// never calls the producer back.
const (
	topicCustomerOrdering   = "retailnetwork.customer-ordering.events"
	topicNodeTopology       = "retailnetwork.node-topology.events"
	topicOrderSourcing      = "retailnetwork.order-sourcing.events"
	topicVendorConnectivity = "retailnetwork.vendor-connectivity.events"

	typeCustomerOrderPlaced  = "com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced"
	typeShopperNoticeDecided = "com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided"
	typeNodeRegistered       = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"
	typeNodeActivated        = "com.retailnetwork.logistics.node-topology.node.NodeActivated"
	typeNodeSuspended        = "com.retailnetwork.logistics.node-topology.node.NodeSuspended"
	typeNodeRetired          = "com.retailnetwork.logistics.node-topology.node.NodeRetired"
	typeServiceAreaAssigned  = "com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned"
	typeFulfillmentReleased  = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode"
	typeFulfillmentLocked    = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked"
	typeVendorOutcome        = "com.retailnetwork.partner.vendor-connectivity.returnoutcome.VendorReturnOutcomeSubmitted"
)

// NewOrderConsumer consumes the customer-ordering facts of the OrderGroupView
// (ADR 0003): the placed order and the terminal notice families.
func NewOrderConsumer(brokers []string, groupID string, svc *usecases.Service, logger *slog.Logger) *Consumer {
	return newConsumer("customer-ordering", brokers, topicCustomerOrdering, groupID, map[string]HandlerFunc{
		typeCustomerOrderPlaced:  handleOrderPlaced(svc),
		typeShopperNoticeDecided: handleNoticeDecided(svc),
	}, logger)
}

// NewTopologyConsumer consumes the node-topology facts of the
// ReturnsNetworkView: which returns centres exist and are ACTIVE, and which
// region a postal prefix belongs to.
func NewTopologyConsumer(brokers []string, groupID string, svc *usecases.Service, logger *slog.Logger) *Consumer {
	return newConsumer("node-topology", brokers, topicNodeTopology, groupID, map[string]HandlerFunc{
		typeNodeRegistered:      handleNodeRegistered(svc),
		typeNodeActivated:       handleNodeStatus(svc, network.NodeActive, typeNodeActivated),
		typeNodeSuspended:       handleNodeStatus(svc, network.NodeSuspended, typeNodeSuspended),
		typeNodeRetired:         handleNodeStatus(svc, network.NodeRetired, typeNodeRetired),
		typeServiceAreaAssigned: handleServiceArea(svc),
	}, logger)
}

// NewSourcingConsumer consumes the order-sourcing facts of the
// ReturnsNetworkView's origin half: the release names the per-item owners, the
// lock names the fulfilling node authoritatively.
func NewSourcingConsumer(brokers []string, groupID string, svc *usecases.Service, logger *slog.Logger) *Consumer {
	return newConsumer("order-sourcing", brokers, topicOrderSourcing, groupID, map[string]HandlerFunc{
		typeFulfillmentReleased: handleOriginFact(svc, false, typeFulfillmentReleased),
		typeFulfillmentLocked:   handleOriginFact(svc, true, typeFulfillmentLocked),
	}, logger)
}

// NewVendorConsumer consumes the vendors' graded outcomes (through the
// connectivity context's VendorReturnOutcomeSubmitted) and completes the
// authorization: the restock fact of the network.
func NewVendorConsumer(brokers []string, groupID string, svc *usecases.Service, logger *slog.Logger) *Consumer {
	return newConsumer("vendor-connectivity", brokers, topicVendorConnectivity, groupID, map[string]HandlerFunc{
		typeVendorOutcome: handleVendorOutcome(svc),
	}, logger)
}

func eventID(e ce.Event) string { return e.ID() }

type placedLineWire struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type placedGroupWire struct {
	GroupNo       int              `json:"groupNo"`
	WitnessNodeID string           `json:"witnessNodeId"`
	Lines         []placedLineWire `json:"lines"`
}

// orderPlacedWire is the part of CustomerOrderPlaced this context reads. The
// destination postal prefix is published once, on the order: the groups carry
// none.
type orderPlacedWire struct {
	OrderID                 string            `json:"orderId"`
	DestinationPostalPrefix string            `json:"destinationPostalPrefix"`
	PlacedAt                time.Time         `json:"placedAt"`
	Groups                  []placedGroupWire `json:"groups"`
}

func handleOrderPlaced(svc *usecases.Service) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		wire, err := decodeData[orderPlacedWire](e)
		if err != nil {
			return err
		}
		if wire.OrderID == "" || len(wire.Groups) == 0 {
			return errBadPayload
		}
		placed := requests.OrderPlaced{
			OrderID: wire.OrderID, DestinationPostalPrefix: wire.DestinationPostalPrefix, PlacedAt: wire.PlacedAt,
			Groups: make([]requests.PlacedGroup, 0, len(wire.Groups)),
		}
		for _, g := range wire.Groups {
			lines := make([]network.GroupLine, 0, len(g.Lines))
			for _, l := range g.Lines {
				lines = append(lines, network.GroupLine{LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity})
			}
			placed.Groups = append(placed.Groups, requests.PlacedGroup{
				GroupNo: g.GroupNo, WitnessNodeID: g.WitnessNodeID, Lines: lines,
			})
		}
		return svc.OnOrderPlaced(ctx, eventID(e), placed)
	}
}

type noticeWire struct {
	OrderID string `json:"orderId"`
	GroupNo int    `json:"groupNo"`
	Family  string `json:"family"`
	Fact    struct {
		Status string `json:"status"`
	} `json:"fact"`
}

func handleNoticeDecided(svc *usecases.Service) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		wire, err := decodeData[noticeWire](e)
		if err != nil {
			return err
		}
		if wire.OrderID == "" || wire.GroupNo < 1 || wire.Family == "" {
			return errBadPayload
		}
		return svc.OnNoticeDecided(ctx, eventID(e), requests.NoticeDecided{
			OrderID: wire.OrderID, GroupNo: wire.GroupNo, Family: wire.Family,
			Status: network.GroupStatus(wire.Fact.Status),
		})
	}
}

type outcomeLineWire struct {
	CatalogItemID  string `json:"catalogItemId"`
	OwnerRef       string `json:"ownerRef,omitempty"`
	QtyRestockable int    `json:"qtyRestockable"`
	QtyUnsellable  int    `json:"qtyUnsellable"`
}

type outcomeWire struct {
	AuthorizationID string            `json:"authorizationId"`
	NodeID          string            `json:"nodeId"`
	ReceivedAt      time.Time         `json:"receivedAt"`
	Lines           []outcomeLineWire `json:"lines"`
}

func handleVendorOutcome(svc *usecases.Service) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		wire, err := decodeData[outcomeWire](e)
		if err != nil {
			return err
		}
		if wire.AuthorizationID == "" || wire.NodeID == "" || len(wire.Lines) == 0 {
			return errBadPayload
		}
		outcome := requests.OutcomeSubmitted{
			AuthorizationID: returnauthorization.ID(wire.AuthorizationID),
			NodeID:          wire.NodeID, ReceivedAt: wire.ReceivedAt,
			Lines: make([]requests.OutcomeLine, 0, len(wire.Lines)),
		}
		for _, l := range wire.Lines {
			outcome.Lines = append(outcome.Lines, requests.OutcomeLine{
				CatalogItemID: l.CatalogItemID, OwnerRef: l.OwnerRef,
				QtyRestockable: l.QtyRestockable, QtyUnsellable: l.QtyUnsellable,
			})
		}
		return svc.OnOutcomeSubmitted(ctx, eventID(e), outcome)
	}
}
