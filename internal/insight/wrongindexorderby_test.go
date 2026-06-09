package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestWrongIndexOrderBy_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "wrongindexorderby.json"))
	var wi *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindWrongIndexOrderBy {
			wi = &got[i]
		}
	}
	if wi == nil {
		t.Fatalf("no wrong_index_order_by insight: %+v", got)
	}
	if wi.Severity != insight.SeverityMedium { // 200000 removed: >=100000, <1000000
		t.Errorf("severity = %q, want medium", wi.Severity)
	}
	if !strings.Contains(wi.Detail, "ORDER BY") {
		t.Errorf("detail missing ORDER BY: %q", wi.Detail)
	}
	for _, banned := range []string{"'", "=", "::"} {
		if strings.Contains(wi.Detail, banned) {
			t.Errorf("possible literal %q in detail: %q", banned, wi.Detail)
		}
	}
}

// scan_direction "" means the index was not walked to satisfy an ORDER BY, so
// this detector must stay silent even when the filter discards a lot (that case
// belongs to InefficientIndex, not this detector).
func TestWrongIndexOrderBy_noScanDirection_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "wrongindex_noscandir.json")) {
		if in.Kind == insight.KindWrongIndexOrderBy {
			t.Errorf("non-ordered index scan flagged: %+v", in)
		}
	}
}
