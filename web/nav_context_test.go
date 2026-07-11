package web

import (
	"context"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func renderFromContext(t *testing.T, ctx context.Context) string {
	t.Helper()
	var sb strings.Builder
	if err := SidebarFromContext().Render(ctx, &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestNavStateFromContext_DefaultsToFleet(t *testing.T) {
	ns := NavStateFromContext(context.Background())
	if !ns.Scope.IsFleet() || !ns.Engines.Postgres {
		t.Errorf("default NavState = %+v, want fleet scope + DefaultEngines", ns)
	}
}

func TestSidebarFromContext_DefaultRendersFleet(t *testing.T) {
	html := renderFromContext(t, context.Background())
	if !strings.Contains(html, ">OVERVIEW<") {
		t.Error("no-NavState context must render the fleet tree (OVERVIEW group)")
	}
	if strings.Contains(html, "CLUSTER:") {
		t.Error("no-NavState context must not render a scoped identity header")
	}
}

func TestSidebarFromContext_ThreadsResolvedScope(t *testing.T) {
	ctx := WithNavState(context.Background(), NavState{
		Scope:   scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"},
		Label:   "orders-prod",
		Engines: DefaultEngines(),
		Active:  "waits",
	})
	html := renderFromContext(t, ctx)
	if !strings.Contains(html, "CLUSTER: ORDERS-PROD") {
		t.Error("SidebarFromContext must render the scope put on the context (not hardcoded fleet)")
	}
	if !strings.Contains(html, "ln-nav-item--active") {
		t.Error("SidebarFromContext must thread the active screen from context")
	}
}
