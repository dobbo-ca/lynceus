package insight_test

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestInefficientIndex_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "inefficient_index.json"))
	var ii *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindInefficientIndex {
			ii = &got[i]
		}
	}
	if ii == nil {
		t.Fatalf("no inefficient_index insight: %+v", got)
	}
	if ii.Relation != "orders" {
		t.Errorf("relation = %q, want orders", ii.Relation)
	}
	if ii.RowsScanned != 20000 || ii.RowsReturned != 100 {
		t.Errorf("scanned/returned = %d/%d, want 20000/100", ii.RowsScanned, ii.RowsReturned)
	}
}

func TestInefficientIndex_selectiveIndex_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "index_selective.json")) {
		if in.Kind == insight.KindInefficientIndex {
			t.Errorf("selective index flagged: %+v", in)
		}
	}
}

// A Seq Scan with a discarding filter is SlowScan's job, never InefficientIndex.
func TestInefficientIndex_seqScan_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "slowscan_events.json")) {
		if in.Kind == insight.KindInefficientIndex {
			t.Errorf("seq scan flagged as inefficient index: %+v", in)
		}
	}
}
