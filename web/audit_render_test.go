package web

import (
	"context"
	"strings"
	"testing"
)

func renderAuditPage(t *testing.T, chain AuditChain, rows []AuditRow) string {
	t.Helper()
	var sb strings.Builder
	if err := AuditPage(ShellView{}, chain, AuditFilterValues{}, rows).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestAuditPage_bannerAndColumns(t *testing.T) {
	// NOTE: pass a non-empty row set. AuditTable renders the header grid
	// (>TARGET</>HASH<) only in the len(rows)>0 branch; with nil rows it emits
	// just the "No audit entries" note and the header asserts below would FAIL.
	rows := []AuditRow{
		{ID: 1, Actor: "alice", Action: "read", ServerID: "srv-1", DataTier: 2, Target: "orders.pg_stat_statements", HashShort: "a71f…9c2", IsT2: true, At: "2026-07-10T00:00:00Z"},
	}
	html := renderAuditPage(t, AuditChain{Verified: true, TipShort: "c4b7…2ef", Count: 42}, rows)
	for _, want := range []string{
		`href="/static/css/governance.css"`,
		`class="badge badge--live"`,
		"HASH CHAIN VERIFIED",
		"c4b7…2ef",
		"42 EVENTS",
		"TAMPER-EVIDENT",
		">TARGET<", ">HASH<", // new column headers (require ≥1 row to render)
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audit page missing %q", want)
		}
	}
}

func TestAuditPage_brokenBanner(t *testing.T) {
	html := renderAuditPage(t, AuditChain{Verified: false, TipShort: "dead…f00", Count: 9}, nil)
	if !strings.Contains(html, "chain-banner--broken") || !strings.Contains(html, "HASH CHAIN BROKEN") {
		t.Error("broken chain must render the crit banner variant")
	}
}

func TestAuditTable_t2Striping(t *testing.T) {
	rows := []AuditRow{
		{ID: 2, Actor: "alice", Action: "read", ServerID: "srv-1", DataTier: 2, Target: "orders.pg_stat_statements", HashShort: "a71f…9c2", IsT2: true, At: "2026-07-10T00:00:00Z"},
		{ID: 1, Actor: "bob", Action: "login", ServerID: "srv-2", DataTier: 0, HashShort: "0011…abc", At: "2026-07-10T00:00:00Z"},
	}
	var sb strings.Builder
	if err := AuditTable(rows).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	if !strings.Contains(html, "audit-row--t2") {
		t.Error("T2 row must carry the amber-stripe class")
	}
	if !strings.Contains(html, "orders.pg_stat_statements") {
		t.Error("TARGET column must render the derived target")
	}
	if !strings.Contains(html, "a71f…9c2") {
		t.Error("HASH column must render the short hash")
	}
	if !strings.Contains(html, ">T2<") || !strings.Contains(html, ">—<") {
		t.Error("tier badge must show T2 for tier 2 and — for tier 0")
	}
	if !strings.Contains(html, `id="audit-table"`) {
		t.Error("swap-target id must be preserved")
	}
}
