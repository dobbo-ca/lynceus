package caps

import "sync"

// GateKey identifies one effective-policy decision by (database,
// capability). The collector connects to a single database, so db is the
// connection's current_database(); per-database overrides for OTHER
// databases on the same server are not honored by a single connection
// (documented limitation, spec §4.4.2 B4).
type GateKey struct {
	Db  string
	Cap Capability
}

// Gate is a collector-local in-memory snapshot of effective capability
// policy. Allowed is an O(1) map lookup under RLock with zero I/O —
// readers call it before every query. Replace atomically swaps a freshly
// fetched snapshot and is called on the full-snapshot ticker, never per
// read. The collector has no config-DB handle (spec §4.4.0), so the
// authoritative resolver runs server-side and reaches the collector via
// GET /policy-snapshot.
type Gate struct {
	mu      sync.RWMutex
	enabled map[GateKey]bool
}

// NewGate returns an empty Gate. With no snapshot loaded, every Allowed
// returns true (fail-open) so a collector is never silently dark before
// its first successful policy fetch.
func NewGate() *Gate {
	return &Gate{enabled: make(map[GateKey]bool)}
}

// Allowed reports whether capability c is enabled for database db. An
// absent key returns true (fail-open / default-enabled).
func (g *Gate) Allowed(db string, c Capability) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	enabled, ok := g.enabled[GateKey{Db: db, Cap: c}]
	if !ok {
		return true // absent => enabled (fail-open)
	}
	return enabled
}

// Replace atomically installs a new snapshot. A nil snap clears the gate
// (back to all-enabled).
func (g *Gate) Replace(snap map[GateKey]bool) {
	if snap == nil {
		snap = make(map[GateKey]bool)
	}
	g.mu.Lock()
	g.enabled = snap
	g.mu.Unlock()
}
