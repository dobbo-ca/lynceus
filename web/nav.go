package web

import (
	"net/url"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

// NavItem is one link in the scope-driven sidebar.
type NavItem struct {
	Label  string
	Screen string // design screen id (key into screenPath)
	Href   string
	Active bool
	Soon   bool
	T2     bool
}

// ItemClass is the CSS class list for the item's <a>. Styling lives in
// web/static/css/nav.css (tokens only).
func (it NavItem) ItemClass() string {
	c := "ln-nav-item"
	if it.Active {
		c += " ln-nav-item--active"
	}
	if it.Soon {
		c += " ln-nav-item--soon"
	}
	return c
}

// NavGroup is a labelled section of the sidebar (e.g. "QUERIES").
type NavGroup struct {
	Label string
	Items []NavItem
}

// screenPath maps a design screen id to its canonical route. It is the single
// source of truth for scoped-section URLs — verticals (ly-ae6.4..11) register
// their route to match these. Not-yet-built routes 404 until their owning bead
// ships; the nav links ahead to them intentionally.
var screenPath = map[string]string{
	// fleet + database vertical
	"fleet":     "/fleet",         // fleet landing shell (ly-ae6.2; body ly-ae6.4)
	"clusters":  "/databases",     // Database › Clusters list (exists)
	"nodes":     "/nodes",         // Database › Nodes (ly-ae6.5)
	"databases": "/databases/all", // Database › Databases list (ly-ae6.5)
	// search vertical (ly-ae6.10)
	"searchdomains": "/search/domains",
	"searchnodes":   "/search/nodes",
	// cache vertical (ly-ae6.11)
	"cacheclusters":    "/cache/clusters",
	"cachereplicasets": "/cache/replicasets",
	"cachenodes":       "/cache/nodes",
	// scoped identity + capabilities
	"clusterdetail": "/cluster",      // scoped cluster overview (ly-ae6.6)
	"capabilities":  "/capabilities", // ly-ae6.6
	// queries
	"topqueries": "/",         // top-queries (exists at /)
	"insights":   "/insights", // exists
	"plans":      "/plan",     // exists
	// advisors (exist)
	"indexadvisor":  "/index-advisor",
	"vacuumadvisor": "/vacuum-advisor",
	"configadvisor": "/config-advisor",
	// activity
	"waits":       "/waits",       // exists
	"connections": "/connections", // SOON
	// console
	"console": "/console", // SQL Console T2 (ly-ae6.8)
	"scripts": "/scripts", // Saved Scripts (ly-ae6.9)
	// checks & alerts
	"checks": "/checks", // exists
	"alerts": "/alerts", // SOON
	// schema (all SOON)
	"inventory":   "/schema/inventory",
	"tablegrowth": "/schema/table-growth",
	"indexes":     "/schema/indexes",
	// logs (SOON)
	"loginsights": "/logs/insights",
}

// NavHref returns screen's canonical route with the current scope encoded as the
// single "scope" query param ly-ae6.2's request resolver reads back
// (scope.Parse(qv.Get("scope"))). Fleet scope carries no param, matching the
// ScopeHref/RangeOptions convention.
func NavHref(sc scope.Scope, screen string) string {
	p, ok := screenPath[screen]
	if !ok {
		p = "/"
	}
	if sc.IsFleet() {
		return p
	}
	return p + "?" + url.Values{"scope": {sc.Encode()}}.Encode()
}

// isActive reports whether the nav item for `screen` should render active given
// the shell's `active` screen id and the current scope. It encodes the exact
// match plus the prototype's detail-screen aliases (Lynceus.dc.html:2411):
// querydetail→topqueries, scriptdetail→scripts, and (fleet only)
// clusterdetail→clusters. An empty `active` highlights nothing.
func isActive(screen, active string, sc scope.Scope) bool {
	if active == "" {
		return false
	}
	if screen == active {
		return true
	}
	switch {
	case screen == "topqueries" && active == "querydetail":
		return true
	case screen == "scripts" && active == "scriptdetail":
		return true
	case screen == "clusters" && active == "clusterdetail" && sc.IsFleet():
		return true
	}
	return false
}
