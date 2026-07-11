package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/scope"
	"github.com/dobbo-ca/lynceus/web"
)

// handleClusterScopedOverview renders the canonical scoped cluster Overview at
// GET /cluster?scope=cluster:<id> — the screen the scope-driven sidebar's
// "Overview" nav points at. It lives inside the design Shell (query-based scope),
// leads with OPEN ISSUES ON THIS CLUSTER, and shows a health rollup, a stat
// strip, and per-node cards whose ⌖ button re-scopes to the node. T1 only.
func (s *Server) handleClusterScopedOverview(w http.ResponseWriter, r *http.Request) {
	sc := scope.Parse(r.URL.Query().Get("scope"))
	if sc.Kind != scope.Cluster {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)

	detail, found, err := fleetview.GetClusterDetail(ctx, s.conf, s.stats, sc.ClusterID, since, now)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}

	serverIDs, _ := s.conf.ServerIDsForCluster(ctx, sc.ClusterID)
	issues := s.buildScopeIssuesVM(ctx, "CLUSTER", true, sc.ClusterID, serverIDs, since, now)
	line, mod := rollupLine(issues)

	vm := web.ClusterOverviewVM{
		Name:       detail.Cluster.Name,
		Meta:       fmt.Sprintf("%d NODES · %d DATABASES", len(detail.Instances), detail.StreamCount),
		HealthLine: line,
		HealthMod:  mod,
		Issues:     issues,
		Stats: []web.ScopeStatVM{
			{Label: "Calls 24h", Value: fmt.Sprintf("%d", detail.Calls)},
			{Label: "Avg latency (ms)", Value: fmt.Sprintf("%.1f", detail.AvgLatencyMs)},
			{Label: "Active conns", Value: fmt.Sprintf("%d", detail.ActiveConns)},
			{Label: "Insights", Value: fmt.Sprintf("%d", detail.InsightCount)},
			{Label: "Databases", Value: fmt.Sprintf("%d", detail.StreamCount)},
		},
		Nodes: s.nodeCardsVM(ctx, &detail, since, now),
	}

	shell := s.buildShellView(r, "clusterdetail")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ClusterOverviewShell(shell, vm).Render(ctx, w)
}

// nodeCardsVM builds the per-node cards for the cluster Overview, deriving each
// node's health from its own firing checks/insights and its ⌖ href from the
// repointed ScopeHref (node scope).
func (s *Server) nodeCardsVM(ctx context.Context, d *fleetview.ClusterDetail, since, until time.Time) []web.ScopeNodeVM {
	out := make([]web.ScopeNodeVM, 0, len(d.Instances))
	for i := range d.Instances {
		it := d.Instances[i]
		roleMod, roleLabel := roleModLabel(it.Instance.Role)

		ids, _ := s.conf.ServerIDsForInstance(ctx, it.Instance.ID)
		nis, _ := fleetview.ScopeIssues(ctx, s.stats, ids, since, until)
		healthLabel, healthMod := nodeHealthLabel(fleetview.WorstSeverity(nis))

		var dbCount int
		for j := range it.Streams {
			if it.Streams[j].DatabaseName != "" {
				dbCount++
			}
		}

		out = append(out, web.ScopeNodeVM{
			Role:      roleLabel,
			RoleMod:   roleMod,
			Name:      it.Instance.Name,
			Meta:      fmt.Sprintf("%d calls · %d active conns · %d db", it.Calls, it.ActiveConns, dbCount),
			Health:    healthLabel,
			HealthMod: healthMod,
			ScopeHref: web.ScopeHref(scope.Scope{Kind: scope.Node, ClusterID: d.Cluster.ID, NodeID: it.Instance.ID}),
		})
	}
	return out
}

// handleCapabilitiesPage renders the scoped Capabilities screen at
// GET /capabilities?scope=... — the destination of the SQL-console gate's
// "REQUEST SESSION GRANT" CTA (which 404'd before this route existed). It lists
// declared capabilities and their discovered availability across the active
// scope's servers. The full matrix redesign is ly-4ov. T1: capability enums +
// package-authored reasons only.
func (s *Server) handleCapabilitiesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc := scope.Parse(r.URL.Query().Get("scope"))
	shell := s.buildShellView(r, "capabilities")

	var serverIDs []string
	switch sc.Kind {
	case scope.Cluster:
		serverIDs, _ = s.conf.ServerIDsForCluster(ctx, sc.ClusterID)
	case scope.Node:
		serverIDs, _ = s.conf.ServerIDsForInstance(ctx, sc.NodeID)
	case scope.Database:
		serverIDs, _ = s.conf.ServerIDsForCluster(ctx, sc.ClusterID)
	}

	vm := web.CapabilitiesVM{ScopeLabel: shell.ScopeLabel}
	if len(serverIDs) == 0 {
		vm.Empty = true
	} else {
		availByCap := map[string]bool{}
		for _, sid := range serverIDs {
			disc, err := s.disc.ListDiscoveredCapabilities(ctx, sid)
			if err != nil {
				continue
			}
			for _, dcap := range disc {
				if dcap.DatabaseName == "" && dcap.Available {
					availByCap[dcap.Capability] = true
				}
			}
		}
		for _, c := range caps.Declared() {
			name := string(c)
			vm.Caps = append(vm.Caps, web.CapabilityRowVM{Name: name, Available: availByCap[name]})
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.CapabilitiesShell(shell, vm).Render(ctx, w)
}

// buildScopeIssuesVM assembles the OPEN ISSUES view-model for a server set.
// clusterID is used only for insight deep-links.
func (s *Server) buildScopeIssuesVM(
	ctx context.Context, kind string, showServer bool, clusterID string,
	serverIDs []string, since, until time.Time,
) web.ScopedIssuesVM {
	issues, _ := fleetview.ScopeIssues(ctx, s.stats, serverIDs, since, until)
	vm := web.ScopedIssuesVM{ScopeKind: kind, ShowServer: showServer, Count: len(issues)}
	for i := range issues {
		vm.Issues = append(vm.Issues, web.ScopeIssueVM{
			Severity: issues[i].Severity,
			ID:       issues[i].ID,
			Detail:   issues[i].Detail,
			Server:   issues[i].Server,
			Age:      humanizeAge(issues[i].AgeMin),
			Href:     issueHref(clusterID, &issues[i]),
		})
	}
	return vm
}

// issueHref deep-links an issue to its explanation: insights -> the cluster
// query drilldown for the fingerprint; checks -> the checks screen. (Both routes
// already exist; check-expand deep targeting is owned by the Checks bead.)
func issueHref(clusterID string, is *fleetview.ScopeIssue) templ.SafeURL {
	if is.Kind == "insight" {
		return templ.SafeURL("/databases/" + clusterID + "/queries#drill-" + is.Ref)
	}
	return templ.SafeURL("/checks")
}

// rollupLine renders "[DEGRADED] n CRIT · n WARN" style summaries from the issues
// and the matching health modifier class.
func rollupLine(issues web.ScopedIssuesVM) (line, mod string) {
	var crit, warn, info int
	for _, is := range issues.Issues {
		switch is.Severity {
		case "crit":
			crit++
		case "warn":
			warn++
		default:
			info++
		}
	}
	switch {
	case crit > 0:
		return fmt.Sprintf("[DEGRADED] %d CRIT · %d WARN", crit, warn), "crit"
	case warn > 0:
		return fmt.Sprintf("[WARNING] %d WARN · %d INFO", warn, info), "warn"
	case info > 0:
		return fmt.Sprintf("[HEALTHY] %d INFO", info), "info"
	default:
		return "[HEALTHY] 0 OPEN", "ok"
	}
}

func humanizeAge(min int) string {
	switch {
	case min <= 0:
		return "—"
	case min < 60:
		return fmt.Sprintf("%dm", min)
	case min < 60*24:
		return fmt.Sprintf("%dh", min/60)
	default:
		return fmt.Sprintf("%dd", min/(60*24))
	}
}

// roleModLabel maps a store instance Role to (css modifier, display label). Role
// is "primary" | "replica" | "unknown"; unknown falls back to replica so the
// chip and .is-<role> class always resolve to a styled value.
func roleModLabel(role string) (mod, label string) {
	switch role {
	case "primary":
		return "primary", "PRIMARY"
	case "replica":
		return "replica", "REPLICA"
	default:
		return "replica", "REPLICA"
	}
}

func nodeHealthLabel(worst string) (label, mod string) {
	switch worst {
	case "crit":
		return "CRIT", "crit"
	case "warn":
		return "WARN", "warn"
	case "info":
		return "INFO", "info"
	default:
		return "OK", "ok"
	}
}
