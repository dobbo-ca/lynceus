package web

import (
	"context"
	"strings"
	"testing"
)

func TestQueriesScreen_HeadersAndT2Column(t *testing.T) {
	rows := []TopQuery{{
		Fingerprint: "3f2a", NormalizedQuery: "select * from orders where id=$1",
		Calls: 1200, TotalTimeMs: 4800, MeanTimeMs: 4, Rows: 0, CacheHitPct: -1,
		InsightCount: 2, ClusterID: "orders-prod",
	}}
	var sb strings.Builder
	_ = QueriesScreen(QuerySort{Col: "total", Dir: "desc"}, rows).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Top Queries", "badge--live", "FINGERPRINT", "NORMALIZED QUERY",
		"CALLS", "TOTAL", "MEAN", "ROWS", "CACHE", "TREND", "SAMPLE",
		"3f2a", "2▲", // insight count triangle
		"T2 ◈",                              // T2 sample-column badge
		"MAY CONTAIN LITERALS",              // T2 caption
		"/databases/orders-prod/query/3f2a", // drilldown link
	} {
		if !strings.Contains(html, want) {
			t.Errorf("QueriesScreen missing %q", want)
		}
	}
	if strings.Contains(html, "system-ui") || strings.Contains(html, "#2b6cb0") {
		t.Error("QueriesScreen must not use legacy light styling")
	}
}

func TestQueriesScreen_UnknownMetricsRenderDash(t *testing.T) {
	rows := []TopQuery{{Fingerprint: "x", NormalizedQuery: "q", Calls: 1, TotalTimeMs: 1, MeanTimeMs: 1, Rows: 0, CacheHitPct: -1}}
	var sb strings.Builder
	_ = QueriesTable(QuerySort{Col: "total", Dir: "desc"}, rows).Render(context.Background(), &sb)
	if !strings.Contains(sb.String(), "—") {
		t.Error("unknown ROWS/CACHE should render em-dash")
	}
}
