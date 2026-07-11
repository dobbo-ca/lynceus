// Package scope models the current working scope of the UI shell:
// fleet -> cluster -> node/pooler -> database. A Scope round-trips through
// a single URL-safe "scope" query param (Encode/Parse) so the top-bar
// picker, the row ⌖ buttons, and deep links all agree. Databases are
// identified by cluster + name (the same name in two clusters is a
// different database), so the database form carries the cluster id.
package scope

import "strings"

// Kind is the scope level.
type Kind string

const (
	Fleet    Kind = "fleet"
	Cluster  Kind = "cluster"
	Node     Kind = "node"
	Pooler   Kind = "pooler"
	Database Kind = "database"
)

// Scope is the current working scope. The zero value is Fleet.
type Scope struct {
	Kind      Kind
	ClusterID string // cluster, node, database
	NodeID    string // node (a store Instance id)
	PoolerID  string // pooler (not yet modeled server-side; see ly-99s)
	Database  string // database name (database scope)
}

// IsFleet reports whether this is the fleet (root) scope.
func (s Scope) IsFleet() bool { return s.Kind == "" || s.Kind == Fleet }

// Encode returns the "scope" query-param form. Fleet -> "".
func (s Scope) Encode() string {
	switch s.Kind {
	case Cluster:
		return "cluster:" + s.ClusterID
	case Node:
		return "node:" + s.ClusterID + ":" + s.NodeID
	case Pooler:
		return "pooler:" + s.PoolerID
	case Database:
		return "db:" + s.ClusterID + ":" + s.Database
	default:
		return ""
	}
}

// Parse decodes an Encode() string. Empty or unrecognized input -> Fleet.
func Parse(raw string) Scope {
	if raw == "" {
		return Scope{Kind: Fleet}
	}
	key, val, ok := strings.Cut(raw, ":")
	if !ok {
		return Scope{Kind: Fleet}
	}
	switch key {
	case "cluster":
		return Scope{Kind: Cluster, ClusterID: val}
	case "node":
		cid, nid, ok := strings.Cut(val, ":")
		if !ok {
			return Scope{Kind: Fleet}
		}
		return Scope{Kind: Node, ClusterID: cid, NodeID: nid}
	case "pooler":
		return Scope{Kind: Pooler, PoolerID: val}
	case "db":
		cid, name, ok := strings.Cut(val, ":")
		if !ok {
			return Scope{Kind: Fleet}
		}
		return Scope{Kind: Database, ClusterID: cid, Database: name}
	default:
		return Scope{Kind: Fleet}
	}
}
