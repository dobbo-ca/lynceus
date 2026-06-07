package planextract_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/planextract"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// walk visits every node in the tree depth-first.
func walk(n *lynceusv1.PlanNode, fn func(*lynceusv1.PlanNode)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.GetPlans() {
		walk(c, fn)
	}
}

// assertNoLiteral fails if any string field anywhere in the plan contains a
// quoted literal or a known literal value from the source fixtures.
func assertNoLiteral(t *testing.T, qp *lynceusv1.QueryPlan) {
	t.Helper()
	banned := []string{"'", "shipped", "pending", "500.00", "900"}
	walk(qp.GetRoot(), func(n *lynceusv1.PlanNode) {
		for _, b := range banned {
			if strings.Contains(n.GetNormalizedCondition(), b) {
				t.Errorf("literal %q leaked into normalized_condition: %q", b, n.GetNormalizedCondition())
			}
		}
	})
}

func TestExtract_seqScanFilter(t *testing.T) {
	const fp = "fp-seq"
	at := time.Unix(1700000000, 0)
	qp, err := planextract.Extract(loadFixture(t, "seqscan_filter.json"), fp, at)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if qp.GetFingerprint() != fp {
		t.Errorf("fingerprint = %q, want %q", qp.GetFingerprint(), fp)
	}
	if qp.GetCapturedAtUnix() != at.Unix() {
		t.Errorf("captured_at_unix = %d, want %d", qp.GetCapturedAtUnix(), at.Unix())
	}
	if qp.GetFormatVersion() != 1 {
		t.Errorf("format_version = %d, want 1", qp.GetFormatVersion())
	}
	if qp.GetActualTotalTimeMs() != 0 {
		t.Errorf("actual_total_time_ms = %v, want 0 (no ANALYZE)", qp.GetActualTotalTimeMs())
	}
	root := qp.GetRoot()
	if root.GetNodeType() != "Aggregate" {
		t.Fatalf("root node type = %q, want Aggregate", root.GetNodeType())
	}
	if root.GetTotalCost() != 102.84 {
		t.Errorf("root total_cost = %v, want 102.84", root.GetTotalCost())
	}
	if len(root.GetPlans()) != 1 {
		t.Fatalf("root children = %d, want 1", len(root.GetPlans()))
	}
	child := root.GetPlans()[0]
	if child.GetNodeType() != "Seq Scan" || child.GetRelationName() != "orders" {
		t.Errorf("child = %q on %q, want Seq Scan on orders", child.GetNodeType(), child.GetRelationName())
	}
	if child.GetPlanRows() != 2532 {
		t.Errorf("child plan_rows = %d, want 2532", child.GetPlanRows())
	}
	if child.GetNormalizedCondition() == "" {
		t.Error("expected a normalized Filter on the Seq Scan, got empty")
	}
	assertNoLiteral(t, qp)
}

func TestExtract_indexCond(t *testing.T) {
	qp, err := planextract.Extract(loadFixture(t, "index_cond.json"), "fp-idx", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// Aggregate -> Bitmap Heap Scan -> Bitmap Index Scan
	var depth int
	var sawIndex bool
	walk(qp.GetRoot(), func(n *lynceusv1.PlanNode) {
		depth++
		if n.GetIndexName() == "orders_status_idx" {
			sawIndex = true
		}
	})
	if depth != 3 {
		t.Errorf("node count = %d, want 3", depth)
	}
	if !sawIndex {
		t.Error("expected to find index name orders_status_idx")
	}
	assertNoLiteral(t, qp)
}

func TestExtract_nestedLoopAnalyze(t *testing.T) {
	qp, err := planextract.Extract(loadFixture(t, "nestloop_analyze.json"), "fp-nl", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	root := qp.GetRoot()
	if root.GetNodeType() != "Limit" {
		t.Fatalf("root = %q, want Limit", root.GetNodeType())
	}
	if root.GetActualTotalTimeMs() != 0.034 {
		t.Errorf("root actual_total_time_ms = %v, want 0.034", root.GetActualTotalTimeMs())
	}
	if qp.GetActualTotalTimeMs() != 0.034 {
		t.Errorf("plan actual_total_time_ms = %v, want 0.034", qp.GetActualTotalTimeMs())
	}
	var sawJoin bool
	walk(root, func(n *lynceusv1.PlanNode) {
		if n.GetNodeType() == "Nested Loop" {
			sawJoin = true
			if n.GetJoinType() != "Inner" {
				t.Errorf("nested loop join_type = %q, want Inner", n.GetJoinType())
			}
		}
		if n.GetNodeType() == "Index Scan" {
			if n.GetScanDirection() != "Forward" {
				t.Errorf("index scan scan_direction = %q, want Forward", n.GetScanDirection())
			}
		}
	})
	if !sawJoin {
		t.Error("expected a Nested Loop node")
	}
	assertNoLiteral(t, qp)
}

func TestExtract_rowsRemovedByFilter(t *testing.T) {
	qp, err := planextract.Extract(loadFixture(t, "nestloop_analyze.json"), "fp-nl", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	var seq *lynceusv1.PlanNode
	walk(qp.GetRoot(), func(n *lynceusv1.PlanNode) {
		if n.GetNodeType() == "Seq Scan" {
			seq = n
		}
	})
	if seq == nil {
		t.Fatal("no Seq Scan node found in nestloop_analyze.json")
	}
	if got := seq.GetRowsRemovedByFilter(); got != 233 {
		t.Errorf("rows_removed_by_filter = %d, want 233", got)
	}
}

func TestExtract_unsupportedFormat(t *testing.T) {
	// A text-format auto_explain body (not JSON) must be rejected, not guessed.
	textBody := []byte("Aggregate  (cost=102.83..102.84 rows=1 width=8)\n  ->  Seq Scan on orders")
	if _, err := planextract.Extract(textBody, "fp", time.Unix(1, 0)); err == nil {
		t.Fatal("expected ErrUnsupportedPlanFormat for text-format body, got nil")
	}
	if _, err := planextract.Extract([]byte("   "), "fp", time.Unix(1, 0)); err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
}
