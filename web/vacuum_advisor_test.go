package web

import (
	"context"
	"strings"
	"testing"
)

func TestVacuumAdvisorScreen_FourPanels(t *testing.T) {
	vm := VacuumAdvisorVM{
		ScopeLabel: "orders-prod ▸ db orders ▸ srv-orders-primary",
		Bloat:      []VacBloatRow{{Relation: "orders", PctLabel: "37%", WidthPct: 37, ColorVar: "var(--warn)", Dead: "12,340 dead", Wasted: "48 MB"}},
		Perf:       []VacPerfRow{{Label: "autovacuum lag", Value: "2 tables", Detail: "dead tuples exceed threshold"}},
		Activity:   []VacActivityRow{{Relation: "orders", Last: "3h ago", LastColorVar: "var(--dim)", Analyze: "1d ago"}},
		Freeze:     []VacFreezeRow{{Name: "orders", Kind: "xid", AgeLabel: "182M", PctLabel: "91%", WidthPct: 70, ColorVar: "var(--warn)"}},
	}
	var sb strings.Builder
	_ = VacuumAdvisorScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Vacuum Advisor", "BLOAT — DEAD TUPLE SHARE", "PERFORMANCE",
		"ACTIVITY — LAST AUTOVACUUM / ANALYZE", "FREEZING — WRAPAROUND RISK",
		"orders", "37%", "12,340 dead", "3h ago", "182M",
		"orders-prod ▸ db orders ▸ srv-orders-primary",
		"AUTOVACUUM_FREEZE_MAX_AGE",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("VacuumAdvisorScreen missing %q", want)
		}
	}
}
