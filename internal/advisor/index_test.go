package advisor

import (
	"reflect"
	"testing"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

func TestFilterColumns_equalityBeforeRange(t *testing.T) {
	got := filterColumns("((orders.status = $1) AND (orders.created_at > $2))")
	want := []string{"status", "created_at"} // equality first, then range
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterColumns = %v, want %v", got, want)
	}
}

func TestFilterColumns_dedupesAndStripsQualifier(t *testing.T) {
	got := filterColumns("(a.user_id = $1) AND (user_id = $2)")
	want := []string{"user_id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterColumns = %v, want %v", got, want)
	}
}

func TestFilterColumns_empty(t *testing.T) {
	if got := filterColumns(""); got != nil {
		t.Errorf("filterColumns(empty) = %v, want nil", got)
	}
}

func TestRecommendIndexes_aggregatesAndRanks(t *testing.T) {
	plans := []*lynceusv1.QueryPlan{
		planWithSeqScan("orders", "(status = $1)", "fp1"),
		planWithSeqScan("orders", "(status = $1)", "fp2"), // same candidate, 2 fps
		planWithSeqScan("tiny", "(flag = $1)", "fp3"),
	}
	tables := map[string]TableInfo{
		"orders": {TotalBytes: 500 << 20, SeqScans: 9000}, // big + hot
		"tiny":   {TotalBytes: 8 << 10, SeqScans: 3},      // small + cold
	}
	recs := RecommendIndexes(plans, tables)
	if len(recs) != 2 {
		t.Fatalf("recs = %d, want 2: %+v", len(recs), recs)
	}
	if recs[0].Relation != "orders" { // ranked first (bigger, hotter)
		t.Errorf("top relation = %q, want orders", recs[0].Relation)
	}
	if got := recs[0].Columns; len(got) != 1 || got[0] != "status" {
		t.Errorf("columns = %v, want [status]", got)
	}
	if recs[0].QueryCount != 2 {
		t.Errorf("query count = %d, want 2", recs[0].QueryCount)
	}
}

// planWithSeqScan builds a one-node QueryPlan with a Seq Scan carrying the
// given normalized condition (test helper).
func planWithSeqScan(rel, cond, fp string) *lynceusv1.QueryPlan {
	return &lynceusv1.QueryPlan{Fingerprint: fp, Root: &lynceusv1.PlanNode{
		NodeType: "Seq Scan", RelationName: rel, NormalizedCondition: cond,
		ActualRows: 1000, ActualLoops: 1,
	}}
}
