package console

import (
	"sync"
	"time"
)

// Run is one executed console statement retained per (actor, cluster) so
// pagination, export and history-restore read back the full result without
// re-executing or re-auditing.
type Run struct {
	ID         string // full audit-row hash hex — stable, URL-safe lookup key
	ShortHash  string // display form only ("6c1d…e44"); never a lookup key
	ClusterID  string // owning cluster (redundant with the map key; kept for callers)
	At         time.Time
	SQL        string
	Node       string
	Database   string
	Result     Result
	DurationMs float64
}

// Sessions is an in-memory ring of recent runs, keyed by (actor, cluster) and
// newest first. It is process-local UI state, not a durable store; the durable
// record of every run is the audit_log. Keying by cluster keeps a run from one
// scope from being read back (paginated / exported / restored) under a
// different cluster's URL.
type Sessions struct {
	mu    sync.Mutex
	byKey map[string][]Run
	cap   int
}

// sessionKey composites the actor and cluster into a single map key. \x00 is a
// safe separator because neither actor ids nor cluster ids contain a NUL byte.
func sessionKey(actor, clusterID string) string { return actor + "\x00" + clusterID }

// NewSessions returns a cache retaining `capacity` runs per (actor, cluster).
func NewSessions(capacity int) *Sessions {
	if capacity <= 0 {
		capacity = 5
	}
	return &Sessions{byKey: map[string][]Run{}, cap: capacity}
}

// Append prepends r for (actor, clusterID) and trims to the capacity.
func (s *Sessions) Append(actor, clusterID string, r Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := sessionKey(actor, clusterID)
	list := append([]Run{r}, s.byKey[k]...)
	if len(list) > s.cap {
		list = list[:s.cap]
	}
	s.byKey[k] = list
}

// Recent returns a copy of the (actor, cluster) runs, newest first.
func (s *Sessions) Recent(actor, clusterID string) []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Run(nil), s.byKey[sessionKey(actor, clusterID)]...)
}

// Latest returns the (actor, cluster) most recent run.
func (s *Sessions) Latest(actor, clusterID string) (Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list := s.byKey[sessionKey(actor, clusterID)]; len(list) > 0 {
		return list[0], true
	}
	return Run{}, false
}

// Get returns the (actor, cluster) run with the given full-hex id.
func (s *Sessions) Get(actor, clusterID, id string) (Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.byKey[sessionKey(actor, clusterID)] {
		if r.ID == id {
			return r, true
		}
	}
	return Run{}, false
}
