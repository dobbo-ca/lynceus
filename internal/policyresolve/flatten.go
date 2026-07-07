// Package policyresolve is the pure-function core of the CRD capability-policy
// flattener (crd-operator-control-plane.md §3, lines 199-241). It takes the
// four-tier scope tree (fleet -> group -> instance -> database) authored as
// CapabilityPolicy CRs and flattens it DOWN to the exactly-two stored levels
// the existing resolver understands: a server-wide default row
// (database_name NULL) and a per-database override row.
//
// The package is framework-free: it imports only stdlib + internal/caps, has
// no k8s / store / DB / api dependency, and is independently unit-testable.
// The DesiredRow -> store-upsert adapter is deferred to the operator
// CapabilityPolicyController bead.
//
// Load-bearing privacy invariant (§2.3 line 134): a servers row == one
// collector CONNECTION == one current_database() gate key. The downstream
// resolvers (collector/policy_refresh.go:48-61; store/capability_policy.go:
// 169-193) only honor the NULL default and an override matching the
// connection's current_database; they silently ignore override rows for
// sibling datnames. Flatten therefore emits database-tier winners ONLY as an
// override keyed to the stream whose current_database == database_name, and
// surfaces a Warning-class Conflict when a databaseSelector policy names a
// database no in-scope stream serves.
package policyresolve

import (
	"fmt"
	"sort"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// Scope is the policy tier, broadest -> narrowest.
type Scope string

const (
	// ScopeFleet applies to every server in the graph.
	ScopeFleet Scope = "fleet"
	// ScopeGroup applies to servers matched by a cluster-level label selector.
	ScopeGroup Scope = "group"
	// ScopeInstance applies to servers matched by an instance-level label selector.
	ScopeInstance Scope = "instance"
	// ScopeDatabase applies to the single stream whose current_database ==
	// DatabaseName (and whose labels match Selector).
	ScopeDatabase Scope = "database"
)

// tierRank orders scopes narrower-wins: database(4) > instance(3) > group(2) >
// fleet(1). A higher rank beats a lower one.
func tierRank(s Scope) int {
	switch s {
	case ScopeDatabase:
		return 4
	case ScopeInstance:
		return 3
	case ScopeGroup:
		return 2
	default: // ScopeFleet
		return 1
	}
}

// CapabilityToggle is one capability's desired state inside a policy.
type CapabilityToggle struct {
	Capability caps.Capability
	Enabled    bool
}

// ScopedPolicy is one CapabilityPolicy CR, already resolved to its scope and
// matching keys so Flatten stays pure (no metav1.LabelSelector parsing here —
// that is the operator's job). Selection:
//   - fleet:    applies to every server in the graph.
//   - group:    Selector is a subset of a server's labels.
//   - instance: Selector is a subset of a server's labels.
//   - database: Selector subset of labels AND DatabaseName == the server's
//     current_database.
type ScopedPolicy struct {
	Name         string            // CR name, for conflict/warning attribution
	Scope        Scope             // tier
	Priority     int               // higher wins within a tier
	Selector     map[string]string // label match (group/instance/database)
	DatabaseName string            // database scope only: target current_database
	Capabilities []CapabilityToggle
}

// Entity is one in-scope server: one collector connection / one stream.
type Entity struct {
	CurrentDatabase string            // the connection's current_database()
	Labels          map[string]string // cluster+instance labels propagated to the stream
}

// EntityGraph maps in-scope server_id -> Entity.
type EntityGraph map[string]Entity

// DesiredRow mirrors store.CapabilityPolicy's storable shape (the F2 target).
// DatabaseName == "" is the NULL server-wide default; a non-empty value is a
// per-database override effective only for the stream whose current_database
// equals it.
type DesiredRow struct {
	ServerID     string
	DatabaseName string
	Capability   caps.Capability
	Enabled      bool
}

// Conflict is a refuse-and-surface signal (NOT a hard fail): either an
// equal-tier/equal-priority disagreement on enabled for one capability (the
// row is omitted, leaving the prior row intact downstream), or a dead-row
// warning when a databaseSelector policy targets a database no in-scope stream
// serves. ServerID is "" for graph-wide dead-row warnings.
type Conflict struct {
	Capability caps.Capability
	ServerID   string
	Reason     string
}

// matches reports whether policy p applies to server (sid, e). For database
// scope it also requires DatabaseName == e.CurrentDatabase (the single
// current_database rule); callers that need the dead-row warning check that
// separately.
func matches(p *ScopedPolicy, e Entity) bool {
	if !labelsSubset(p.Selector, e.Labels) {
		return false
	}
	if p.Scope == ScopeDatabase {
		return p.DatabaseName == e.CurrentDatabase
	}
	return true
}

// labelsSubset reports whether every key/value in want is present in have. An
// empty/nil want matches everything (fleet-style).
func labelsSubset(want, have map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// candidate is one policy's toggle for a given (server, capability), retained
// for precedence resolution.
type candidate struct {
	scope    Scope
	priority int
	enabled  bool
}

// resolvePrecedence picks the winner among same-(server,capability) candidates
// per the §3 rules (plan line 139): FIRST reduce to the single winning level —
// narrower tier wins, then higher priority within that tier — over ALL
// candidates; THEN evaluate conflict only among candidates AT that winning
// (tier, priority) level. Equal tier AND equal priority AND disagreeing enabled
// => refuse (ok=false, conflict reason set). A lower-tier or lower-priority
// disagreement is NOT a conflict: the winning level decides. This two-pass
// shape is what makes Flatten order-independent (Determinism criterion).
// cands must be non-empty.
func resolvePrecedence(cands []candidate) (win candidate, ok bool, conflictReason string) {
	// Pass 1: find the winning level over all candidates.
	best := cands[0]
	for _, c := range cands[1:] {
		if tierRank(c.scope) > tierRank(best.scope) ||
			(tierRank(c.scope) == tierRank(best.scope) && c.priority > best.priority) {
			best = c
		}
	}
	// Pass 2: conflict only among candidates at the winning level.
	for _, c := range cands {
		if tierRank(c.scope) == tierRank(best.scope) && c.priority == best.priority &&
			c.enabled != best.enabled {
			return candidate{}, false, fmt.Sprintf(
				"conflict: equal tier %s and equal priority %d disagree on enabled",
				best.scope, best.priority)
		}
	}
	return best, true, ""
}

// deadRowConflicts surfaces a Warning-class conflict per (database policy,
// capability) whose DatabaseName matches no in-scope stream's current_database.
// Such a policy would only ever produce override rows the downstream resolver
// ignores, so Flatten emits no row for it and raises this signal instead.
// (§3 line 226)
func deadRowConflicts(policies []ScopedPolicy, graph EntityGraph) []Conflict {
	var conflicts []Conflict
	for i := range policies {
		p := &policies[i]
		if p.Scope != ScopeDatabase {
			continue
		}
		hit := false
		for _, e := range graph {
			if labelsSubset(p.Selector, e.Labels) && p.DatabaseName == e.CurrentDatabase {
				hit = true
				break
			}
		}
		if hit {
			continue
		}
		for _, tog := range p.Capabilities {
			conflicts = append(conflicts, Conflict{
				Capability: tog.Capability,
				Reason: fmt.Sprintf(
					"dead-row: databaseSelector policy %q targets database %q which is no in-scope stream's current_database",
					p.Name, p.DatabaseName),
			})
		}
	}
	return conflicts
}

// Flatten resolves the four-tier scoped policies against the entity graph and
// returns the storable DesiredRows plus any Conflicts (refuse-and-surface and
// dead-row warnings). Output is sorted canonically for deterministic diffs:
// rows by (ServerID, DatabaseName, Capability); conflicts by (ServerID,
// Capability, Reason). It is a pure function of its inputs.
//
// Complexity: O(streams * caps * policies) — sub-millisecond at fleet scale
// (§3 line 241).
func Flatten(policies []ScopedPolicy, graph EntityGraph) (rows []DesiredRow, conflicts []Conflict) {
	conflicts = deadRowConflicts(policies, graph)

	for sid, e := range graph {
		// Per capability, split candidates into the broad tiers (fleet/group/
		// instance -> NULL-default row) and the database tier (-> override row),
		// then resolve each independently so override-beats-default emits BOTH
		// rows without pre-collapsing. (§3 lines 233-236)
		broad := map[caps.Capability][]candidate{}
		dbTier := map[caps.Capability][]candidate{}

		for i := range policies {
			p := &policies[i]
			if !matches(p, e) {
				continue
			}
			for _, tog := range p.Capabilities {
				c := candidate{scope: p.Scope, priority: p.Priority, enabled: tog.Enabled}
				if p.Scope == ScopeDatabase {
					dbTier[tog.Capability] = append(dbTier[tog.Capability], c)
				} else {
					broad[tog.Capability] = append(broad[tog.Capability], c)
				}
			}
		}

		// Broad tiers -> NULL-default row.
		for cap, cands := range broad {
			win, ok, reason := resolvePrecedence(cands)
			if !ok {
				conflicts = append(conflicts, Conflict{Capability: cap, ServerID: sid, Reason: reason})
				continue
			}
			rows = append(rows, DesiredRow{ServerID: sid, DatabaseName: "", Capability: cap, Enabled: win.enabled})
		}

		// Database tier -> per-db override row keyed to this stream's
		// current_database. (matches() already guaranteed DatabaseName ==
		// CurrentDatabase, so no sibling-datname rows are emitted.)
		for cap, cands := range dbTier {
			win, ok, reason := resolvePrecedence(cands)
			if !ok {
				conflicts = append(conflicts, Conflict{Capability: cap, ServerID: sid, Reason: reason})
				continue
			}
			rows = append(rows, DesiredRow{ServerID: sid, DatabaseName: e.CurrentDatabase, Capability: cap, Enabled: win.enabled})
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.ServerID != b.ServerID {
			return a.ServerID < b.ServerID
		}
		if a.DatabaseName != b.DatabaseName {
			return a.DatabaseName < b.DatabaseName
		}
		return a.Capability < b.Capability
	})
	sort.Slice(conflicts, func(i, j int) bool {
		a, b := conflicts[i], conflicts[j]
		if a.ServerID != b.ServerID {
			return a.ServerID < b.ServerID
		}
		if a.Capability != b.Capability {
			return a.Capability < b.Capability
		}
		return a.Reason < b.Reason
	})
	return rows, conflicts
}
