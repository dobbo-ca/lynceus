package web

import (
	"context"
	"strings"
	"testing"
)

func renderConsoleBody(t *testing.T, vm ConsoleVM) string {
	t.Helper()
	var sb strings.Builder
	if err := ConsoleBody(vm).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func grantedVM() ConsoleVM {
	return ConsoleVM{
		ClusterID:   "c1",
		ClusterName: "orders-prod",
		Granted:     true,
		Grant:       ConsoleGrantVM{Group: "dba-oncall", Incident: "INC-2214", Expires: "3H 12M", ReadOnly: true, AuditHref: "/audit"},
		Picker: ConsolePickerVM{
			ClusterLabel: "orders-prod", GrantChip: "● SESSION GRANT ACTIVE", Granted: true,
			Nodes:     []ConsoleChip{{Label: "primary", Href: "/partial/console?node=primary", Selected: true}},
			Databases: []ConsoleChip{{Label: "appdb", Href: "/partial/console?db=appdb", Selected: true}},
		},
		Editor: ConsoleEditorVM{
			TargetName: "primary · db: appdb", Node: "primary", Database: "appdb",
			RowLimit: 500, TimeoutSecs: 5, Ready: true,
			RunHref: "/partial/console/run", SaveScriptsHref: "/scripts", SearchScriptsHref: "/partial/scripts/search",
		},
	}
}

func TestConsoleBody_grantedRendersBannerEditorTokens(t *testing.T) {
	html := renderConsoleBody(t, grantedVM())
	for _, want := range []string{
		`id="console-body"`,
		"SESSION GRANT ACTIVE",
		"dba-oncall",
		"INC-2214",
		"ROW LIMIT 500 · STATEMENT TIMEOUT 5S",
		"RUN ⌘↵",
		`data-console-run`,                       // ⌘↵ hook, present when Ready
		`hx-post="/partial/console/run"`,         // RUN wired when Ready
		"var(--acc)",                             // tokens, not legacy classes
		"var(--font-mono)",                       // mono data font via token
	} {
		if !strings.Contains(html, want) {
			t.Errorf("granted body missing %q", want)
		}
	}
	// console.js must NOT live inside #console-body (the HTMX swap unit): it is
	// loaded once by ConsolePage, outside the swap target, so it does not
	// re-execute and stack duplicate listeners on every body swap.
	if strings.Contains(html, "console.js") {
		t.Error("console.js must not be embedded in the #console-body swap unit")
	}
	if strings.Contains(html, "class=\"empty\"") {
		t.Error("must not use legacy component classes")
	}
}

func TestConsoleBody_runInertUntilResolved(t *testing.T) {
	vm := grantedVM()
	vm.Editor.Ready = false
	vm.Editor.TargetName = "(SELECT NODE & DATABASE ABOVE)"
	html := renderConsoleBody(t, vm)
	if strings.Contains(html, "data-console-run") || strings.Contains(html, `hx-post="/partial/console/run"`) {
		t.Error("RUN must be inert (no hx-post / no ⌘↵ hook) until node+database resolve")
	}
	if !strings.Contains(html, "(SELECT NODE &amp; DATABASE ABOVE)") {
		t.Error("editor should prompt for target when not ready")
	}
}

func TestConsoleBody_lockedShowsRequestGate(t *testing.T) {
	vm := ConsoleVM{
		ClusterID: "c2", ClusterName: "staging", Granted: false,
		CapabilitiesHref: "/capabilities?scope=cluster:c2",
		Picker:           ConsolePickerVM{ClusterLabel: "staging", GrantChip: "○ NO SESSION GRANT ON THIS CLUSTER"},
	}
	html := renderConsoleBody(t, vm)
	for _, want := range []string{
		"NO SESSION GRANT ON staging",
		"REQUEST SESSION GRANT →",
		`href="/capabilities?scope=cluster:c2"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("locked body missing %q", want)
		}
	}
	if strings.Contains(html, "RUN ⌘↵") {
		t.Error("locked state must not render the editor/RUN")
	}
}

func TestConsoleBody_fixedAxisChipIsInert(t *testing.T) {
	vm := grantedVM()
	// Node axis fixed (scope=node:…): the locked axis is a single inert chip
	// (empty Href); the free database axis carries the scope so the lock persists.
	vm.Picker.NodeFixed = true
	vm.Picker.Nodes = []ConsoleChip{{Label: "primary", Href: "", Selected: true}}
	vm.Picker.Databases = []ConsoleChip{{Label: "appdb", Href: "/partial/console?db=appdb&scope=node%3Ac1%3An1", Selected: true}}
	html := renderConsoleBody(t, vm)
	if !strings.Contains(html, "data-console-chip-fixed") {
		t.Error("fixed node chip must render inert (data-console-chip-fixed, no hx-get)")
	}
	if strings.Contains(html, `hx-get=""`) {
		t.Error("fixed node chip must not be a clickable hx-get link")
	}
	// The free database chip must carry the scope so a click preserves the fixed node.
	if !strings.Contains(html, "scope=node") {
		t.Error("free-axis chip href must preserve the locked axis (scope=node:…)")
	}
}

func TestConsoleBody_resultsPaginationAndExport(t *testing.T) {
	vm := grantedVM()
	vm.HasResult = true
	vm.Result = ConsoleResultVM{
		Columns:   []string{"relname", "n_dead_tup", "last_autovacuum"},
		Rows:      [][]string{{"orders", "182,431", "never"}},
		TotalRows: 54, DurationMs: 18.4, Hash: "6c1d…e44",
		PageLabel: "ROWS 1–25 OF 54",
		PrevHref:  "/partial/console?page=0", NextHref: "/partial/console?page=1",
		PrevActive: false, NextActive: true,
		PageSizes: []ConsolePageSize{{Label: "25", Href: "/partial/console?pagesize=25", Selected: true}},
		CopyTSV:   "relname\tn_dead_tup\tlast_autovacuum\norders\t182,431\tnever",
		CsvHref:   "/console/export?format=csv", SqlHref: "/console/export?format=sql",
	}
	html := renderConsoleBody(t, vm)
	for _, want := range []string{
		"T2 READ LOGGED · 6c1d…e44",
		"ROWS 1–25 OF 54",
		"↓ CSV", "↓ SQL", "⧉ COPY",
		`href="/console/export?format=csv"`,
		`id="console-copy-src"`, // hidden copy payload for console.js
		"data-console-copy",
		"RELNAME", "N_DEAD_TUP", "LAST_AUTOVACUUM", // uppercased header labels
	} {
		if !strings.Contains(html, want) {
			t.Errorf("result body missing %q", want)
		}
	}
}
