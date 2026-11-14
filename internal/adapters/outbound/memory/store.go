// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit and BDD tests only: cmd/** never imports this package
// (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every repository round-trips its aggregate through Snapshot and Restore, so a
// loaded aggregate never aliases stored state and the doubles keep the
// persistence contract of the real adapters: a miss is ports.ErrNotFound, a
// stale or duplicate save is ports.ErrConcurrentUpdate.
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store for
// the whole unit and restores the state taken at the start when the unit fails
// or panics, outbox and inbox included.
package memory

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
	"github.com/IQVO/customer-returns/internal/domain/shared"
)

// The doubles implement the out ports.
var (
	_ ports.AuthorizationRepository = (*AuthorizationRepository)(nil)
	_ ports.GroupViewRepository     = (*GroupViewRepository)(nil)
	_ ports.NetworkViewRepository   = (*NetworkViewRepository)(nil)
	_ ports.OutcomeFacts            = (*OutcomeFacts)(nil)
	_ ports.LabelIssuer             = (*LabelIssuer)(nil)
	_ ports.ProcessedEvents         = (*Inbox)(nil)
	_ ports.EventOutbox             = (*Outbox)(nil)
	_ ports.UnitOfWork              = (*UnitOfWork)(nil)
	_ ports.Clock                   = (*Clock)(nil)
	_ ports.IDGenerator             = (*SequentialIDs)(nil)
	_ ports.ReturnMetrics           = (*Metrics)(nil)
)

type groupKey struct {
	orderID string
	groupNo int
}

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu             sync.Mutex
	authorizations map[returnauthorization.ID]returnauthorization.Snapshot
	groups         map[groupKey]network.OrderGroup
	nodes          map[string]network.Node
	prefixes       map[string]network.PrefixAssignment
	origins        map[groupKey]network.GroupOrigin
	outcomeFacts   []requests.OutcomeFact
	inbox          map[string]string
	outbox         []shared.Event
	issuedLabels   []requests.IssueReturnLabel
	labelResponse  func(requests.IssueReturnLabel) (requests.ReturnLabel, error)
}

// NewStore builds an empty store.
func NewStore() *Store {
	return &Store{
		authorizations: map[returnauthorization.ID]returnauthorization.Snapshot{},
		groups:         map[groupKey]network.OrderGroup{},
		nodes:          map[string]network.Node{},
		prefixes:       map[string]network.PrefixAssignment{},
		origins:        map[groupKey]network.GroupOrigin{},
		inbox:          map[string]string{},
		labelResponse: func(requests.IssueReturnLabel) (requests.ReturnLabel, error) {
			return requests.ReturnLabel{}, ports.ErrLabelUnavailable
		},
	}
}

type storeState struct {
	authorizations map[returnauthorization.ID]returnauthorization.Snapshot
	groups         map[groupKey]network.OrderGroup
	nodes          map[string]network.Node
	prefixes       map[string]network.PrefixAssignment
	origins        map[groupKey]network.GroupOrigin
	outcomeFacts   []requests.OutcomeFact
	inbox          map[string]string
	outbox         []shared.Event
	issuedLabels   []requests.IssueReturnLabel
}

func (s *Store) snapshotState() storeState {
	return storeState{
		authorizations: mapsClone(s.authorizations), groups: groupsClone(s.groups),
		nodes: mapsClone(s.nodes), prefixes: mapsClone(s.prefixes),
		origins: originsClone(s.origins), outcomeFacts: slices.Clone(s.outcomeFacts),
		inbox: mapsClone(s.inbox), outbox: slices.Clone(s.outbox),
		issuedLabels: slices.Clone(s.issuedLabels),
	}
}

func (s *Store) restore(st storeState) {
	s.authorizations = st.authorizations
	s.groups = st.groups
	s.nodes = st.nodes
	s.prefixes = st.prefixes
	s.origins = st.origins
	s.outcomeFacts = st.outcomeFacts
	s.inbox = st.inbox
	s.outbox = st.outbox
	s.issuedLabels = st.issuedLabels
}

func mapsClone[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func groupsClone(m map[groupKey]network.OrderGroup) map[groupKey]network.OrderGroup {
	out := make(map[groupKey]network.OrderGroup, len(m))
	for k, v := range m {
		v.Lines = slices.Clone(v.Lines)
		out[k] = v
	}
	return out
}

func originsClone(m map[groupKey]network.GroupOrigin) map[groupKey]network.GroupOrigin {
	out := make(map[groupKey]network.GroupOrigin, len(m))
	for k, v := range m {
		v.OwnersByItem = mapsClone(v.OwnersByItem)
		out[k] = v
	}
	return out
}

// enter locks the store unless ctx already carries THIS store's open unit
// (the unit holds the lock for the whole transaction, so the repository calls
// inside it must not lock again).
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// UnitOfWork is the store's atomic unit.
type UnitOfWork struct{ store *Store }

// NewUnitOfWork builds the unit over the store.
func NewUnitOfWork(s *Store) *UnitOfWork { return &UnitOfWork{store: s} }

// Do runs fn atomically (see ports.UnitOfWork).
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	u.store.mu.Lock()
	defer u.store.mu.Unlock()
	if ctx.Value(txKey{}) != nil {
		return fn(ctx)
	}
	snap := u.store.snapshotState()
	if err := fn(context.WithValue(ctx, txKey{}, true)); err != nil {
		u.store.restore(snap)
		return err
	}
	return nil
}

type txKey struct{}

// Clock is a settable clock.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// Now implements ports.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Set sets the clock.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// NewClockAtNow builds a clock already set to the real now.
func NewClockAtNow() *Clock {
	c := &Clock{}
	c.Set(time.Now().UTC())
	return c
}

// SequentialIDs mints ret-1, ret-2, ... in call order.
type SequentialIDs struct {
	mu sync.Mutex
	n  int
}

// NewSequentialIDs builds the generator.
func NewSequentialIDs() *SequentialIDs { return &SequentialIDs{} }

// NewID implements ports.IDGenerator.
func (g *SequentialIDs) NewID(prefix string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	return prefix + "-" + itoa(g.n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// Metrics is the no-op Tier 2 counter.
type Metrics struct {
	Authorized map[string]int
	Rejected   map[string]int
	Applied    int
}

// NewMetrics builds the counter.
func NewMetrics() *Metrics {
	return &Metrics{Authorized: map[string]int{}, Rejected: map[string]int{}}
}

// ReturnAuthorized implements ports.ReturnMetrics.
func (m *Metrics) ReturnAuthorized(_ context.Context, disposition string) {
	m.Authorized[disposition]++
}

// ReturnRejected implements ports.ReturnMetrics.
func (m *Metrics) ReturnRejected(_ context.Context, reason string) {
	m.Rejected[reason]++
}

// OutcomeApplied implements ports.ReturnMetrics.
func (m *Metrics) OutcomeApplied(_ context.Context, _ int) { m.Applied++ }
