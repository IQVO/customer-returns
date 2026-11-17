package analyticsstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

// MemoryStore is the in-memory twin of Projection and Reader, for unit tests
// of the consumer, the reports and the BDD scenarios. contract_test.go runs the
// SAME suite against it and against Postgres, so its behaviour cannot drift
// from the real store. The shipped binaries never construct it.
type MemoryStore struct {
	mu             sync.Mutex
	processed      map[string]time.Time
	authorizations map[string]memAuthorization
	receipts       map[string]time.Time
	// Fail, when set, is returned by the next Apply (and cleared), to drive
	// the consumer's transient and rejection paths.
	Fail error
}

type memAuthorization struct {
	reason string
	at     time.Time
}

// NewMemoryStore returns an empty twin.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		processed:      map[string]time.Time{},
		authorizations: map[string]memAuthorization{},
		receipts:       map[string]time.Time{},
	}
}

var (
	_ returnsperformance.Projection = (*MemoryStore)(nil)
	_ returnsperformance.Reader     = (*MemoryStore)(nil)
)

// Apply implements returnsperformance.Projection with the Postgres store's
// semantics: an event the schema's CHECKs would refuse is rejected before
// anything is recorded, a replay is a no-op.
func (m *MemoryStore) Apply(_ context.Context, e returnsperformance.Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		err := m.Fail
		m.Fail = nil
		return false, err
	}
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", returnsperformance.ErrRejected, err)
	}
	if _, seen := m.processed[e.EventID]; seen {
		return false, nil
	}
	m.processed[e.EventID] = e.At.UTC()
	if e.Kind == returnsperformance.KindAuthorized {
		m.authorizations[e.AuthorizationID] = memAuthorization{reason: e.Reason, at: e.OccurredAt.UTC()}
		return true, nil
	}
	m.receipts[e.AuthorizationID] = e.OccurredAt.UTC() // Validate admitted only the two kinds
	return true, nil
}

// inRange reports whether the instant is in [r.From, r.To).
func inRange(t time.Time, r returnsperformance.Range) bool {
	return t.Compare(r.From) >= 0 && t.Before(r.To)
}

// AuthorizedPerDay implements returnsperformance.Reader.
func (m *MemoryStore) AuthorizedPerDay(_ context.Context, r returnsperformance.Range) ([]returnsperformance.AuthorizedDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := map[time.Time]int{}
	for _, a := range m.authorizations {
		if inRange(a.at, r) {
			counts[returnsperformance.DayOf(a.at)]++
		}
	}
	out := make([]returnsperformance.AuthorizedDay, 0, len(counts))
	for day, n := range counts {
		out = append(out, returnsperformance.AuthorizedDay{Day: day, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, nil
}

// ReceivedPerDay implements returnsperformance.Reader.
func (m *MemoryStore) ReceivedPerDay(_ context.Context, r returnsperformance.Range) ([]returnsperformance.ReceivedDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type bucket struct {
		received int
		seconds  []float64
	}
	days := map[time.Time]*bucket{}
	for id, at := range m.receipts {
		if !inRange(at, r) {
			continue
		}
		b := days[returnsperformance.DayOf(at)]
		if b == nil {
			b = &bucket{}
			days[returnsperformance.DayOf(at)] = b
		}
		b.received++
		if a, ok := m.authorizations[id]; ok {
			b.seconds = append(b.seconds, returnsperformance.Seconds(a.at, at))
		}
	}
	out := make([]returnsperformance.ReceivedDay, 0, len(days))
	for day, b := range days {
		d := returnsperformance.ReceivedDay{Day: day, Received: b.received, Samples: len(b.seconds)}
		if len(b.seconds) > 0 {
			p50 := returnsperformance.Percentile(b.seconds, returnsperformance.TimeToReceiveP50())
			p95 := returnsperformance.Percentile(b.seconds, returnsperformance.TimeToReceiveP95())
			d.P50, d.P95 = &p50, &p95
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, nil
}

// ReasonCounts implements returnsperformance.Reader.
func (m *MemoryStore) ReasonCounts(_ context.Context, r returnsperformance.Range) ([]returnsperformance.ReasonCount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := map[string]int{}
	for _, a := range m.authorizations {
		if inRange(a.at, r) {
			counts[a.reason]++
		}
	}
	out := make([]returnsperformance.ReasonCount, 0, len(counts))
	for reason, n := range counts {
		out = append(out, returnsperformance.ReasonCount{Reason: reason, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reason < out[j].Reason })
	return out, nil
}

// LastEventAt implements returnsperformance.Reader.
func (m *MemoryStore) LastEventAt(context.Context) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var last *time.Time
	for _, at := range m.processed {
		if last == nil || at.After(*last) {
			a := at
			last = &a
		}
	}
	return last, nil
}

// Ping is the twin's readiness check: always ready.
func (m *MemoryStore) Ping(context.Context) error { return nil }
