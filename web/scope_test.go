package web

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func TestDefaultEngines_PostgresOnly(t *testing.T) {
	e := DefaultEngines()
	if !e.Postgres || e.Search || e.Cache {
		t.Errorf("DefaultEngines() = %+v, want only Postgres true", e)
	}
}

func TestFleetScope_IsFleet(t *testing.T) {
	if !FleetScope().IsFleet() {
		t.Error("FleetScope() must be the fleet scope")
	}
}

// headerLabel is the identity-group header at the top of a scoped nav tree. The
// scope.Scope carries only ids, so the resolved display label is threaded in by
// the shell (ShellView.ScopeLabel); fleet scope has no identity group.
func TestHeaderLabel(t *testing.T) {
	cases := []struct {
		sc    scope.Scope
		label string
		want  string
	}{
		{FleetScope(), "", "OVERVIEW"},
		{scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}, "orders-prod", "CLUSTER: ORDERS-PROD"},
		{scope.Scope{Kind: scope.Node, ClusterID: "c-1", NodeID: "n-1"}, "srv-orders-1", "NODE: SRV-ORDERS-1"},
		{scope.Scope{Kind: scope.Pooler, PoolerID: "p-1"}, "pgbouncer-1", "POOLER: PGBOUNCER-1"},
		{scope.Scope{Kind: scope.Database, ClusterID: "c-1", Database: "orders"}, "orders", "DATABASE: ORDERS"},
	}
	for _, c := range cases {
		if got := headerLabel(c.sc, c.label); got != c.want {
			t.Errorf("headerLabel(%v, %q) = %q, want %q", c.sc, c.label, got, c.want)
		}
	}
}
