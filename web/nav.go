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
	"fleet":     "/",              // fleet landing shell (ly-ae6.2; body ly-ae6.4)
	"clusters":  "/clusters",  // Database › Clusters list (exists)
	"nodes":     "/nodes",     // Database › Nodes (ly-ae6.5)
	"databases": "/databases", // Database › Databases list (ly-ae6.5)
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
	"topqueries": "/queries",  // legacy global top-queries (root is now the fleet shell)
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

// navItem builds a NavItem, resolving its href and active state (via isActive,
// including the prototype's detail-screen aliases) and applying "soon"/"t2".
func navItem(sc scope.Scope, active, label, screen string, flags ...string) NavItem {
	it := NavItem{
		Label:  label,
		Screen: screen,
		Href:   NavHref(sc, screen),
		Active: isActive(screen, active, sc),
	}
	for _, f := range flags {
		switch f {
		case "soon":
			it.Soon = true
		case "t2":
			it.T2 = true
		}
	}
	return it
}

// BuildNav returns the sidebar nav tree for the current scope. It mirrors the
// prototype's navDef builder (docs/design/Lynceus.dc.html:2366-2406) and
// enforces the three gating rules: low-level sections never appear at fleet;
// Saved Scripts at every scope; SQL Console only at cluster/node/database scope.
// label is the resolved display name for the scope (the shell threads it in via
// ShellView.ScopeLabel), used only for the scoped identity-group header.
func BuildNav(sc scope.Scope, label string, eng EngineFlags, active string) []NavGroup {
	itm := func(itemLabel, screen string, flags ...string) NavItem {
		return navItem(sc, active, itemLabel, screen, flags...)
	}
	hdr := headerLabel(sc, label)

	// shared low-level groups (never used at fleet scope)
	queries := NavGroup{Label: "QUERIES", Items: []NavItem{
		itm("Top Queries", "topqueries"),
		itm("Query Insights", "insights"),
		itm("Plans", "plans"),
	}}
	advisors := func(configLabel string) NavGroup {
		items := []NavItem{itm("Index", "indexadvisor"), itm("Vacuum", "vacuumadvisor")}
		if configLabel != "" {
			items = append(items, itm(configLabel, "configadvisor"))
		}
		return NavGroup{Label: "ADVISORS", Items: items}
	}
	activityFull := NavGroup{Label: "ACTIVITY", Items: []NavItem{
		itm("Wait Events", "waits"),
		itm("Connections", "connections", "soon", "t2"),
	}}
	consoleFull := NavGroup{Label: "CONSOLE", Items: []NavItem{
		itm("SQL Console", "console", "t2"),
		itm("Saved Scripts", "scripts"),
	}}
	consoleScriptsOnly := NavGroup{Label: "CONSOLE", Items: []NavItem{
		itm("Saved Scripts", "scripts"),
	}}
	checksFull := NavGroup{Label: "CHECKS & ALERTS", Items: []NavItem{
		itm("Checks", "checks"),
		itm("Alerts", "alerts", "soon"),
	}}
	schema := NavGroup{Label: "SCHEMA", Items: []NavItem{
		itm("Inventory", "inventory", "soon"),
		itm("Table Growth", "tablegrowth", "soon"),
		itm("Indexes", "indexes", "soon"),
	}}
	logs := NavGroup{Label: "LOGS", Items: []NavItem{
		itm("Log Insights", "loginsights", "soon"),
	}}

	switch sc.Kind {
	case scope.Cluster:
		return []NavGroup{
			{Label: hdr, Items: []NavItem{
				itm("Overview", "clusterdetail"),
				itm("Nodes", "nodes"),
				itm("Databases", "databases"),
				itm("Capabilities", "capabilities"),
			}},
			queries,
			advisors("Config · per node"),
			activityFull, consoleFull, checksFull, schema, logs,
		}
	case scope.Node:
		return []NavGroup{
			{Label: hdr, Items: []NavItem{
				itm("Overview", "nodes"),
				itm("Config", "configadvisor"),
				itm("Capabilities", "capabilities"),
			}},
			queries,
			advisors(""),
			activityFull, consoleFull, checksFull, logs,
		}
	case scope.Pooler:
		return []NavGroup{
			{Label: hdr, Items: []NavItem{
				itm("Overview", "nodes"),
				itm("Config · pgbouncer", "configadvisor"),
			}},
			{Label: "ACTIVITY", Items: []NavItem{itm("Connections", "connections", "soon", "t2")}},
			consoleScriptsOnly, checksFull, logs,
		}
	case scope.Database:
		return []NavGroup{
			{Label: hdr, Items: []NavItem{
				itm("Overview", "databases"),
				itm("Capabilities", "capabilities"),
			}},
			queries,
			advisors(""),
			consoleFull,
			{Label: "CHECKS & ALERTS", Items: []NavItem{itm("Checks", "checks")}},
			schema,
		}
	default: // fleet — low-level sections suppressed; verticals gate on engines
		groups := []NavGroup{
			{Label: "OVERVIEW", Items: []NavItem{itm("Fleet", "fleet")}},
		}
		if eng.Postgres {
			groups = append(groups, NavGroup{Label: "DATABASE", Items: []NavItem{
				itm("Clusters", "clusters"),
				itm("Nodes", "nodes"),
				itm("Databases", "databases"),
			}})
		}
		if eng.Search {
			groups = append(groups, NavGroup{Label: "SEARCH", Items: []NavItem{
				itm("Domains", "searchdomains"),
				itm("Nodes", "searchnodes"),
			}})
		}
		if eng.Cache {
			groups = append(groups, NavGroup{Label: "CACHE", Items: []NavItem{
				itm("Clusters", "cacheclusters"),
				itm("Replicasets", "cachereplicasets"),
				itm("Nodes", "cachenodes"),
			}})
		}
		return append(groups, consoleScriptsOnly)
	}
}
