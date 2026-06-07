package insight_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/insight"
	"github.com/dobbo-ca/lynceus/internal/planextract"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// planFromFixture extracts a T1 QueryPlan from an auto_explain JSON fixture,
// exercising the real planextract path (incl. rows_removed_by_filter).
func planFromFixture(t *testing.T, name string) *lynceusv1.QueryPlan {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	qp, err := planextract.Extract(b, "fp-"+name, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("extract %s: %v", name, err)
	}
	return qp
}

func TestDetectAll_nilPlan_returnsNil(t *testing.T) {
	if got := insight.DetectAll(nil); got != nil {
		t.Errorf("DetectAll(nil) = %v, want nil", got)
	}
}

func TestDetectPlans_emptySlice_returnsNil(t *testing.T) {
	if got := insight.DetectPlans(nil); got != nil {
		t.Errorf("DetectPlans(nil) = %v, want nil", got)
	}
}

func TestDetectAll_planWithNoAntiPattern_returnsNil(t *testing.T) {
	// A trivial Index Scan plan trips no detector.
	qp := &lynceusv1.QueryPlan{
		Fingerprint: "fp-x",
		Root: &lynceusv1.PlanNode{
			NodeType:     "Index Scan",
			RelationName: "users",
			IndexName:    "users_pkey",
			ActualRows:   1,
			ActualLoops:  1,
		},
	}
	if got := insight.DetectAll(qp); got != nil {
		t.Errorf("DetectAll(index scan) = %v, want nil", got)
	}
}

func TestSlowScan_positive_highSeverity(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "slowscan_events.json"))
	if len(got) != 1 {
		t.Fatalf("insights = %d, want 1: %+v", len(got), got)
	}
	in := got[0]
	if in.Kind != insight.KindSlowScan {
		t.Errorf("kind = %q, want slow_scan", in.Kind)
	}
	if in.Severity != insight.SeverityHigh {
		t.Errorf("severity = %q, want high", in.Severity)
	}
	if in.Relation != "events" {
		t.Errorf("relation = %q, want events", in.Relation)
	}
	if in.RowsScanned != 100000 {
		t.Errorf("rows_scanned = %d, want 100000", in.RowsScanned)
	}
	if in.RowsReturned != 10 {
		t.Errorf("rows_returned = %d, want 10", in.RowsReturned)
	}
	if in.NodePath != "Seq Scan(events)" {
		t.Errorf("node_path = %q, want Seq Scan(events)", in.NodePath)
	}
}

func TestSlowScan_smallScan_noInsight(t *testing.T) {
	if got := insight.DetectAll(planFromFixture(t, "seqscan_small.json")); got != nil {
		t.Errorf("small scan flagged: %+v", got)
	}
}

func TestSlowScan_fullRead_noInsight(t *testing.T) {
	if got := insight.DetectAll(planFromFixture(t, "seqscan_fullread.json")); got != nil {
		t.Errorf("full-read scan flagged: %+v", got)
	}
}

func TestSlowScan_noAnalyze_noInsight(t *testing.T) {
	if got := insight.DetectAll(planFromFixture(t, "seqscan_noanalyze.json")); got != nil {
		t.Errorf("non-ANALYZE scan flagged: %+v", got)
	}
}

func TestSlowScan_loops_totalsAndSeverity(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "seqscan_loops.json"))
	if len(got) != 1 {
		t.Fatalf("insights = %d, want 1: %+v", len(got), got)
	}
	in := got[0]
	if in.RowsScanned != 5100 {
		t.Errorf("rows_scanned = %d, want 5100 (51*100 loops)", in.RowsScanned)
	}
	if in.RowsReturned != 100 {
		t.Errorf("rows_returned = %d, want 100 (1*100 loops)", in.RowsReturned)
	}
	if in.Severity != insight.SeverityMedium {
		t.Errorf("severity = %q, want medium", in.Severity)
	}
	if in.NodePath != "Nested Loop > Seq Scan(b)" {
		t.Errorf("node_path = %q, want Nested Loop > Seq Scan(b)", in.NodePath)
	}
}

func TestSlowScan_detailHasNoLiteral(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "slowscan_events.json"))
	if len(got) != 1 {
		t.Fatalf("insights = %d, want 1", len(got))
	}
	for _, banned := range []string{"'", "error", "2024-01-01", "kind ="} {
		if strings.Contains(got[0].Detail, banned) {
			t.Errorf("literal %q leaked into Detail: %q", banned, got[0].Detail)
		}
		if strings.Contains(got[0].NodePath, banned) {
			t.Errorf("literal %q leaked into NodePath: %q", banned, got[0].NodePath)
		}
	}
}

func TestDetectPlans_aggregatesAcrossPlans(t *testing.T) {
	plans := []*lynceusv1.QueryPlan{
		planFromFixture(t, "slowscan_events.json"),
		planFromFixture(t, "seqscan_small.json"),
		planFromFixture(t, "seqscan_loops.json"),
	}
	if got := insight.DetectPlans(plans); len(got) != 2 {
		t.Errorf("DetectPlans = %d insights, want 2", len(got))
	}
}
