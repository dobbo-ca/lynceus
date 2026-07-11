package web

import (
	"strings"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

// scope.go carries the ly-ae6.3-owned nav helpers that sit on top of the
// ly-ae6.2 scope model (internal/scope). The Scope value-object, its five Kind
// constants, and the URL codec (Encode/Parse) are owned by ly-ae6.2; this bead
// only adds the engine flags, the fleet-scope constructor, and the identity
// header label the sidebar needs — it never forks the scope type.

// EngineFlags gates the per-vertical fleet sections. Cache shows if Redis OR
// Valkey is enabled, Search if Elasticsearch OR OpenSearch — the caller
// collapses those pairs before constructing this (PRODUCT_INTENT §5).
type EngineFlags struct {
	Postgres bool
	Search   bool
	Cache    bool
}

// FleetScope is the default (unscoped) scope.
func FleetScope() scope.Scope { return scope.Scope{Kind: scope.Fleet} }

// DefaultEngines is the only real configuration today: Postgres on, Search and
// Cache off until those verticals ship (ly-ae6.10 / ly-ae6.11).
func DefaultEngines() EngineFlags { return EngineFlags{Postgres: true} }

// headerLabel is the identity-group header shown at the top of a scoped nav
// tree, e.g. "CLUSTER: ORDERS-PROD". A scope.Scope carries only ids, so the
// resolved display name is threaded in by the shell (ShellView.ScopeLabel);
// fleet scope has no identity group, so it returns "OVERVIEW".
func headerLabel(sc scope.Scope, label string) string {
	switch sc.Kind {
	case scope.Cluster:
		return "CLUSTER: " + strings.ToUpper(label)
	case scope.Node:
		return "NODE: " + strings.ToUpper(label)
	case scope.Pooler:
		return "POOLER: " + strings.ToUpper(label)
	case scope.Database:
		return "DATABASE: " + strings.ToUpper(label)
	default:
		return "OVERVIEW"
	}
}
