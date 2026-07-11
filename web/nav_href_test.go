package web

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func TestNavHref_EncodesScope(t *testing.T) {
	// Scope travels in the same single "scope" query param ly-ae6.2's resolver
	// reads (scope.Encode/Parse). url.Values.Encode escapes ':' to %3A. Fleet
	// scope carries no param (matches ScopeHref/RangeOptions).
	cluster := scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}
	node := scope.Scope{Kind: scope.Node, ClusterID: "c-1", NodeID: "n-1"}
	cases := []struct {
		name, screen string
		sc           scope.Scope
		want         string
	}{
		{"fleet overview", "fleet", FleetScope(), "/fleet"},
		{"fleet clusters", "clusters", FleetScope(), "/databases"},
		{"cluster waits", "waits", cluster, "/waits?scope=cluster%3Ac-1"},
		{"cluster sql console", "console", cluster, "/console?scope=cluster%3Ac-1"},
		{"node waits", "waits", node, "/waits?scope=node%3Ac-1%3An-1"},
	}
	for _, c := range cases {
		if got := NavHref(c.sc, c.screen); got != c.want {
			t.Errorf("%s: NavHref = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNavHref_UnknownScreenFallsBackToRoot(t *testing.T) {
	if got := NavHref(FleetScope(), "does-not-exist"); got != "/" {
		t.Errorf("unknown screen: NavHref = %q, want /", got)
	}
}

func TestNavItem_ItemClass(t *testing.T) {
	cases := []struct {
		it   NavItem
		want string
	}{
		{NavItem{}, "ln-nav-item"},
		{NavItem{Active: true}, "ln-nav-item ln-nav-item--active"},
		{NavItem{Soon: true}, "ln-nav-item ln-nav-item--soon"},
		{NavItem{Active: true, Soon: true}, "ln-nav-item ln-nav-item--active ln-nav-item--soon"},
	}
	for _, c := range cases {
		if got := c.it.ItemClass(); got != c.want {
			t.Errorf("ItemClass(%+v) = %q, want %q", c.it, got, c.want)
		}
	}
}

func TestIsActive_ExactAndAliases(t *testing.T) {
	cl := scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}
	pl := scope.Scope{Kind: scope.Pooler, PoolerID: "p-1"}
	fl := FleetScope()
	cases := []struct {
		name, screen, active string
		sc                   scope.Scope
		want                 bool
	}{
		{"empty active highlights nothing", "waits", "", cl, false},
		{"exact match", "waits", "waits", cl, true},
		{"querydetail aliases topqueries", "topqueries", "querydetail", cl, true},
		{"scriptdetail aliases scripts", "scripts", "scriptdetail", pl, true},
		{"clusterdetail aliases clusters at fleet", "clusters", "clusterdetail", fl, true},
		{"clusterdetail does NOT alias clusters off fleet", "clusters", "clusterdetail", cl, false},
		{"no cross-alias", "waits", "querydetail", cl, false},
	}
	for _, c := range cases {
		if got := isActive(c.screen, c.active, c.sc); got != c.want {
			t.Errorf("%s: isActive(%q,%q,%v) = %v, want %v", c.name, c.screen, c.active, c.sc.Kind, got, c.want)
		}
	}
}
