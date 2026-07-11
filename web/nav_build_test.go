package web

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

// helpers ------------------------------------------------------------------

func groupLabels(gs []NavGroup) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Label
	}
	return out
}

func hasGroup(gs []NavGroup, label string) bool {
	for _, g := range gs {
		if g.Label == label {
			return true
		}
	}
	return false
}

func findGroup(gs []NavGroup, label string) (NavGroup, bool) {
	for _, g := range gs {
		if g.Label == label {
			return g, true
		}
	}
	return NavGroup{}, false
}

func hasScreen(gs []NavGroup, screen string) bool {
	for _, g := range gs {
		for _, it := range g.Items {
			if it.Screen == screen {
				return true
			}
		}
	}
	return false
}

// itemActive reports whether the item for `screen` is marked active.
func itemActive(gs []NavGroup, screen string) bool {
	for _, g := range gs {
		for _, it := range g.Items {
			if it.Screen == screen {
				return it.Active
			}
		}
	}
	return false
}

var (
	scCluster  = scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}
	scNode     = scope.Scope{Kind: scope.Node, ClusterID: "c-1", NodeID: "n-1"}
	scPooler   = scope.Scope{Kind: scope.Pooler, ClusterID: "c-1", PoolerID: "p-1"}
	scDatabase = scope.Scope{Kind: scope.Database, ClusterID: "c-1", Database: "orders"}
)

// fleet --------------------------------------------------------------------

func TestBuildNav_FleetHidesLowLevelSections(t *testing.T) {
	gs := BuildNav(FleetScope(), "", EngineFlags{Postgres: true, Search: true, Cache: true}, "")
	for _, banned := range []string{"QUERIES", "ADVISORS", "ACTIVITY", "CHECKS & ALERTS", "SCHEMA", "LOGS"} {
		if hasGroup(gs, banned) {
			t.Errorf("fleet nav must not contain low-level group %q; groups = %v", banned, groupLabels(gs))
		}
	}
	if hasScreen(gs, "capabilities") {
		t.Error("fleet nav must not expose capabilities")
	}
	// CONSOLE at fleet is Saved Scripts only — no SQL Console.
	if hasScreen(gs, "console") {
		t.Error("fleet nav must not contain SQL Console (rule: cluster/node/db only)")
	}
	if !hasScreen(gs, "scripts") {
		t.Error("fleet nav must contain Saved Scripts (rule: every scope)")
	}
}

func TestBuildNav_FleetEngineGating(t *testing.T) {
	pgOnly := BuildNav(FleetScope(), "", EngineFlags{Postgres: true}, "")
	if !hasGroup(pgOnly, "DATABASE") || hasGroup(pgOnly, "SEARCH") || hasGroup(pgOnly, "CACHE") {
		t.Errorf("postgres-only fleet groups = %v, want DATABASE without SEARCH/CACHE", groupLabels(pgOnly))
	}
	all := BuildNav(FleetScope(), "", EngineFlags{Postgres: true, Search: true, Cache: true}, "")
	for _, want := range []string{"OVERVIEW", "DATABASE", "SEARCH", "CACHE", "CONSOLE"} {
		if !hasGroup(all, want) {
			t.Errorf("all-engines fleet missing group %q; groups = %v", want, groupLabels(all))
		}
	}
	none := BuildNav(FleetScope(), "", EngineFlags{}, "")
	if len(groupLabels(none)) != 2 || !hasGroup(none, "OVERVIEW") || !hasGroup(none, "CONSOLE") {
		t.Errorf("no-engines fleet groups = %v, want [OVERVIEW CONSOLE]", groupLabels(none))
	}
}

func TestBuildNav_FleetDatabaseSection(t *testing.T) {
	gs := BuildNav(FleetScope(), "", EngineFlags{Postgres: true}, "")
	g, ok := findGroup(gs, "DATABASE")
	if !ok {
		t.Fatal("no DATABASE group")
	}
	want := []string{"Clusters", "Nodes", "Databases"}
	if len(g.Items) != 3 {
		t.Fatalf("DATABASE items = %d, want 3", len(g.Items))
	}
	for i, w := range want {
		if g.Items[i].Label != w {
			t.Errorf("DATABASE item %d = %q, want %q", i, g.Items[i].Label, w)
		}
	}
}

// cluster ------------------------------------------------------------------

func TestBuildNav_ClusterTree(t *testing.T) {
	gs := BuildNav(scCluster, "orders-prod", DefaultEngines(), "")
	if gs[0].Label != "CLUSTER: ORDERS-PROD" {
		t.Errorf("identity header = %q, want CLUSTER: ORDERS-PROD", gs[0].Label)
	}
	for _, want := range []string{"QUERIES", "ADVISORS", "ACTIVITY", "CONSOLE", "CHECKS & ALERTS", "SCHEMA", "LOGS"} {
		if !hasGroup(gs, want) {
			t.Errorf("cluster nav missing group %q", want)
		}
	}
	// ADVISORS at cluster scope includes Config · per node.
	adv, _ := findGroup(gs, "ADVISORS")
	if len(adv.Items) != 3 || adv.Items[2].Label != "Config · per node" {
		t.Errorf("cluster ADVISORS = %+v, want Index/Vacuum/Config · per node", adv.Items)
	}
	// CONSOLE at cluster scope = SQL Console (T2) + Saved Scripts.
	con, _ := findGroup(gs, "CONSOLE")
	if len(con.Items) != 2 || con.Items[0].Label != "SQL Console" || !con.Items[0].T2 || con.Items[1].Label != "Saved Scripts" {
		t.Errorf("cluster CONSOLE = %+v, want SQL Console(T2)+Saved Scripts", con.Items)
	}
	// identity group has Overview/Nodes/Databases/Capabilities.
	if !hasScreen(gs, "clusterdetail") || !hasScreen(gs, "capabilities") {
		t.Error("cluster identity group missing Overview or Capabilities")
	}
}

// node ---------------------------------------------------------------------

func TestBuildNav_NodeTree(t *testing.T) {
	gs := BuildNav(scNode, "srv-orders-1", DefaultEngines(), "")
	if gs[0].Label != "NODE: SRV-ORDERS-1" {
		t.Errorf("identity header = %q", gs[0].Label)
	}
	// ADVISORS at node scope has NO Config item.
	adv, _ := findGroup(gs, "ADVISORS")
	if len(adv.Items) != 2 {
		t.Errorf("node ADVISORS items = %d, want 2 (Index, Vacuum)", len(adv.Items))
	}
	if hasGroup(gs, "SCHEMA") {
		t.Error("node nav must not have SCHEMA group")
	}
	if !hasGroup(gs, "LOGS") {
		t.Error("node nav must have LOGS group")
	}
	if !hasScreen(gs, "console") { // SQL Console valid at node scope
		t.Error("node nav must contain SQL Console")
	}
}

// pooler -------------------------------------------------------------------

func TestBuildNav_PoolerTree(t *testing.T) {
	gs := BuildNav(scPooler, "pgbouncer-1", DefaultEngines(), "")
	if gs[0].Label != "POOLER: PGBOUNCER-1" {
		t.Errorf("identity header = %q", gs[0].Label)
	}
	if hasScreen(gs, "console") {
		t.Error("pooler nav must NOT contain SQL Console (rule: cluster/node/db only)")
	}
	if !hasScreen(gs, "scripts") {
		t.Error("pooler nav must contain Saved Scripts (rule: every scope)")
	}
	if hasGroup(gs, "QUERIES") || hasGroup(gs, "ADVISORS") {
		t.Error("pooler nav has no QUERIES/ADVISORS groups")
	}
	act, ok := findGroup(gs, "ACTIVITY")
	if !ok || len(act.Items) != 1 || act.Items[0].Label != "Connections" {
		t.Errorf("pooler ACTIVITY = %+v, want single Connections", act.Items)
	}
}

// database -----------------------------------------------------------------

func TestBuildNav_DatabaseTree(t *testing.T) {
	gs := BuildNav(scDatabase, "orders", DefaultEngines(), "")
	if gs[0].Label != "DATABASE: ORDERS" {
		t.Errorf("identity header = %q", gs[0].Label)
	}
	if !hasScreen(gs, "console") {
		t.Error("database nav must contain SQL Console")
	}
	if hasGroup(gs, "ACTIVITY") || hasGroup(gs, "LOGS") {
		t.Error("database nav has no ACTIVITY/LOGS groups")
	}
	if !hasGroup(gs, "SCHEMA") {
		t.Error("database nav must have SCHEMA group")
	}
	// CHECKS & ALERTS at db scope = Checks only (no Alerts).
	chk, _ := findGroup(gs, "CHECKS & ALERTS")
	if len(chk.Items) != 1 || chk.Items[0].Label != "Checks" {
		t.Errorf("database CHECKS = %+v, want single Checks", chk.Items)
	}
}

// cross-cutting rules ------------------------------------------------------

func TestBuildNav_SavedScriptsEverywhere(t *testing.T) {
	scopes := []scope.Scope{FleetScope(), scCluster, scNode, scPooler, scDatabase}
	for _, sc := range scopes {
		if !hasScreen(BuildNav(sc, "x", DefaultEngines(), ""), "scripts") {
			t.Errorf("scope %s missing Saved Scripts", sc.Kind)
		}
	}
}

func TestBuildNav_SQLConsoleOnlyClusterNodeDatabase(t *testing.T) {
	cases := []struct {
		sc   scope.Scope
		want bool
	}{
		{FleetScope(), false},
		{scCluster, true},
		{scNode, true},
		{scPooler, false},
		{scDatabase, true},
	}
	for _, c := range cases {
		if got := hasScreen(BuildNav(c.sc, "x", DefaultEngines(), ""), "console"); got != c.want {
			t.Errorf("SQL Console at %s = %v, want %v", c.sc.Kind, got, c.want)
		}
	}
}

func TestBuildNav_ActiveMarksMatchingScreen(t *testing.T) {
	gs := BuildNav(scCluster, "orders-prod", DefaultEngines(), "waits")
	var activeCount int
	for _, g := range gs {
		for _, it := range g.Items {
			if it.Active {
				activeCount++
				if it.Screen != "waits" {
					t.Errorf("active item = %q, want waits", it.Screen)
				}
			}
		}
	}
	if activeCount != 1 {
		t.Errorf("active items = %d, want exactly 1", activeCount)
	}
	// empty active highlights nothing.
	for _, g := range BuildNav(scCluster, "orders-prod", DefaultEngines(), "") {
		for _, it := range g.Items {
			if it.Active {
				t.Errorf("active=\"\" must highlight nothing; %q is active", it.Screen)
			}
		}
	}
}

func TestBuildNav_ActiveAliases(t *testing.T) {
	// querydetail highlights Top Queries at cluster scope.
	if !itemActive(BuildNav(scCluster, "orders-prod", DefaultEngines(), "querydetail"), "topqueries") {
		t.Error("querydetail must highlight topqueries")
	}
	// scriptdetail highlights Saved Scripts (present at every scope; test pooler).
	if !itemActive(BuildNav(scPooler, "pgbouncer-1", DefaultEngines(), "scriptdetail"), "scripts") {
		t.Error("scriptdetail must highlight scripts")
	}
	// clusterdetail highlights the fleet Database › Clusters item, fleet only.
	if !itemActive(BuildNav(FleetScope(), "", DefaultEngines(), "clusterdetail"), "clusters") {
		t.Error("clusterdetail must highlight clusters at fleet scope")
	}
}

func TestBuildNav_HrefsCarryScope(t *testing.T) {
	gs := BuildNav(scCluster, "orders-prod", DefaultEngines(), "")
	for _, g := range gs {
		for _, it := range g.Items {
			if it.Href != NavHref(scCluster, it.Screen) {
				t.Errorf("item %q href = %q, want %q", it.Screen, it.Href, NavHref(scCluster, it.Screen))
			}
		}
	}
}
