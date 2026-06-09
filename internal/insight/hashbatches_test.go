package insight_test

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/insight"
)

func TestHashBatches_multiBatch_flagged(t *testing.T) {
	got := insight.DetectAll(planFromFixture(t, "hashjoin_batches.json"))
	var h *insight.Insight
	for i := range got {
		if got[i].Kind == insight.KindHashBatches {
			h = &got[i]
		}
	}
	if h == nil {
		t.Fatalf("no hash_batches insight: %+v", got)
	}
	if h.Severity != insight.SeverityMedium { // 8 batches
		t.Errorf("severity = %q, want medium", h.Severity)
	}
	if !strings.Contains(h.Detail, "work_mem") {
		t.Errorf("detail missing work_mem hint: %q", h.Detail)
	}
}

func TestHashBatches_singleBatch_notFlagged(t *testing.T) {
	for _, in := range insight.DetectAll(planFromFixture(t, "hashjoin_onebatch.json")) {
		if in.Kind == insight.KindHashBatches {
			t.Errorf("single-batch hash flagged: %+v", in)
		}
	}
}
