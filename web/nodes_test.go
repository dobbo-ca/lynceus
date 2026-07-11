package web

import (
	"context"
	"strings"
	"testing"
)

func renderNodes(t *testing.T, v NodesView) string {
	t.Helper()
	var sb strings.Builder
	if err := NodesBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestNodesBody_GroupAndRow(t *testing.T) {
	html := renderNodes(t, NodesView{Groups: []NodeGroupVM{{
		Name: "orders-prod", EngineIcon: "eng-pg", EngineName: "POSTGRES",
		Version: "16.3", Provider: "SELF-HOSTED", ProviderNote: "Collector on host",
		Rollup: "NODE HEALTH 1 CRIT · 2 OK → CLUSTER DEGRADED", RollupClass: "hl-crit",
		ScopeHref: "/?scope=cluster%3Ac1",
		Nodes: []NodeRowVM{{
			Role: "PRIMARY", RoleClass: "role-primary", Name: "srv-orders-primary",
			Version: "16.3", Source: "collector on host · node + pg stats",
			CPU: "—", Mem: "—", Disk: "—", IOWait: "—",
			Conns: "87 / 200", ConnsPct: "44%", Health: "● CRIT", HealthClass: "hl-crit",
			ScopeHref: "/?scope=node%3Ac1%3Ai1",
		}},
	}}})
	for _, want := range []string{
		`id="nodes-screen"`,
		`href="#eng-pg"`, `orders-prod`, `v16.3`,
		`SELF-HOSTED`, `⌖`,
		`NODE HEALTH 1 CRIT · 2 OK → CLUSTER DEGRADED`,
		`class="tbl-scroll"`, `class="nodes-grid"`,
		`role-primary`, `srv-orders-primary`,
		`collector on host · node + pg stats`,
		`87 / 200`, `width:44%`,
		`● CRIT`,
		`href="/?scope=node%3Ac1%3Ai1"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("NodesBody missing %q", want)
		}
	}
}

func TestNodesBody_BlindSpotDimmed(t *testing.T) {
	html := renderNodes(t, NodesView{Groups: []NodeGroupVM{{
		Name: "analytics-stage", EngineIcon: "eng-pg", Provider: "AWS RDS · MULTI-AZ",
		Nodes: []NodeRowVM{{
			Role: "STANDBY", RoleClass: "role-standby", Name: "analytics-standby-az2",
			NameBlind: true, Source: "no endpoint — CloudWatch instance metrics only",
			CPU: "—", Mem: "—", Disk: "—", IOWait: "—", Conns: "— / —", ConnsPct: "0%",
			Health: "◌ BLIND SPOT", HealthClass: "hl-warn",
		}},
	}}})
	if !strings.Contains(html, "◌ BLIND SPOT") {
		t.Fatal("blind-spot health text missing")
	}
	if !strings.Contains(html, "node-name--blind") {
		t.Fatal("blind-spot node name is not dimmed")
	}
	if !strings.Contains(html, "no endpoint — CloudWatch instance metrics only") {
		t.Fatal("blind-spot data-source line missing")
	}
}

func TestNodesBody_EmptySearch(t *testing.T) {
	html := renderNodes(t, NodesView{NoResults: true})
	if !strings.Contains(html, "NO CLUSTERS OR NODES MATCH") {
		t.Fatal("empty-search state missing")
	}
}
