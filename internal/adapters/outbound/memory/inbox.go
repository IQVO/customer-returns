package memory

import (
	"context"
	"time"
)

// Inbox is the memory double of the consumers' inbox.
type Inbox struct{ store *Store }

// NewInbox builds the double over the store.
func NewInbox(s *Store) *Inbox { return &Inbox{store: s} }

// Claim records the event id and reports whether it was new.
func (i *Inbox) Claim(ctx context.Context, eventID, eventType string, _ time.Time) (bool, error) {
	defer i.store.enter(ctx)()
	if _, seen := i.store.inbox[eventID]; seen {
		return false, nil
	}
	i.store.inbox[eventID] = eventType
	return true, nil
}

// ClaimedType returns the event type an event id was claimed under, and
// whether the id was claimed at all (test helper).
func (s *Store) ClaimedType(eventID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	eventType, ok := s.inbox[eventID]
	return eventType, ok
}

// ForgetClaims deletes every inbox claim of eventType and reports how many it
// deleted: the double of the operator's `DELETE FROM processed_events WHERE
// event_type = ...` that precedes a rebuild of a read model (test helper).
func (s *Store) ForgetClaims(eventType string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, t := range s.inbox {
		if t == eventType {
			delete(s.inbox, id)
			n++
		}
	}
	return n
}
