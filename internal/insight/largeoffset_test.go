package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestLargeOffset_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "largeoffset.json"))
	var lo *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindLargeOffset {
			lo = &got[i]
		}
	}
	if lo == nil {
		t.Fatalf("no large_offset insight: %+v", got)
	}
	if lo.Severity != insight.SeverityMedium { // 49950 discarded: >=10000, <100000
		t.Errorf("severity = %q, want medium", lo.Severity)
	}
	if !strings.Contains(lo.Detail, "OFFSET") {
		t.Errorf("detail missing OFFSET: %q", lo.Detail)
	}
	for _, banned := range []string{"'", "=", "::"} {
		if strings.Contains(lo.Detail, banned) {
			t.Errorf("possible literal %q in detail: %q", banned, lo.Detail)
		}
	}
}

func TestLargeOffset_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "limit_smalldiscard.json")) {
		if in.Kind == insight.KindLargeOffset {
			t.Errorf("small-discard Limit flagged: %+v", in)
		}
	}
}
