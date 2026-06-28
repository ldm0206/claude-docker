// Package capture provides a thread-safe store for HTTP request/response records
// and secret redaction utilities. All bodies and headers stored are expected to
// already be redacted by the caller; the store itself is redaction-agnostic.
package capture

import (
	"sync"
)

// Record represents a single captured HTTP request/response pair.
type Record struct {
	SessionID                              string
	Method, Host, Path                     string
	Status                                 int
	LatencyMs, Ts                          int64
	ReqHeaders, ResHeaders                 map[string]string
	ReqBody, ResBody                       string
}

// Store is a mutex-guarded, append-only record store with subscriber support.
type Store struct {
	mu          sync.Mutex
	records     []Record
	subscribers map[uint64]func(Record)
	nextSubID   uint64
}

// NewStore returns an initialized Store.
func NewStore() *Store {
	return &Store{
		subscribers: make(map[uint64]func(Record)),
	}
}

// Add appends a record and notifies all subscribers. Subscribers are called
// outside the mutex lock to avoid deadlocks if a subscriber calls back into
// the store.
func (s *Store) Add(r Record) {
	s.mu.Lock()
	s.records = append(s.records, r)
	subs := make([]func(Record), 0, len(s.subscribers))
	for _, cb := range s.subscribers {
		subs = append(subs, cb)
	}
	s.mu.Unlock()

	for _, cb := range subs {
		cb(r)
	}
}

// List returns a copy of records filtered by sessionID. An empty sessionID
// returns all records. The returned slice is a defensive copy so callers
// cannot mutate the store's internal state.
func (s *Store) List(sessionID string) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Record
	for _, r := range s.records {
		if sessionID == "" || r.SessionID == sessionID {
			out = append(out, r)
		}
	}
	if out == nil {
		out = []Record{}
	}
	return out
}

// Clear removes all records from the store.
func (s *Store) Clear() {
	s.mu.Lock()
	s.records = s.records[:0]
	s.mu.Unlock()
}

// ClearSession removes all records belonging to the given session.
func (s *Store) ClearSession(id string) {
	s.mu.Lock()
	filtered := s.records[:0]
	for _, r := range s.records {
		if r.SessionID != id {
			filtered = append(filtered, r)
		}
	}
	s.records = filtered
	s.mu.Unlock()
}

// Subscribe registers a callback that fires on every Add. The returned
// function unsubscribes when called and is idempotent.
func (s *Store) Subscribe(cb func(Record)) func() {
	s.mu.Lock()
	id := s.nextSubID
	s.nextSubID++
	s.subscribers[id] = cb
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		delete(s.subscribers, id)
		s.mu.Unlock()
	}
}
