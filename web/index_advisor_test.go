package web

import (
	"context"
	"strings"
	"testing"
)

func TestIndexAdvisorScreen_DDLBenefitEvidence(t *testing.T) {
	rows := []IndexAdvisorRow{{
		Relation: "orders", Columns: "customer_id, created_at", QueryCount: 12,
		SizePretty: "500 MB", SeqScans: 900, Rationale: "seq scans on filtered columns",
		DDL: "CREATE INDEX ON orders (customer_id, created_at)", BenefitPct: 64,
		EvidenceFP: "3f2a", ClusterID: "orders-prod", Cluster: "orders-prod", Database: "orders", Server: "srv-1",
	}}
	var sb strings.Builder
	_ = IndexAdvisorScreen(rows).Render(context.Background(), &sb)
	html := sb.String()
	for _, want := range []string{
		"Index Advisor", "badge--live", "CREATE INDEX ON orders (customer_id, created_at)",
		"EST. BENEFIT", "64%", "EVIDENCE", "WHY",
		"/databases/orders-prod/query/3f2a", "orders-prod ▸ orders ▸ srv-1",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("IndexAdvisorScreen missing %q", want)
		}
	}
}
