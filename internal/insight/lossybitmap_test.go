package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestLossyBitmap_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "lossybitmap.json"))
	var lb *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindLossyBitmap {
			lb = &got[i]
		}
	}
	if lb == nil {
		t.Fatalf("no lossy_bitmap insight: %+v", got)
	}
	if lb.Severity != insight.SeverityMedium { // 200000 removed: >=100000, <1000000
		t.Errorf("severity = %q, want medium", lb.Severity)
	}
	if !strings.Contains(lb.Detail, "lossy") {
		t.Errorf("detail missing lossy hint: %q", lb.Detail)
	}
	for _, banned := range []string{"'", "=", "::"} {
		if strings.Contains(lb.Detail, banned) {
			t.Errorf("possible literal %q in detail: %q", banned, lb.Detail)
		}
	}
}

func TestLossyBitmap_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "bitmap_keptmost.json")) {
		if in.Kind == insight.KindLossyBitmap {
			t.Errorf("kept-most Bitmap Heap Scan flagged: %+v", in)
		}
	}
}
