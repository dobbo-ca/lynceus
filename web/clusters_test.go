package web

import (
	"context"
	"strings"
	"testing"
)

func renderClusters(t *testing.T, v ClustersView) string {
	t.Helper()
	var sb strings.Builder
	if err := ClustersBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestClustersBody_RowAnatomy(t *testing.T) {
	html := renderClusters(t, ClustersView{Rows: []ClusterListRow{{
		Name: "orders-prod", EngineIcon: "eng-pg", EngineName: "POSTGRES",
		Version: "16.3", Meta: "3 INSTANCES · 4 STREAMS", QPS: "1,284",
		HealthText: "[DEGRADED] 1 CRIT · 4 WARN", HealthClass: "hl-crit", SevRank: 2,
		ScopeHref: "/cluster?scope=cluster%3Ac1",
	}}})
	for _, want := range []string{
		`id="clusters-screen"`,
		`href="#eng-pg"`, // engine sprite chip
		`class="cl-name"`, `orders-prod`,
		`v16.3`,                   // version chip
		`3 INSTANCES · 4 STREAMS`, // faint meta
		`1,284 QPS`,
		`[DEGRADED] 1 CRIT · 4 WARN`,
		`hl-crit`,
		`class="scope-btn"`, `href="/cluster?scope=cluster%3Ac1"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ClustersBody missing %q", want)
		}
	}
	// Rows must NOT be whole-row links, and no sparkline/cards remain.
	if strings.Contains(html, `<polyline`) || strings.Contains(html, `class="db-card"`) {
		t.Error("ClustersBody still renders a sparkline or legacy card")
	}
}

func TestClustersBody_VersionOmittedWhenBlank(t *testing.T) {
	html := renderClusters(t, ClustersView{Rows: []ClusterListRow{{
		Name: "x", EngineIcon: "eng-pg", HealthClass: "hl-ok",
	}}})
	if strings.Contains(html, `class="cl-ver"`) {
		t.Error("version chip must be omitted when Version is empty")
	}
}
