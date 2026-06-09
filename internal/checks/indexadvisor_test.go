package checks

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/advisor"
)

func TestIndexAdvisorWarnsOnHotSeqScan(t *testing.T) {
	in := Input{ServerID: "srv-a", IndexRecs: []advisor.IndexRecommendation{
		{Relation: "public.orders", Columns: []string{"customer_id"}, QueryCount: 12,
			TotalBytes: 800_000_000, SeqScans: 500_000, Rationale: "frequent seq scan"},
		{Relation: "public.tiny", Columns: []string{"k"}, QueryCount: 1,
			TotalBytes: 4096, SeqScans: 3, Rationale: "rare"},
	}}
	got := IndexAdvisorCheck{}.Eval(&in)
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(got), got)
	}
	var hot *Result
	for i := range got {
		if got[i].Object == "public.orders" {
			hot = &got[i]
		}
	}
	if hot == nil || hot.Severity != SeverityWarning || hot.CheckID != "queries.missing_index" {
		t.Fatalf("hot recommendation wrong: %+v", hot)
	}
	for _, banned := range []string{"'", "::"} {
		for _, r := range got {
			if strings.Contains(r.Detail, banned) {
				t.Fatalf("possible literal %q in details: %q", banned, r.Detail)
			}
		}
	}
}
