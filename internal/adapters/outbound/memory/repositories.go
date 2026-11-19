package memory

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// AuthorizationRepository is the memory double of the aggregate repository.
type AuthorizationRepository struct{ store *Store }

// NewAuthorizationRepository builds the double over the store.
func NewAuthorizationRepository(s *Store) *AuthorizationRepository {
	return &AuthorizationRepository{store: s}
}

// Get returns the authorization, or ports.ErrNotFound.
func (r *AuthorizationRepository) Get(ctx context.Context, id returnauthorization.ID) (*returnauthorization.ReturnAuthorization, error) {
	defer r.store.enter(ctx)()
	snap, ok := r.store.authorizations[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return returnauthorization.Restore(snap)
}

// Save persists under the version rule (create expects a free id).
func (r *AuthorizationRepository) Save(ctx context.Context, a *returnauthorization.ReturnAuthorization) error {
	defer r.store.enter(ctx)()
	snap := a.Snapshot()
	old, exists := r.store.authorizations[snap.ID]
	if exists && old.Version != snap.Version-1 {
		return ports.ErrConcurrentUpdate
	}
	if !exists && snap.Version != 1 {
		return ports.ErrConcurrentUpdate
	}
	r.store.authorizations[snap.ID] = snap
	return nil
}

// ListByOrder returns the order's authorizations newest first, keyset-paged.
func (r *AuthorizationRepository) ListByOrder(ctx context.Context, orderID string, afterCreatedAt time.Time, afterID returnauthorization.ID, limit int) ([]*returnauthorization.ReturnAuthorization, error) {
	defer r.store.enter(ctx)()
	type row struct {
		snap returnauthorization.Snapshot
	}
	rows := make([]row, 0, len(r.store.authorizations))
	for _, s := range r.store.authorizations {
		if s.OrderID != orderID {
			continue
		}
		rows = append(rows, row{s})
	}
	slices.SortFunc(rows, func(a, b row) int {
		if !a.snap.AuthorizedAt.Equal(b.snap.AuthorizedAt) {
			return b.snap.AuthorizedAt.Compare(a.snap.AuthorizedAt)
		}
		return strings.Compare(string(b.snap.ID), string(a.snap.ID))
	})
	out := make([]*returnauthorization.ReturnAuthorization, 0, limit)
	for _, rw := range rows {
		if !afterCreatedAt.IsZero() {
			if rw.snap.AuthorizedAt.After(afterCreatedAt) ||
				(rw.snap.AuthorizedAt.Equal(afterCreatedAt) && string(rw.snap.ID) <= string(afterID)) {
				continue
			}
		}
		if len(out) == limit {
			break
		}
		a, err := returnauthorization.Restore(rw.snap)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// GroupViewRepository is the memory double of the OrderGroupView.
type GroupViewRepository struct{ store *Store }

// NewGroupViewRepository builds the double over the store.
func NewGroupViewRepository(s *Store) *GroupViewRepository { return &GroupViewRepository{store: s} }

// Group returns the group row, or ports.ErrNotFound.
func (r *GroupViewRepository) Group(ctx context.Context, orderID string, groupNo int) (network.OrderGroup, error) {
	defer r.store.enter(ctx)()
	g, ok := r.store.groups[groupKey{orderID, groupNo}]
	if !ok {
		return network.OrderGroup{}, ports.ErrNotFound
	}
	return g, nil
}

// SaveGroup upserts the group row.
func (r *GroupViewRepository) SaveGroup(ctx context.Context, g network.OrderGroup) error {
	defer r.store.enter(ctx)()
	g.Lines = slices.Clone(g.Lines)
	r.store.groups[groupKey{g.OrderID, g.GroupNo}] = g
	return nil
}

// NetworkViewRepository is the memory double of the ReturnsNetworkView.
type NetworkViewRepository struct{ store *Store }

// NewNetworkViewRepository builds the double over the store.
func NewNetworkViewRepository(s *Store) *NetworkViewRepository {
	return &NetworkViewRepository{store: s}
}

// RegisterNode upserts the node's descriptive attributes; the lifecycle of a
// node already held is kept, a node never seen starts PENDING.
func (r *NetworkViewRepository) RegisterNode(ctx context.Context, n network.Node) error {
	defer r.store.enter(ctx)()
	held := r.store.nodes[n.NodeID]
	held.NodeID = n.NodeID
	r.store.nodes[n.NodeID] = held.WithRegistration(n.Type, n.RegionID)
	return nil
}

// SetNodeLifecycle moves only the lifecycle; a stale fact is a no-op and a
// node never seen gets a lifecycle-only row.
func (r *NetworkViewRepository) SetNodeLifecycle(ctx context.Context, nodeID string, lifecycle network.NodeLifecycle, at time.Time) error {
	defer r.store.enter(ctx)()
	held := r.store.nodes[nodeID]
	held.NodeID = nodeID
	if next, applies := held.WithStatus(lifecycle, at); applies {
		r.store.nodes[nodeID] = next
	}
	return nil
}

// Node returns the node row, or ports.ErrNotFound.
func (r *NetworkViewRepository) Node(ctx context.Context, nodeID string) (network.Node, error) {
	defer r.store.enter(ctx)()
	n, ok := r.store.nodes[nodeID]
	if !ok {
		return network.Node{}, ports.ErrNotFound
	}
	return n, nil
}

// SavePrefix upserts the prefix row; a stale assignmentVersion is a no-op.
func (r *NetworkViewRepository) SavePrefix(ctx context.Context, p network.PrefixAssignment) error {
	defer r.store.enter(ctx)()
	if old, ok := r.store.prefixes[p.PostalPrefix]; ok && old.AssignmentVersion >= p.AssignmentVersion {
		return nil
	}
	r.store.prefixes[p.PostalPrefix] = p
	return nil
}

// SaveOrigin upserts the group-origin row.
func (r *NetworkViewRepository) SaveOrigin(ctx context.Context, o network.GroupOrigin) error {
	defer r.store.enter(ctx)()
	o.OwnersByItem = mapsClone(o.OwnersByItem)
	r.store.origins[groupKey{o.OrderID, o.GroupNo}] = o
	return nil
}

// ReturnsCenterForRegion answers the region's ACTIVE returns centre with the
// lowest nodeId, or ports.ErrNotFound.
func (r *NetworkViewRepository) ReturnsCenterForRegion(ctx context.Context, regionID string) (network.Node, error) {
	defer r.store.enter(ctx)()
	var found *network.Node
	for _, n := range r.store.nodes {
		if n.RegionID != regionID || n.Type != network.TypeReturnsCenter || n.Lifecycle != network.NodeActive {
			continue
		}
		if found == nil || n.NodeID < found.NodeID {
			copied := n
			found = &copied
		}
	}
	if found == nil {
		return network.Node{}, ports.ErrNotFound
	}
	return *found, nil
}

// RegionOfPrefix answers the prefix's region, or ports.ErrNotFound.
func (r *NetworkViewRepository) RegionOfPrefix(ctx context.Context, prefix string) (string, error) {
	defer r.store.enter(ctx)()
	p, ok := r.store.prefixes[prefix]
	if !ok {
		return "", ports.ErrNotFound
	}
	return p.RegionID, nil
}

// Origin answers the group's origin row, or ports.ErrNotFound.
func (r *NetworkViewRepository) Origin(ctx context.Context, orderID string, groupNo int) (network.GroupOrigin, error) {
	defer r.store.enter(ctx)()
	o, ok := r.store.origins[groupKey{orderID, groupNo}]
	if !ok {
		return network.GroupOrigin{}, ports.ErrNotFound
	}
	return o, nil
}

// OutcomeFacts is the memory double of the outcome-facts port.
type OutcomeFacts struct{ store *Store }

// NewOutcomeFacts builds the double over the store.
func NewOutcomeFacts(s *Store) *OutcomeFacts { return &OutcomeFacts{store: s} }

// Record appends the outcome fact.
func (f *OutcomeFacts) Record(ctx context.Context, fact requests.OutcomeFact) error {
	defer f.store.enter(ctx)()
	f.store.outcomeFacts = append(f.store.outcomeFacts, fact)
	return nil
}

// LabelIssuer is the memory double of the reverse-label port: it records every
// call and answers whatever LabelResponse was configured.
type LabelIssuer struct{ store *Store }

// NewLabelIssuer builds the double over the store.
func NewLabelIssuer(s *Store) *LabelIssuer { return &LabelIssuer{store: s} }

// LabelResponse configures the double's answer (nil response means the
// function decides).
func (s *Store) LabelResponse(fn func(requests.IssueReturnLabel) (requests.ReturnLabel, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labelResponse = fn
}

// IssuedLabels returns the recorded calls.
func (s *Store) IssuedLabels() []requests.IssueReturnLabel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.issuedLabels)
}

// IssueReturnLabel implements ports.LabelIssuer.
func (l *LabelIssuer) IssueReturnLabel(ctx context.Context, in requests.IssueReturnLabel) (requests.ReturnLabel, error) {
	defer l.store.enter(ctx)()
	l.store.issuedLabels = append(l.store.issuedLabels, in)
	return l.store.labelResponse(in)
}

// Outbox is the memory double of the transactional outbox write side.
type Outbox struct{ store *Store }

// NewOutbox builds the double over the store.
func NewOutbox(s *Store) *Outbox { return &Outbox{store: s} }

// Append stores the events in order.
func (o *Outbox) Append(ctx context.Context, events ...shared.Event) error {
	defer o.store.enter(ctx)()
	o.store.outbox = append(o.store.outbox, events...)
	return nil
}

// Events drains the recorded events (test helper).
func (s *Store) Events() []shared.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.outbox)
}

// GroupSaver exposes the group-view double for harness seeding (test helper).
func (s *Store) GroupSaver() *GroupViewRepository { return &GroupViewRepository{store: s} }

// NetworkSaver exposes the network-view double for harness seeding (test helper).
func (s *Store) NetworkSaver() *NetworkViewRepository { return &NetworkViewRepository{store: s} }

// OutcomeFactRows returns the recorded outcome facts (test helper).
func (s *Store) OutcomeFactRows() []requests.OutcomeFact {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.outcomeFacts)
}

// Groups returns the group rows (test helper).
func (s *Store) Groups() map[groupKey]network.OrderGroup {
	s.mu.Lock()
	defer s.mu.Unlock()
	return groupsClone(s.groups)
}
