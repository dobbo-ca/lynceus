package web

import (
	"context"
	"strings"
	"testing"
)

func renderFleetBody(t *testing.T, v FleetView) string {
	t.Helper()
	var sb strings.Builder
	if err := FleetBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func unhealthyFixture() FleetView {
	return FleetView{
		Row1: []FleetStat{{Label: "DATABASES", Value: "1", Sub: "clusters · 1 nodes · 2 databases"}},
		Row2: []FleetStat{
			{Label: "OPEN CRITICAL", Value: "2", Sub: "2 db", ValueClass: "fl-crit"},
			{Label: "OPEN WARN", Value: "1", Sub: "1 db", ValueClass: "fl-warn"},
			{Label: "OPEN INFO", Value: "0", Sub: "0 db", ValueClass: "fl-info"},
		},
		Attention: []FleetAttentionRow{
			{SevClass: "fl-sq-crit", ID: "settings.fsync", Detail: "fsync = off", Server: "srv-orders-primary", Age: "2h", Href: "/checks?scope=fv-srv-a&check=settings.fsync"},
		},
		AttnCrit: 2, AttnWarn: 1,
		Cards: []FleetClusterCard{
			{Name: "orders-prod", Version: "16.3", Engine: "POSTGRESQL", EngineIcon: "eng-pg",
				Health: "DEGRADED", HealthClass: "fl-crit", QPS: "1,284", LatencyMs: "18.2",
				Conns: "87", TopWait: "IO/DataFileRead", Crit: 2, Warn: 1, Info: 0, Href: "/databases/cl-1?scope=cl-1"},
		},
		HiddenLinks:   []FleetLink{{Label: "3 HEALTHY DB CLUSTERS NOT SHOWN →", Href: "/databases"}},
		RangeLabel:    "24H", Range: "24H", Sort: "health",
		EngineSummary: "1 DB CLUSTERS / RANGE 24H",
	}
}

func TestFleetBody_unhealthyRendersStripsAttentionAndCards(t *testing.T) {
	html := renderFleetBody(t, unhealthyFixture())
	for _, want := range []string{
		`id="fleet-body"`,
		"DATABASES", "OPEN CRITICAL", "OPEN WARN", "OPEN INFO",
		"NEEDS ATTENTION",
		"settings.fsync", "srv-orders-primary", "2h",
		`href="/checks?scope=fv-srv-a&amp;check=settings.fsync"`,
		"orders-prod", "v16.3", "[DEGRADED]", "POSTGRESQL", "#eng-pg",
		"2 CRIT", "1 WARN", "0 INFO",
		"3 HEALTHY DB CLUSTERS NOT SHOWN",
		`hx-get="/partial/fleet?sort=health&amp;range=24H"`, // auto-poll preserves sort+range
		`hx-get="/partial/fleet?sort=name&amp;range=24H"`,   // SORT toggle flips mode, keeps range
		"var(--", // tokens, not legacy
	} {
		if !strings.Contains(html, want) {
			t.Errorf("unhealthy fleet body missing %q", want)
		}
	}
	// explicitly-removed noise must NOT appear
	for _, forbidden := range []string{"<polyline", "components", "class=\"db-card\""} {
		if strings.Contains(html, forbidden) {
			t.Errorf("fleet card must not contain removed noise %q", forbidden)
		}
	}
}

func TestFleetBody_healthyShowsAllClearNoCards(t *testing.T) {
	v := FleetView{
		Row1: []FleetStat{{Label: "DATABASES", Value: "1", Sub: "clusters · 1 nodes · 1 databases"}},
		Row2: []FleetStat{
			{Label: "OPEN CRITICAL", Value: "0", Sub: "all clear", ValueClass: "fl-acc2"},
			{Label: "OPEN WARN", Value: "0", Sub: "no checks firing"},
			{Label: "OPEN INFO", Value: "0", Sub: "no advisories"},
		},
		Healthy: true, RangeLabel: "24H", Range: "24H", Sort: "health", EngineSummary: "1 DB CLUSTERS / RANGE 24H",
		HealthyLinks: []FleetLink{{Label: "1 DATABASE CLUSTER HEALTHY →", Href: "/databases"}},
	}
	html := renderFleetBody(t, v)
	if !strings.Contains(html, "ALL CLEAR") || !strings.Contains(html, "NO OPEN CHECKS OR INSIGHTS ACROSS ANY ENGINE") {
		t.Error("healthy fleet must show the all-clear panel")
	}
	if !strings.Contains(html, "DATABASE CLUSTER HEALTHY") || !strings.Contains(html, `href="/databases"`) {
		t.Error("all-clear panel must carry the per-vertical DB healthy link")
	}
	if strings.Contains(html, "NEEDS ATTENTION") {
		t.Error("healthy fleet must not show the Needs-Attention card")
	}
}
