package kafka

import (
	"context"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
)

// releaseLineWire is one line of a release. AssignmentID is present only on a
// merged release, where it names the member group the line belongs to.
type releaseLineWire struct {
	CatalogItemID string `json:"catalogItemId"`
	OwnerRef      string `json:"ownerRef,omitempty"`
	AssignmentID  string `json:"assignmentId,omitempty"`
}

// releaseMemberWire is one merged group of a merged release.
type releaseMemberWire struct {
	AssignmentID string `json:"assignmentId"`
	OrderID      string `json:"orderId"`
	GroupNo      int    `json:"groupNo"`
}

// originWire is the part of FulfillmentReleasedToNode and FulfillmentLocked
// this context reads. Lines and Members are the release's; a lock carries
// neither. Members is present only on a MERGED release (one purchase order
// for several order groups): orderId and groupNo then name the carrier group
// only, and every merged group is listed in Members.
type originWire struct {
	OrderID string              `json:"orderId"`
	GroupNo int                 `json:"groupNo"`
	NodeID  string              `json:"nodeId"`
	Lines   []releaseLineWire   `json:"lines,omitempty"`
	Members []releaseMemberWire `json:"members,omitempty"`
}

func handleOriginFact(svc *usecases.Service, locked bool, eventType string) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		wire, err := decodeData[originWire](e)
		if err != nil {
			return err
		}
		if wire.OrderID == "" || wire.GroupNo < 1 {
			return errBadPayload
		}
		return svc.OnOriginFact(ctx, eventID(e), eventType, originFacts(wire, locked)...)
	}
}

// originFacts says what the event tells about each order group it names: one
// fact for the event's own group, or one per member of a merged release (each
// with the owners of ITS lines; the node is the release's for all of them).
func originFacts(wire originWire, locked bool) []requests.OriginFact {
	facts := make([]requests.OriginFact, 0, len(wire.Members))
	for _, m := range wire.Members {
		if m.OrderID == "" || m.GroupNo < 1 || m.AssignmentID == "" {
			continue
		}
		facts = append(facts, requests.OriginFact{
			OrderID: m.OrderID, GroupNo: m.GroupNo, NodeID: wire.NodeID,
			Locked: locked, Owners: ownersOf(wire.Lines, m.AssignmentID),
		})
	}
	if len(facts) > 0 {
		return facts
	}
	return []requests.OriginFact{{
		OrderID: wire.OrderID, GroupNo: wire.GroupNo, NodeID: wire.NodeID,
		Locked: locked, Owners: ownersOf(wire.Lines, ""),
	}}
}

// ownersOf maps catalog item to owner for the lines of one member
// (assignmentID), or for every line when assignmentID is empty.
func ownersOf(lines []releaseLineWire, assignmentID string) map[string]string {
	owners := make(map[string]string, len(lines))
	for _, l := range lines {
		if l.OwnerRef == "" || (assignmentID != "" && l.AssignmentID != assignmentID) {
			continue
		}
		owners[l.CatalogItemID] = l.OwnerRef
	}
	return owners
}
