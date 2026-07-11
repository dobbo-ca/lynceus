package web

import (
	"context"
	"strings"
	"testing"
)

func TestInsightsScreen_ChipsStripesAndDeepLink(t *testing.T) {
	rows := []InsightRow{{
		Kind: "slow_scan", Severity: "high", Fingerprint: "3f2a",
		Relation: "orders", NodePath: "Seq Scan(orders)", Detail: "scanned 1M rows",
		ServerID: "srv-1", ClusterID: "orders-prod",
	}}
	var sb strings.Builder
	_ = InsightsScreen(InsightFilter{}, rows).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Query Insights", "SEVERITY", "KIND", "chip",
		"SLOW SEQ SCAN", "stripe-crit", "Seq Scan(orders)",
		"scanned 1M rows", "→ Add an index",
		"/databases/orders-prod/query/3f2a", // deep-link
	} {
		if !strings.Contains(html, want) {
			t.Errorf("InsightsScreen missing %q", want)
		}
	}
}
