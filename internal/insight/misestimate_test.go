package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func find(got []insight.Insight, k insight.Kind) *insight.Insight {
	for i := range got {
		if got[i].Kind == k {
			return &got[i]
		}
	}
	return nil
}

func TestStaleStats_leafScan_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "stalestats_seqscan.json"))
	ss := find(got, insight.KindStaleStats)
	if ss == nil {
		t.Fatalf("no stale_stats insight: %+v", got)
	}
	if ss.Relation != "events" {
		t.Errorf("relation = %q, want events", ss.Relation)
	}
	if !strings.Contains(ss.Detail, "ANALYZE") {
		t.Errorf("detail missing ANALYZE recommendation: %q", ss.Detail)
	}
	// A leaf scan must NOT also trip Mis-Estimate.
	if find(got, insight.KindMisEstimate) != nil {
		t.Errorf("leaf scan double-flagged as mis_estimate")
	}
}

func TestMisEstimate_join_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "misestimate_join.json"))
	me := find(got, insight.KindMisEstimate)
	if me == nil {
		t.Fatalf("no mis_estimate insight: %+v", got)
	}
	if !strings.Contains(me.NodePath, "Nested Loop") {
		t.Errorf("node path = %q, want a Nested Loop", me.NodePath)
	}
}

func TestEstimate_accurate_notFlagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "estimate_accurate.json"))
	if find(got, insight.KindStaleStats) != nil || find(got, insight.KindMisEstimate) != nil {
		t.Errorf("accurate estimates flagged: %+v", got)
	}
}
