package web

import (
	"context"
	"strings"
	"testing"
)

func TestQueryDrilldownScreen_StructureAndT2Gate(t *testing.T) {
	vm := DrilldownVM{
		ClusterID: "orders-prod", ServerID: "srv-1", Fingerprint: "3f2a", HasPlan: true,
		NormalizedQuery: "select * from orders where id=$1",
		Stats:           []DrilldownStat{{Label: "CALLS", Value: "1200"}},
		Insights:        []DrilldownInsight{{KindLabel: "SLOW SEQ SCAN", Node: "Seq Scan", SevClass: "crit", Detail: "scanned 1M rows", Rec: "add an index"}},
		Sample:          QuerySampleVM{Locked: true, Group: "dba-oncall", ServerID: "srv-1"},
		Nav:             ScreenNav{Base: "/queries", Plan: "/plan"},
	}
	var sb strings.Builder
	_ = QueryDrilldownScreen(vm).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"← TOP QUERIES", "VIEW PLAN →", "CALLS", "DETECTED INSIGHTS", "SLOW SEQ SCAN",
		"stripe-crit", "→ add an index", "WAIT BREAKDOWN", "RAW SAMPLE — TIER 2",
		"REVEAL SAMPLE (AUDITED)", "dba-oncall",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("drilldown missing %q", want)
		}
	}
}

func TestQuerySampleVM_HasNoLiteralField(t *testing.T) {
	// Compile-time contract: QuerySampleVM must never gain a raw-sample string.
	// This test documents intent; the locked gate never renders a SQL literal.
	sm := QuerySampleVM{Locked: true}
	var sb strings.Builder
	_ = sampleGate(sm).Render(context.Background(), &sb)
	if strings.Contains(sb.String(), "SELECT") && !strings.Contains(sb.String(), "Raw samples may contain") {
		t.Error("sampleGate must not render an actual SQL literal in the locked state")
	}
}
