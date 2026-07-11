package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/web"
)

// handleFleetPartial renders just the #fleet-body fragment for HTMX refresh.
func (s *Server) handleFleetPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.FleetBody(s.fetchFleet(r)).Render(r.Context(), w)
}

// fetchFleet builds the domain fleet view and maps it onto the presentation
// view-model: range parsing (reusing web.ParseRange so the header + since window
// track the shell's shared ?range=), age formatting, sev->class, engine-neutral
// stat strips (gated), scope-aware deep links, the problem-only card filter +
// sort, and the healthy/hidden footer links. A BuildFleetView error surfaces an
// explicit error panel — never a false all-clear.
func (s *Server) fetchFleet(r *http.Request) web.FleetView {
	sortMode := r.URL.Query().Get("sort")
	if sortMode != "name" {
		sortMode = "health"
	}
	now := time.Now().UTC()
	// The shell's canonical UPPER range label (15M|1H|24H|7D|30D, default 24H) is
	// both the header label and the echoed poll/toggle param.
	rangeLabel := web.ParseRange(r.URL.Query().Get("range"))
	since := fleetSince(rangeLabel, now)

	// Postgres-only today. The real per-engine enable source is a fleet-config
	// concern wired WITH the Search/Cache verticals in ly-ae6.10 / ly-ae6.11;
	// here both gates are default-off so Row1/Row2 stay db-only and neutral.
	const enableSearch, enableCache = false, false

	dv, err := fleetview.BuildFleetView(r.Context(), s.conf, s.stats, since, now.Add(time.Minute))
	if err != nil {
		// Surface an explicit error state, NOT Healthy:true — a DB/backend blip
		// must never render as the green all-clear. The 30s poll keeps retrying.
		log.Printf("fleet: BuildFleetView: %v", err)
		return web.FleetView{
			LoadError:     true,
			Sort:          sortMode,
			Range:         rangeLabel,
			RangeLabel:    rangeLabel,
			EngineSummary: "FLEET DATA UNAVAILABLE",
		}
	}

	v := web.FleetView{
		Sort:       sortMode,
		Range:      rangeLabel,
		RangeLabel: rangeLabel,
		Healthy:    dv.Healthy,
	}
	v.EngineSummary = fmt.Sprintf("%d DB CLUSTERS / RANGE %s", dv.ClusterCount, rangeLabel)

	// row 1: engine-neutral counts. DATABASES always; SEARCH/CACHE only when
	// their gate is on (both off today — see the const above).
	v.Row1 = []web.FleetStat{{
		Label: "DATABASES",
		Value: fmt.Sprintf("%d", dv.ClusterCount),
		Sub:   fmt.Sprintf("clusters · %d nodes · %d databases", dv.NodeCount, dv.DatabaseCount),
	}}
	if enableSearch {
		v.Row1 = append(v.Row1, web.FleetStat{Label: "SEARCH", Value: "0", Sub: "no domains"})
	}
	if enableCache {
		v.Row1 = append(v.Row1, web.FleetStat{Label: "CACHE", Value: "0", Sub: "no clusters"})
	}

	// row 2: open crit/warn/info with engine-neutral subs (db-only today).
	if dv.Healthy {
		v.Row2 = []web.FleetStat{
			{Label: "OPEN CRITICAL", Value: "0", Sub: "all clear", ValueClass: "fl-acc2"},
			{Label: "OPEN WARN", Value: "0", Sub: "no checks firing"},
			{Label: "OPEN INFO", Value: "0", Sub: "no advisories"},
		}
	} else {
		v.Row2 = []web.FleetStat{
			{Label: "OPEN CRITICAL", Value: fmt.Sprintf("%d", dv.OpenCrit), Sub: openSub(dv.OpenCrit, enableSearch, enableCache), ValueClass: "fl-crit"},
			{Label: "OPEN WARN", Value: fmt.Sprintf("%d", dv.OpenWarn), Sub: openSub(dv.OpenWarn, enableSearch, enableCache), ValueClass: "fl-warn"},
			{Label: "OPEN INFO", Value: fmt.Sprintf("%d", dv.OpenInfo), Sub: openSub(dv.OpenInfo, enableSearch, enableCache), ValueClass: "fl-info"},
		}
	}

	// needs-attention rows
	for i := range dv.Attention {
		a := &dv.Attention[i]
		v.Attention = append(v.Attention, web.FleetAttentionRow{
			SevClass: sevSquareClass(a.Sev),
			ID:       a.ID,
			Detail:   a.Detail,
			Server:   a.ServerName,
			Age:      formatAge(now.Sub(a.At)),
			Href:     attentionHref(*a),
		})
		switch a.Sev {
		case fleetview.SevCrit:
			v.AttnCrit++
		case fleetview.SevWarn:
			v.AttnWarn++
		}
	}

	// problem-only cluster cards (crit||warn > 0), sorted, with hidden-healthy count
	shown := make([]fleetview.FleetCluster, 0, len(dv.Clusters))
	for i := range dv.Clusters {
		c := dv.Clusters[i]
		if c.Crit > 0 || c.Warn > 0 {
			shown = append(shown, c)
		}
	}
	// footer only on a NON-all-clear board (matches prototype `!healthyFleet &&
	// hiddenHealthy > 0`); on a healthy fleet the all-clear panel's HealthyLinks
	// already links to all clusters, so a "NOT SHOWN" footer would be redundant.
	if hidden := len(dv.Clusters) - len(shown); hidden > 0 && !dv.Healthy {
		v.HiddenLinks = []web.FleetLink{{
			Label: fmt.Sprintf("%d HEALTHY DB %s NOT SHOWN →", hidden, clustersNoun(hidden)),
			Href:  "/databases",
		}}
	}
	// all-clear panel per-vertical healthy link (DB today; search/cache when enabled)
	if dv.Healthy && dv.ClusterCount > 0 {
		v.HealthyLinks = []web.FleetLink{{
			Label: fmt.Sprintf("%d DATABASE %s HEALTHY →", dv.ClusterCount, clustersNoun(dv.ClusterCount)),
			Href:  "/databases",
		}}
	}
	sortClusters(shown, sortMode)
	for i := range shown {
		c := &shown[i]
		v.Cards = append(v.Cards, web.FleetClusterCard{
			Name:         c.Name,
			Version:      c.Version,
			Provider:     c.Provider,
			ProviderName: c.ProviderName,
			Engine:       c.Engine,
			EngineIcon:   c.EngineIcon,
			Health:       c.Health,
			HealthClass:  healthTextClass(c.HealthSev),
			QPS:          fmt.Sprintf("%.0f", c.QPS),
			LatencyMs:    fmt.Sprintf("%.1f", c.LatencyMs),
			Conns:        fmt.Sprintf("%d", c.ActiveConns),
			TopWait:      dashIfEmpty(c.TopWait),
			Crit:         c.Crit,
			Warn:         c.Warn,
			Info:         c.Info,
			Href:         "/databases/" + c.ClusterID + "?scope=" + c.ClusterID,
		})
	}
	return v
}

// sortClusters orders the problem cards: "health" = crit-band first then name;
// "name" = alphabetical.
func sortClusters(cs []fleetview.FleetCluster, mode string) {
	less := func(i, j int) bool { return cs[i].Name < cs[j].Name }
	if mode != "name" {
		less = func(i, j int) bool {
			ri, rj := healthRank(cs[i]), healthRank(cs[j])
			if ri != rj {
				return ri < rj
			}
			return cs[i].Name < cs[j].Name
		}
	}
	sortSlice(cs, less)
}

func healthRank(c fleetview.FleetCluster) int {
	if c.Crit > 0 {
		return 0
	}
	if c.Warn > 0 {
		return 1
	}
	return 2
}

func attentionHref(a fleetview.AttentionItem) string {
	scope := "?scope=" + a.ServerID
	if a.Kind == "insight" {
		// Navigable insights page (the per-query drilldown is a /partial/ route
		// only); carry the fingerprint as a hint for ly-ae6.6 to auto-expand.
		return "/databases/" + a.ClusterID + "/insights" + scope + "&fp=" + a.Fingerprint
	}
	if a.Category == "vacuum" || strings.HasPrefix(a.CheckID, "vacuum.") {
		return "/vacuum-advisor" + scope
	}
	return "/checks" + scope + "&check=" + a.CheckID
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		m := int(d.Minutes())
		if m < 1 {
			m = 1
		}
		return fmt.Sprintf("%dm", m)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func sevSquareClass(s fleetview.Sev) string {
	switch s {
	case fleetview.SevCrit:
		return "fl-sq-crit"
	case fleetview.SevWarn:
		return "fl-sq-warn"
	default:
		return "fl-sq-info"
	}
}

func healthTextClass(s fleetview.Sev) string {
	switch s {
	case fleetview.SevCrit:
		return "fl-crit"
	case fleetview.SevWarn:
		return "fl-warn"
	default:
		return "fl-ok"
	}
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// fleetSince maps the shell's canonical UPPER range label (fed by ly-ae6.2's
// top-bar segmented control via web.ParseRange) to the since-window start.
// Default (and unknown) -> 24H. The label set matches web.ValidRanges.
func fleetSince(label string, now time.Time) time.Time {
	switch label {
	case "15M":
		return now.Add(-15 * time.Minute)
	case "1H":
		return now.Add(-time.Hour)
	case "7D":
		return now.AddDate(0, 0, -7)
	case "30D":
		return now.AddDate(0, 0, -30)
	default: // 24H
		return now.AddDate(0, 0, -1)
	}
}

// openSub renders a Row2 severity sub, engine-neutral: db always; search/cache
// only when their vertical is enabled (both off today, so subs read "%d db").
func openSub(db int, enableSearch, enableCache bool) string {
	sub := fmt.Sprintf("%d db", db)
	if enableSearch {
		sub += " · 0 search"
	}
	if enableCache {
		sub += " · 0 cache"
	}
	return sub
}

// clustersNoun pluralizes CLUSTER/CLUSTERS for the footer + all-clear links.
func clustersNoun(n int) string {
	if n == 1 {
		return "CLUSTER"
	}
	return "CLUSTERS"
}

// sortSlice is a small stable insertion sort over the fleet's clusters (N is
// small); keeps this file dependency-free.
func sortSlice(cs []fleetview.FleetCluster, less func(i, j int) bool) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}
