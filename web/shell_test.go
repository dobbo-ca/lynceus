package web

import (
	"context"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func fleetShellView() ShellView {
	sc := scope.Scope{Kind: scope.Fleet}
	return ShellView{
		Scope:      sc,
		ScopeLabel: "FLEET",
		Scoped:     false,
		ClearHref:  "/",
		LogoHref:   "/",
		Range:      DefaultRange,
		Ranges:     RangeOptions(DefaultRange, sc, "fleet"),
		PollSecs:   3,
		Options: []ScopeOption{
			{Label: "orders-prod", Kind: "CLUSTER", Depth: 0, ScopeKey: "cluster:c-1", Href: "/?scope=cluster%3Ac-1"},
		},
		User:  ShellUser{Name: "dev-admin", Group: "DBA-ONCALL", T2Granted: true},
		Title: "Lynceus — Fleet",
	}
}

func renderShell(t *testing.T, vm ShellView) string {
	t.Helper()
	var sb strings.Builder
	if err := Shell(vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render shell: %v", err)
	}
	return sb.String()
}

func TestShell_NoExternalHosts(t *testing.T) {
	html := renderShell(t, fleetShellView())
	for _, host := range []string{"unpkg.com", "googleapis.com", "gstatic.com", "cdn.jsdelivr.net"} {
		if strings.Contains(html, host) {
			t.Errorf("shell references external host %q — assets must be self-hosted", host)
		}
	}
}

func TestShell_SelfHostedTokenAssetsNoLegacy(t *testing.T) {
	html := renderShell(t, fleetShellView())
	for _, want := range []string{
		`href="/static/css/tokens.css"`,
		`href="/static/css/shape.css"`,
		`href="/static/css/shell.css"`,
		`src="/static/js/htmx.min.js"`,
		`src="/static/js/theme.js"`,
		`src="/static/js/shell.js"`,
		`src="/static/js/onboarding.js"`,
		`data-theme="dark"`,
		"window.Lynceus",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("shell missing %q", want)
		}
	}
	if strings.Contains(html, "legacy.css") {
		t.Error("shell must NOT link legacy.css — new screens are token-based")
	}
}

func TestShell_LinksNavCSS(t *testing.T) {
	html := renderShell(t, fleetShellView())
	if !strings.Contains(html, `href="/static/css/nav.css"`) {
		t.Error("shell must link the self-hosted scope-rail stylesheet")
	}
}

func TestShell_RendersSidebarWhenSet(t *testing.T) {
	vm := fleetShellView()
	vm.Sidebar = Sidebar(FleetScope(), "", DefaultEngines(), "fleet")
	html := renderShell(t, vm)
	if !strings.Contains(html, `class="ln-nav"`) {
		t.Error("shell must render the supplied Sidebar component in the slot")
	}
	if strings.Contains(html, "sidebar-placeholder") {
		t.Error("shell must not render the placeholder sidebar when a Sidebar is supplied")
	}
}

func TestShell_TopBarChrome(t *testing.T) {
	html := renderShell(t, fleetShellView())
	for _, want := range []string{
		"LYNCEUS",              // wordmark
		"SCOPE:",               // picker button
		"FLEET",                // fleet chip label
		"15M", "1H", "24H", "7D", "30D", // range control
		"POLL",                // poll indicator
		"id=\"theme-toggle\"", // theme toggle
		"Audit Log",           // user menu governance item (live route)
		"GROUP: DBA-ONCALL",   // identity header
	} {
		if !strings.Contains(html, want) {
			t.Errorf("top bar missing %q", want)
		}
	}
	// Fleet scope: no ← FLEET reset present.
	if strings.Contains(html, "← FLEET") {
		t.Error("fleet scope must not show the ← FLEET reset")
	}
}

func TestShell_ScopedShowsResetAndAccentChip(t *testing.T) {
	sc := scope.Scope{Kind: scope.Cluster, ClusterID: "c-1"}
	vm := fleetShellView()
	vm.Scope, vm.Scoped, vm.ScopeLabel = sc, true, "orders-prod"
	vm.Ranges = RangeOptions(vm.Range, sc, "clusterdetail")
	html := renderShell(t, vm)
	if !strings.Contains(html, "← FLEET") {
		t.Error("scoped shell must show the ← FLEET reset")
	}
	if !strings.Contains(html, `data-scoped`) {
		t.Error("scoped chip must carry data-scoped for the accent style")
	}
	if !strings.Contains(html, "orders-prod") {
		t.Error("scoped chip must show the resolved scope label")
	}
}

func TestScopeOptionsList_rendersKindBadges(t *testing.T) {
	var sb strings.Builder
	opts := []ScopeOption{
		{Label: "orders-prod", Kind: "CLUSTER", Depth: 0, ScopeKey: "cluster:c-1", Href: "/?scope=cluster%3Ac-1"},
		{Label: "orders-prod / node-1", Kind: "NODE", Depth: 1, ScopeKey: "node:c-1:n-1", Href: "/?scope=node%3Ac-1%3An-1"},
		{Label: "orders-prod/orders", Kind: "DATABASE", Depth: 1, ScopeKey: "db:c-1:orders", Href: "/?scope=db%3Ac-1%3Aorders"}, // databases are cluster-level (Depth 1), not nested under a node
	}
	if err := ScopeOptionsList(opts, "orders").Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{`id="scope-options"`, "orders-prod / node-1", "CLUSTER", "NODE", "DATABASE", "orders-prod/orders"} {
		if !strings.Contains(html, want) {
			t.Errorf("options list missing %q", want)
		}
	}
}

func TestScopeOptionsList_emptyState(t *testing.T) {
	var sb strings.Builder
	if err := ScopeOptionsList(nil, "zzz").Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(sb.String(), "NO COMPONENTS MATCH") {
		t.Error("empty options must render the NO COMPONENTS MATCH state")
	}
}

func TestScopeButton_linksToScopeHref(t *testing.T) {
	var sb strings.Builder
	sc := scope.Scope{Kind: scope.Node, ClusterID: "c-1", NodeID: "n-1"}
	if err := ScopeButton(sc).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	if !strings.Contains(html, `href="/nodes?scope=node%3Ac-1%3An-1"`) {
		t.Errorf("scope button href wrong: %s", html)
	}
	if !strings.Contains(html, "⌖") {
		t.Error("scope button must render the ⌖ crosshair glyph")
	}
}
