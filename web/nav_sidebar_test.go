package web

import (
	"context"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func renderSidebar(t *testing.T, sc scope.Scope, label string, eng EngineFlags, active string) string {
	t.Helper()
	var sb strings.Builder
	if err := Sidebar(sc, label, eng, active).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render sidebar: %v", err)
	}
	return sb.String()
}

func TestSidebar_FleetRendersVerticalGroupsAndScriptsOnly(t *testing.T) {
	html := renderSidebar(t, FleetScope(), "", DefaultEngines(), "")
	for _, want := range []string{`class="ln-nav"`, `class="ln-nav-head"`, ">OVERVIEW<", ">DATABASE<", ">CONSOLE<", ">Saved Scripts<", ">Clusters<"} {
		if !strings.Contains(html, want) {
			t.Errorf("fleet sidebar missing %q", want)
		}
	}
	// low-level + SQL Console suppressed at fleet
	for _, banned := range []string{">QUERIES<", ">SQL Console<", ">Wait Events<"} {
		if strings.Contains(html, banned) {
			t.Errorf("fleet sidebar must not contain %q", banned)
		}
	}
}

func TestSidebar_ClusterActiveItemGetsActiveClassAndScopedHref(t *testing.T) {
	sc := scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}
	html := renderSidebar(t, sc, "orders-prod", DefaultEngines(), "waits")
	if !strings.Contains(html, "ln-nav-item--active") {
		t.Error("active cluster item missing ln-nav-item--active class")
	}
	if !strings.Contains(html, `href="/waits?scope=cluster`) {
		t.Error("Wait Events link missing scope-carrying href")
	}
	if !strings.Contains(html, "CLUSTER: ORDERS-PROD") {
		t.Error("cluster identity header not rendered")
	}
}

func TestSidebar_BadgesRendered(t *testing.T) {
	html := renderSidebar(t, scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}, "orders-prod", DefaultEngines(), "")
	if !strings.Contains(html, "ln-nav-badge--t2") || !strings.Contains(html, ">T2<") {
		t.Error("cluster sidebar missing T2 badge on SQL Console")
	}
	if !strings.Contains(html, ">SOON<") {
		t.Error("cluster sidebar missing SOON badge (Alerts/Connections)")
	}
}

func TestSidebar_NoHardcodedColors(t *testing.T) {
	html := renderSidebar(t, FleetScope(), "", DefaultEngines(), "")
	for _, banned := range []string{"#2b6cb0", "system-ui"} {
		if strings.Contains(html, banned) {
			t.Errorf("sidebar output contains hardcoded %q — styling must be class/token based", banned)
		}
	}
}
